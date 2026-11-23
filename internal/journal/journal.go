package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maxajbarlow/pingm/internal/monitor"
)

const (
	// EnvEnable turns the session log on when set to 1, true, yes or on.
	EnvEnable = "PINGM_SESSION_LOG"
	// EnvDir overrides where session logs are written.
	EnvDir = "PINGM_LOG_DIR"

	// statsWindow is how often each host's latency and loss are summarised.
	statsWindow = time.Minute
	// keepFor is how long old session logs are kept before Open removes them.
	keepFor = 30 * 24 * time.Hour

	fileExt = ".jsonl"
)

// Enabled reports whether the environment asks for a session log.
func Enabled(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvEnable))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Dir is where session logs go: PINGM_LOG_DIR if set, otherwise the usual
// place for logs on each system.
func Dir(getenv func(string) string, goos, home string) string {
	if d := getenv(EnvDir); d != "" {
		return d
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Logs", "pingm")
	case "windows":
		if local := getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "pingm", "logs")
		}
		return filepath.Join(home, "AppData", "Local", "pingm", "logs")
	default:
		if state := getenv("XDG_STATE_HOME"); state != "" {
			return filepath.Join(state, "pingm")
		}
		return filepath.Join(home, ".local", "state", "pingm")
	}
}

// hostTrack is the running tally for one host.
type hostTrack struct {
	state     string
	winStart  time.Time
	sent      int
	lost      int
	rttSum    time.Duration
	rttMin    time.Duration
	rttMax    time.Duration
	totalSent int
	totalLost int
}

// Journal writes one session's log. It satisfies monitor.Recorder, and every
// method is safe to call from any goroutine.
type Journal struct {
	mu     sync.Mutex
	w      *bufio.Writer
	closer io.Closer
	enc    *json.Encoder
	err    error
	now    func() time.Time
	path   string

	order []string
	hosts map[string]*hostTrack
}

// Open creates a new log file in dir and removes logs older than 30 days.
func Open(dir string, now func() time.Time) (*Journal, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create session log folder %s: %w", dir, err)
	}
	prune(dir, now())

	name := fmt.Sprintf("%s-%d%s", now().Format("2006-01-02T150405"), os.Getpid(), fileExt)
	path := filepath.Join(dir, name)
	// The log holds shell commands, so only the owner may read it.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open session log %s: %w", path, err)
	}
	j := New(f, now)
	j.path = path
	return j, nil
}

// New writes to an already-open destination, which it closes if it can.
func New(w io.Writer, now func() time.Time) *Journal {
	if now == nil {
		now = time.Now
	}
	bw := bufio.NewWriter(w)
	j := &Journal{w: bw, enc: json.NewEncoder(bw), now: now, hosts: map[string]*hostTrack{}}
	if c, ok := w.(io.Closer); ok {
		j.closer = c
	}
	return j
}

// Path is the log file, or "" when it was not opened from a folder.
func (j *Journal) Path() string { return j.path }

func prune(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != fileExt {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > keepFor {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// write stamps and appends one event, flushing at once so the file is
// readable while the session runs. Events are few enough for that to be
// cheap. The first error is kept and later writes are skipped.
func (j *Journal) write(e Event) {
	if j.err != nil {
		return
	}
	if e.T.IsZero() {
		e.T = j.now()
	}
	if err := j.enc.Encode(e); err != nil {
		j.err = fmt.Errorf("writing session log: %w", err)
		return
	}
	if err := j.w.Flush(); err != nil {
		j.err = fmt.Errorf("writing session log: %w", err)
	}
}

// Log appends an event.
func (j *Journal) Log(e Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.write(e)
}

// Start records the session's settings. Hosts are registered in table order
// so the final totals come out in the same order.
func (j *Journal) Start(e Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.Kind = KindStart
	if e.OS == "" {
		e.OS = runtime.GOOS + "/" + runtime.GOARCH
	}
	for _, h := range e.Hosts {
		j.track(h.Name)
	}
	j.write(e)
}

// Note records something the person typed.
func (j *Journal) Note(text string) {
	j.Log(Event{Kind: KindNote, Text: text})
}

// Action records a key press that changes what pingm does.
func (j *Journal) Action(text string) {
	j.Log(Event{Kind: KindAction, Text: text})
}

func (j *Journal) track(host string) *hostTrack {
	h, ok := j.hosts[host]
	if !ok {
		h = &hostTrack{}
		j.hosts[host] = h
		j.order = append(j.order, host)
	}
	return h
}

// Record folds in one probe outcome: a state event when the host flips, and a
// stats event once a minute of probes has built up.
func (j *Journal) Record(r monitor.Result, at time.Time, host string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	h := j.track(host)
	state := "down"
	if r.OK {
		state = "up"
	}
	if state != h.state {
		e := Event{T: at, Kind: KindState, Host: host, From: h.state, To: state}
		if r.OK {
			e.RTT = ms(r.RTT)
		}
		j.write(e)
		h.state = state
	}

	if h.winStart.IsZero() {
		h.winStart = at
	}
	h.sent++
	h.totalSent++
	if r.OK {
		h.rttSum += r.RTT
		if h.rttMin == 0 || r.RTT < h.rttMin {
			h.rttMin = r.RTT
		}
		h.rttMax = max(h.rttMax, r.RTT)
	} else {
		h.lost++
		h.totalLost++
	}
	if at.Sub(h.winStart) >= statsWindow {
		j.flushStats(host, h, at)
	}
	return j.err
}

func (j *Journal) flushStats(host string, h *hostTrack, at time.Time) {
	if h.sent == 0 {
		return
	}
	e := Event{T: at, Kind: KindStats, Host: host, Sent: h.sent, Lost: h.lost}
	if ok := h.sent - h.lost; ok > 0 {
		e.MinMs = *ms(h.rttMin)
		e.AvgMs = *ms(h.rttSum / time.Duration(ok))
		e.MaxMs = *ms(h.rttMax)
	}
	j.write(e)
	*h = hostTrack{state: h.state, totalSent: h.totalSent, totalLost: h.totalLost}
}

// Close writes the last partial minute of stats and the session totals, then
// closes the file. It returns the first error the log hit.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	at := j.now()
	totals := make([]HostTotals, 0, len(j.order))
	for _, host := range j.order {
		h := j.hosts[host]
		j.flushStats(host, h, at)
		totals = append(totals, HostTotals{Host: host, Sent: h.totalSent, Lost: h.totalLost})
	}
	j.write(Event{T: at, Kind: KindEnd, Totals: totals})

	if j.closer != nil {
		if err := j.closer.Close(); err != nil && j.err == nil {
			j.err = fmt.Errorf("closing session log: %w", err)
		}
	}
	return j.err
}

// ms converts to milliseconds, rounded to the microsecond.
func ms(d time.Duration) *float64 {
	v := float64(d.Round(time.Microsecond)) / float64(time.Millisecond)
	return &v
}

// Latest returns the newest session log in dir.
func Latest(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no session logs in %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == fileExt {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no session logs in %s", dir)
	}
	// Names start with a sortable timestamp.
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}
