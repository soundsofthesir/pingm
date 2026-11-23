package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// maxListed is how many host names an incident or the never-answered line
// spells out before summarising the rest as a count.
const maxListed = 10

// Read parses a session log. A torn last line, as a crash would leave, is
// skipped rather than failing the whole read.
func Read(r io.Reader) ([]Event, error) {
	var out []Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("reading session log: %w", err)
	}
	return out, nil
}

// outage is one host's stretch of not answering.
type outage struct {
	host       string
	start, end time.Time
	open       bool // Still down when the session ended.
	initial    bool // Down from its first probe.
}

// Incident is a stretch of time when at least one host that answered at some
// point was not answering. Overlapping outages merge into one incident, since
// hosts dropping together usually share a cause.
type Incident struct {
	Start, End time.Time
	Fixed      bool // Every host in it came back before the session ended.
	Hosts      []string
	Events     []Event // What the person and the network did meanwhile.
}

// Summary is what a session log says about outages and their fixes.
type Summary struct {
	Start, End  time.Time
	Args        []string
	Incidents   []Incident
	NeverUp     []string
	Unresolved  []HostInfo
	EventsTotal int
}

// Summarise works out the incidents in a session's events.
func Summarise(events []Event) Summary {
	var s Summary
	s.EventsTotal = len(events)
	if len(events) == 0 {
		return s
	}
	s.Start, s.End = events[0].T, events[len(events)-1].T

	var order []string
	current := map[string]*outage{}
	everUp := map[string]bool{}
	var outages []outage

	for _, e := range events {
		switch e.Kind {
		case KindStart:
			s.Start, s.Args = e.T, e.Args
			for _, h := range e.Hosts {
				if h.Error != "" {
					s.Unresolved = append(s.Unresolved, h)
				}
			}
		case KindEnd:
			s.End = e.T
		case KindState:
			if _, seen := everUp[e.Host]; !seen {
				order = append(order, e.Host)
				everUp[e.Host] = false
			}
			switch e.To {
			case "down":
				current[e.Host] = &outage{host: e.Host, start: e.T, initial: e.From == ""}
			case "up":
				everUp[e.Host] = true
				if o := current[e.Host]; o != nil {
					o.end = e.T
					outages = append(outages, *o)
					delete(current, e.Host)
				}
			}
		}
	}

	for _, host := range order {
		o := current[host]
		if o == nil {
			continue
		}
		if o.initial && !everUp[host] {
			s.NeverUp = append(s.NeverUp, host)
			continue
		}
		o.end, o.open = s.End, true
		outages = append(outages, *o)
	}

	s.Incidents = merge(outages)
	for i := range s.Incidents {
		inc := &s.Incidents[i]
		for _, e := range events {
			if isActivity(e.Kind) && !e.T.Before(inc.Start) && !e.T.After(inc.End) {
				inc.Events = append(inc.Events, e)
			}
		}
	}
	return s
}

func isActivity(kind string) bool {
	switch kind {
	case KindNet, KindCmd, KindNote, KindAction:
		return true
	}
	return false
}

// merge joins overlapping outages into incidents, in time order.
func merge(outages []outage) []Incident {
	sort.SliceStable(outages, func(i, j int) bool { return outages[i].start.Before(outages[j].start) })

	var out []Incident
	for _, o := range outages {
		if n := len(out); n > 0 && !o.start.After(out[n-1].End) {
			inc := &out[n-1]
			if o.end.After(inc.End) {
				inc.End = o.end
			}
			inc.Fixed = inc.Fixed && !o.open
			if !contains(inc.Hosts, o.host) {
				inc.Hosts = append(inc.Hosts, o.host)
			}
			continue
		}
		out = append(out, Incident{Start: o.start, End: o.end, Fixed: !o.open, Hosts: []string{o.host}})
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Report prints a session's incidents and what happened during each one.
func Report(events []Event, path string, w io.Writer, loc *time.Location) error {
	if loc == nil {
		loc = time.Local
	}
	s := Summarise(events)
	if s.EventsTotal == 0 {
		_, err := fmt.Fprintf(w, "%s is empty\n", path)
		return err
	}

	var b strings.Builder
	clock := func(t time.Time) string { return t.In(loc).Format("15:04:05") }

	fmt.Fprintf(&b, "Session %s to %s (%s)\n",
		s.Start.In(loc).Format("2006-01-02 15:04:05"), clock(s.End), span(s.End.Sub(s.Start)))
	if len(s.Args) > 0 {
		fmt.Fprintf(&b, "pingm %s\n", clean(strings.Join(s.Args, " ")))
	}
	fmt.Fprintf(&b, "Log: %s\n", clean(path))

	if len(s.Incidents) == 0 {
		b.WriteString("\nNo outages.\n")
	}
	for i, inc := range s.Incidents {
		end, outcome := clock(inc.End), "fixed"
		if !inc.Fixed {
			end, outcome = "end", "still down at the end"
		}
		fmt.Fprintf(&b, "\nIncident %d: %s to %s (%s), %s\n",
			i+1, clock(inc.Start), end, span(inc.End.Sub(inc.Start)), outcome)
		fmt.Fprintf(&b, "  Down: %s\n", listed(inc.Hosts))
		if len(inc.Events) == 0 {
			b.WriteString("  Nothing recorded meanwhile\n")
		}
		for _, e := range inc.Events {
			fmt.Fprintf(&b, "  %s  %-7s  %s\n", clock(e.T), label(e.Kind), describe(e))
		}
	}

	if len(s.Unresolved) > 0 {
		names := make([]string, len(s.Unresolved))
		for i, h := range s.Unresolved {
			names[i] = h.Name
		}
		fmt.Fprintf(&b, "\nDid not resolve: %s\n", listed(names))
	}
	if len(s.NeverUp) > 0 {
		fmt.Fprintf(&b, "\nNever answered: %s\n", listed(s.NeverUp))
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// clean drops control characters, so text from shell history, Wi-Fi names or
// a hand-edited log cannot send escape sequences to the terminal.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

func label(kind string) string {
	switch kind {
	case KindNet:
		return "network"
	case KindCmd:
		return "command"
	case KindAction:
		return "key"
	default:
		return kind
	}
}

func describe(e Event) string {
	switch e.Kind {
	case KindNet:
		return clean(strings.Join(e.Changes, "; "))
	case KindCmd:
		return clean(e.Cmd)
	default:
		return clean(e.Text)
	}
}

func listed(names []string) string {
	if len(names) <= maxListed {
		return clean(strings.Join(names, ", "))
	}
	return clean(fmt.Sprintf("%s and %d more", strings.Join(names[:maxListed], ", "), len(names)-maxListed))
}

// span formats a duration to the second, like "2m 32s".
func span(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
