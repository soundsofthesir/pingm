package journal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maxajbarlow/pingm/internal/monitor"
	"github.com/maxajbarlow/pingm/internal/netstate"
	"github.com/maxajbarlow/pingm/internal/shellhist"
)

var t0 = time.Date(2026, 11, 22, 16, 50, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

type buffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) events(t *testing.T) []Event {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	evs, err := Read(strings.NewReader(b.b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func kinds(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

func up(rtt time.Duration) monitor.Result { return monitor.Result{OK: true, RTT: rtt} }

var down = monitor.Result{}

func TestEnabled(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "true": true, " On ": true, "yes": true, "": false, "0": false, "off": false} {
		if got := Enabled(func(string) string { return v }); got != want {
			t.Errorf("Enabled(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestDir(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }

	if got := Dir(get, "darwin", "/Users/u"); got != filepath.Join("/Users/u", "Library", "Logs", "pingm") {
		t.Errorf("darwin Dir = %q", got)
	}
	if got := Dir(get, "linux", "/home/u"); got != filepath.Join("/home/u", ".local", "state", "pingm") {
		t.Errorf("linux Dir = %q", got)
	}
	env["LOCALAPPDATA"] = `C:\Users\u\AppData\Local`
	if got := Dir(get, "windows", `C:\Users\u`); got != filepath.Join(env["LOCALAPPDATA"], "pingm", "logs") {
		t.Errorf("windows Dir = %q", got)
	}
	env[EnvDir] = "/custom"
	if got := Dir(get, "linux", "/home/u"); got != "/custom" {
		t.Errorf("override Dir = %q", got)
	}
}

func TestStateEventsOnlyOnChange(t *testing.T) {
	var b buffer
	j := New(&b, func() time.Time { return at(10) })
	j.Start(Event{Hosts: []HostInfo{{Name: "gw"}}})

	_ = j.Record(up(2*time.Millisecond), at(1), "gw")
	_ = j.Record(up(3*time.Millisecond), at(2), "gw")
	_ = j.Record(down, at(3), "gw")
	_ = j.Record(down, at(4), "gw")
	_ = j.Record(up(2*time.Millisecond), at(5), "gw")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	evs := b.events(t)
	got := strings.Join(kinds(evs), ",")
	if got != "start,state,state,state,stats,end" {
		t.Fatalf("kinds = %s", got)
	}
	if evs[1].From != "" || evs[1].To != "up" || *evs[1].RTT != 2 {
		t.Errorf("first state = %+v", evs[1])
	}
	if evs[2].From != "up" || evs[2].To != "down" || evs[2].RTT != nil {
		t.Errorf("drop = %+v", evs[2])
	}
	stats := evs[4]
	if stats.Sent != 5 || stats.Lost != 2 || stats.MinMs != 2 || stats.MaxMs != 3 {
		t.Errorf("stats = %+v", stats)
	}
	end := evs[5]
	if len(end.Totals) != 1 || end.Totals[0] != (HostTotals{Host: "gw", Sent: 5, Lost: 2}) {
		t.Errorf("totals = %+v", end.Totals)
	}
}

func TestStatsAreWrittenEachMinute(t *testing.T) {
	var b buffer
	j := New(&b, func() time.Time { return at(200) })
	for s := 0; s <= 130; s += 10 {
		_ = j.Record(up(time.Millisecond), at(s), "h")
	}
	_ = j.Close()

	var stats []Event
	for _, e := range b.events(t) {
		if e.Kind == KindStats {
			stats = append(stats, e)
		}
	}
	// 0..60, 70..130, then nothing left for Close.
	if len(stats) != 2 || stats[0].Sent != 7 || stats[1].Sent != 7 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestOpenCreatesPrivateFileAndPrunesOldLogs(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "2026-01-01T000000-1.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := t0.Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	j, err := Open(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	j.Note("restarted router")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old log kept: %v", err)
	}
	latest, err := Latest(dir)
	if err != nil || latest != j.Path() {
		t.Fatalf("Latest = %q, %v; want %q", latest, err, j.Path())
	}
	info, err := os.Stat(latest)
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator == '/' && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestLatestWithNoLogs(t *testing.T) {
	if _, err := Latest(t.TempDir()); err == nil {
		t.Fatal("want an error for an empty folder")
	}
}

func TestReadSkipsTornLine(t *testing.T) {
	evs, err := Read(strings.NewReader(`{"t":"2026-11-22T16:50:00Z","ev":"note","text":"a"}` + "\n" + `{"t":"2026-11-2`))
	if err != nil || len(evs) != 1 || evs[0].Text != "a" {
		t.Fatalf("Read = %+v, %v", evs, err)
	}
}

func TestWatchLogsNetworkChangesAndCommands(t *testing.T) {
	var b buffer
	j := New(&b, time.Now)

	start := netstate.Snapshot{Gateway: "10.0.0.1"}
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	src := Sources{
		Network: func(context.Context) netstate.Snapshot {
			calls++
			return netstate.Snapshot{Gateway: "192.168.1.1"}
		},
		Commands: func() []shellhist.Command {
			cancel()
			return []shellhist.Command{{At: at(5), Text: "sudo route flush", Source: "/h"}}
		},
	}
	// Cancelled from inside the first check, so Watch runs that check on its
	// final pass without waiting for a tick.
	done := make(chan struct{})
	go func() {
		j.Watch(ctx, src, start)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Watch did not return")
	}

	evs := b.events(t)
	if len(evs) < 2 || evs[0].Kind != KindNet || evs[0].Changes[0] != "gateway 10.0.0.1 to 192.168.1.1" {
		t.Fatalf("events = %+v", evs)
	}
	if evs[1].Kind != KindCmd || evs[1].Cmd != "sudo route flush" || !evs[1].T.Equal(at(5)) {
		t.Fatalf("command event = %+v", evs[1])
	}
}
