package monitor

import (
	"context"
	"net"
	"sync"
	"time"
)

// HostView is a consistent snapshot of one host, shaped for display. The UI
// never touches live statistics, so a redraw cannot tear across an update.
type HostView struct {
	ID         int // Stable for the host's life in the table.
	Display    string
	State      State
	Last       time.Duration
	Min        time.Duration
	Max        time.Duration
	Avg        time.Duration
	Jitter     time.Duration
	Loss       float64
	HasLatency bool
	HasHistory bool
	HasJitter  bool
	Recent     []Sample
	ResolveErr error
}

// MaxAutoTimeout caps the timeout derived from the interval, so a long
// interval cannot leave a dead host looking merely slow for minutes.
const MaxAutoTimeout = 2 * time.Second

// Monitor probes a set of hosts and maintains their running statistics.
type Monitor struct {
	prober  *Prober
	resolve Resolver

	// autoTimeout means the reply deadline follows the interval. It is false
	// when the caller pinned a timeout explicitly, which must then survive an
	// interval change.
	autoTimeout bool

	// recorder, when set, is handed every probe outcome as it is folded in.
	// It is written once before Run and only read inside it, so it needs no
	// lock of its own.
	recorder Recorder

	// mu guards the host list and its statistics. targets and stats are in
	// table order and always the same length; pos maps a target ID to its
	// index in both, because hosts can be added and removed while running.
	mu      sync.RWMutex
	targets []Target
	stats   []Stats
	pos     map[int]int
}

// Resolver turns a hostname into an IPv4 address. It is a field so tests can
// avoid depending on DNS.
type Resolver func(host string) (net.IP, error)

// New resolves every host and opens the probe socket. A timeout of zero means
// derive it from the interval and keep it in step as the interval changes.
func New(hosts []string, interval, timeout time.Duration, resolve Resolver) (*Monitor, error) {
	auto := timeout <= 0
	if auto {
		timeout = autoTimeoutFor(interval)
	}
	if resolve == nil {
		resolve = resolveIPv4
	}

	prober, err := NewProber(resolveAll(hosts, resolve), interval, timeout)
	if err != nil {
		return nil, err
	}
	m := &Monitor{
		prober:      prober,
		resolve:     resolve,
		autoTimeout: auto,
	}
	m.adopt(prober.snapshotTargets())
	return m, nil
}

// resolveAll looks up every host. A name that will not resolve is kept in the
// table and reported as unresolved, which is more useful than dropping the
// row the user asked for.
func resolveAll(hosts []string, resolve Resolver) []Target {
	targets := make([]Target, len(hosts))
	for i, h := range hosts {
		ip, err := resolve(h)
		targets[i] = Target{Display: h, IP: ip, Err: err}
	}
	return targets
}

// adopt appends targets the prober has already numbered. Callers hold mu, or
// own the Monitor outright as New does.
func (m *Monitor) adopt(targets []Target) {
	if m.pos == nil {
		m.pos = make(map[int]int, len(targets))
	}
	for _, t := range targets {
		m.pos[t.ID] = len(m.targets)
		m.targets = append(m.targets, t)
		m.stats = append(m.stats, Stats{})
	}
}

// Add resolves hosts and starts probing them from the next round, appended to
// the end of the table. Hosts already in the table are skipped. It returns how
// many were added. Resolution can be slow, so it is done before any lock is
// taken.
func (m *Monitor) Add(hosts []string) int {
	m.mu.RLock()
	present := make(map[string]bool, len(m.targets))
	for _, t := range m.targets {
		present[t.Display] = true
	}
	m.mu.RUnlock()

	var fresh []string
	for _, h := range hosts {
		if !present[h] {
			present[h] = true
			fresh = append(fresh, h)
		}
	}
	if len(fresh) == 0 {
		return 0
	}
	targets := resolveAll(fresh, m.resolve)

	// The table learns of the targets before any result for them can be
	// folded in, because Run needs mu to record one.
	m.mu.Lock()
	m.adopt(m.prober.Add(targets...))
	m.mu.Unlock()
	return len(targets)
}

