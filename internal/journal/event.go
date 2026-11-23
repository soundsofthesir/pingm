// Package journal keeps a session log: one JSON line per event, covering what
// pingm saw (hosts going up and down, per-minute latency and loss) and what
// the person did while it ran (network changes, shell commands, notes, keys).
//
// The point is to read it afterwards and see what fixed a connection, so it
// logs changes and summaries rather than every probe.
package journal

import (
	"time"

	"github.com/maxajbarlow/pingm/internal/netstate"
)

// Event kinds.
const (
	KindStart  = "start"  // Session began: version, arguments, hosts, network.
	KindState  = "state"  // A host changed state.
	KindStats  = "stats"  // One host's probes over the last minute.
	KindNet    = "net"    // The network setup changed.
	KindCmd    = "cmd"    // A shell command was run in another terminal.
	KindNote   = "note"   // The person typed a note.
	KindAction = "action" // The person pressed a key that changes probing.
	KindEnd    = "end"    // Session ended, with per-host totals.
)

// Event is one line of the log. Fields that do not apply to a kind are left
// out of the JSON.
type Event struct {
	T    time.Time `json:"t"`
	Kind string    `json:"ev"`

	Host string   `json:"host,omitempty"`
	From string   `json:"from,omitempty"`
	To   string   `json:"to,omitempty"`
	RTT  *float64 `json:"rtt_ms,omitempty"`

	Sent  int     `json:"sent,omitempty"`
	Lost  int     `json:"lost,omitempty"`
	MinMs float64 `json:"min_ms,omitempty"`
	AvgMs float64 `json:"avg_ms,omitempty"`
	MaxMs float64 `json:"max_ms,omitempty"`

	Changes []string           `json:"changes,omitempty"`
	Net     *netstate.Snapshot `json:"net,omitempty"`

	Cmd    string `json:"cmd,omitempty"`
	Source string `json:"source,omitempty"`
	Text   string `json:"text,omitempty"`

	Version  string       `json:"version,omitempty"`
	OS       string       `json:"os,omitempty"`
	Args     []string     `json:"args,omitempty"`
	Hosts    []HostInfo   `json:"hosts,omitempty"`
	Interval string       `json:"interval,omitempty"`
	Totals   []HostTotals `json:"totals,omitempty"`
}

// HostInfo is one target as the session started.
type HostInfo struct {
	Name  string `json:"name"`
	Error string `json:"error,omitempty"` // Why the name did not resolve.
}

// HostTotals is one host's whole-session count.
type HostTotals struct {
	Host string `json:"host"`
	Sent int    `json:"sent"`
	Lost int    `json:"lost"`
}
