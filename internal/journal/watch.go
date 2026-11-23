package journal

import (
	"context"
	"time"

	"github.com/maxajbarlow/pingm/internal/netstate"
	"github.com/maxajbarlow/pingm/internal/shellhist"
)

// watchInterval is how often the network and shell history are checked.
// Short enough that a change lands near the outage it belongs to, long
// enough that the helper commands cost nothing noticeable.
const watchInterval = 2 * time.Second

// Sources are what Watch reads. Fields are functions so tests can script
// them.
type Sources struct {
	Network  func(context.Context) netstate.Snapshot
	Commands func() []shellhist.Command
}

// Watch logs network changes and new shell commands until ctx is done, then
// checks once more so a command run just before quitting is not lost.
//
// initial is the network as the start event recorded it; only changes from
// it are logged.
func (j *Journal) Watch(ctx context.Context, src Sources, initial netstate.Snapshot) {
	last := initial
	check := func(ctx context.Context) {
		if src.Network != nil {
			now := src.Network(ctx)
			if changes := netstate.Diff(last, now); len(changes) > 0 {
				snap := now
				j.Log(Event{Kind: KindNet, Changes: changes, Net: &snap})
				last = now
			}
		}
		if src.Commands != nil {
			for _, c := range src.Commands() {
				j.Log(Event{T: c.At, Kind: KindCmd, Cmd: c.Text, Source: c.Source})
			}
		}
	}

	t := time.NewTicker(watchInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// The run context is gone; give the last check a short one of
			// its own.
			final, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			check(final)
			cancel()
			return
		case <-t.C:
			check(ctx)
		}
	}
}