// Remove stops probing the host with this ID and drops it from the table.
func (m *Monitor) Remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.pos[id]
	if !ok {
		return
	}
	m.prober.Remove(id)
	m.targets = append(m.targets[:i:i], m.targets[i+1:]...)
	m.stats = append(m.stats[:i:i], m.stats[i+1:]...)
	delete(m.pos, id)
	for j := i; j < len(m.targets); j++ {
		m.pos[m.targets[j].ID] = j
	}
}

func autoTimeoutFor(interval time.Duration) time.Duration {
	if interval < MaxAutoTimeout {
		return interval
	}
	return MaxAutoTimeout
}

// Recorder receives every probe outcome, in the order the monitor folds them
// in, for callers that want a durable log alongside the live table.
// Implementations must not block for long: the probe loops push results
// synchronously, so a slow recorder slows probing.
type Recorder interface {
	Record(r Result, at time.Time, host string) error
}

// SetRecorder attaches a recorder. It must be called before Run.
func (m *Monitor) SetRecorder(r Recorder) { m.recorder = r }

// Hosts returns the display name of every target, in table order.
func (m *Monitor) Hosts() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, len(m.targets))
	for i, t := range m.targets {
		names[i] = t.Display
	}
	return names
}

// Interval is the current gap between probes for a given host.
func (m *Monitor) Interval() time.Duration { return m.prober.Interval() }

// SetInterval changes the probe cadence while running, carrying the reply
// deadline with it unless the caller pinned one.
func (m *Monitor) SetInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	m.prober.SetInterval(d)
	if m.autoTimeout {
		m.prober.SetTimeout(autoTimeoutFor(d))
	}
}

func resolveIPv4(host string) (net.IP, error) {
	addr, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return nil, err
	}
	return addr.IP, nil
}

// Privileged reports whether a raw socket was required.
func (m *Monitor) Privileged() bool { return m.prober.Privileged() }

// Run probes until ctx is cancelled, or until limit rounds have completed when
// limit is positive. It returns once every result has been folded in.
func (m *Monitor) Run(ctx context.Context, limit int) {
	go m.prober.Run(ctx, limit)

	for r := range m.prober.Results() {
		// A result can still be queued for a host removed a moment ago;
		// with no row left to credit, it is dropped.
		m.mu.Lock()
		i, ok := m.pos[r.Index]
		var host string
		if ok {
			m.stats[i].Record(r)
			host = m.targets[i].Display
		}
		m.mu.Unlock()

		if ok && m.recorder != nil {
			// A failing recorder must not take the table down with it; it
			// keeps its own error for the caller to report at close.
			_ = m.recorder.Record(r, time.Now(), host)
		}
	}
}

// Close releases the probe socket.
func (m *Monitor) Close() error { return m.prober.Close() }

// Snapshot copies the current state of every host.
func (m *Monitor) Snapshot() []HostView {
	m.mu.RLock()
	defer m.mu.RUnlock()

	views := make([]HostView, len(m.stats))
	for i := range m.stats {
		s := &m.stats[i]
		// A name that never resolved is reported as such rather than as a
		// plain down host: nothing was ever sent, so "unreachable" would be
		// pointing at the wrong problem.
		state := s.State
		if m.targets[i].Err != nil {
			state = StateUnresolved
		}
		views[i] = HostView{
			ID:         m.targets[i].ID,
			Display:    m.targets[i].Display,
			State:      state,
			Last:       s.Last,
			Min:        s.Min,
			Max:        s.Max,
			Avg:        s.Avg(),
			Jitter:     s.Jitter(),
			Loss:       s.Loss(),
			HasLatency: s.HasLatency(),
			HasHistory: s.HasHistory(),
			HasJitter:  s.HasJitter(),
			Recent:     s.Recent(),
			ResolveErr: m.targets[i].Err,
		}
	}
	return views
}

// Done reports whether every host has completed at least one probe.
func (m *Monitor) Done() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := range m.stats {
		if m.stats[i].State == StateWaiting {
			return false
		}
	}
	return true
}
