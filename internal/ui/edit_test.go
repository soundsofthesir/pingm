package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/maxajbarlow/pingm/internal/monitor"
)

func typeKeys(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, c := m.Update(msg)
		m, cmd = next.(Model), c
	}
	return m, cmd
}

func source(m Model) *fakeSource { return m.mon.(*fakeSource) }

func TestAddPromptHandsParsedHostsToTheMonitor(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("a", ms(1), ms(1), 0)})

	m, _ = typeKeys(m, "n", "10.0.0.1-10.0.0.3", ",", "x", "backspace", "backspace")
	if !strings.Contains(m.View(), "Add 10.0.0.1-10.0.0.3") {
		t.Fatalf("prompt does not show what was typed:\n%s", m.View())
	}
	m, cmd := typeKeys(m, "enter")
	if m.edit.mode != editNone {
		t.Error("prompt stayed open after a valid entry")
	}
	if cmd == nil {
		t.Fatal("no command to add the hosts")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)

	got := source(m).added
	if len(got) != 1 || strings.Join(got[0], ",") != "10.0.0.1,10.0.0.2,10.0.0.3" {
		t.Errorf("monitor was asked to add %v", got)
	}
}

func TestAddPromptKeepsABadEntryForCorrection(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("a", ms(1), ms(1), 0)})

	m, cmd := typeKeys(m, "n", "10.0.0.9-10.0.0.1", "enter")
	if cmd != nil || m.edit.mode != editAdd {
		t.Fatal("a bad range was submitted or closed the prompt")
	}
	if m.edit.notice == "" || !strings.Contains(m.View(), m.edit.notice) {
		t.Errorf("no error shown for a bad range:\n%s", m.View())
	}
	if string(m.edit.input) != "10.0.0.9-10.0.0.1" {
		t.Errorf("input = %q, want it kept", string(m.edit.input))
	}
}

func TestAddPromptRespectsTheHostLimit(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("a", ms(1), ms(1), 0)}).WithHostLimit(3)

	m, cmd := typeKeys(m, "n", "10.0.0.1-10.0.0.3", "enter")
	if cmd != nil {
		t.Fatal("hosts past the limit were submitted")
	}
	if m.edit.notice != "Only 2 more hosts fit" {
		t.Errorf("notice = %q", m.edit.notice)
	}
}

func TestEscCancelsTheAddPrompt(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("a", ms(1), ms(1), 0)})

	m, cmd := typeKeys(m, "n", "google.com", "esc")
	if m.edit.mode != editNone || cmd != nil || len(source(m).added) != 0 {
		t.Error("Esc did not cancel the add")
	}
	// Keys go back to the table: q quits again.
	if _, cmd := typeKeys(m, "q"); cmd == nil {
		t.Error("q did not quit after the prompt closed")
	}
}

// Typing at the prompt must not trigger the table's own keys.
func TestPromptSwallowsTableKeys(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("a", ms(1), ms(1), 0)})

	m, cmd := typeKeys(m, "n", "q", "d", "p")
	if cmd != nil || m.filter != FilterAll || m.paused {
		t.Error("a table key acted while typing")
	}
	if string(m.edit.input) != "qdp" {
		t.Errorf("input = %q, want qdp", string(m.edit.input))
	}
}

func TestRemoveModeRemovesTheHighlightedHost(t *testing.T) {
	three := []monitor.HostView{
		up("a", ms(1), ms(1), 0), up("b", ms(1), ms(1), 0), up("c", ms(1), ms(1), 0),
	}
	// After the removal the monitor reports a and c.
	m := newTestModel(three, three, []monitor.HostView{three[0], {ID: 2, Display: "c", State: monitor.StateUp}})
	m = advance(m)

	m, _ = typeKeys(m, "x", "down")
	if !strings.Contains(m.View(), glyphSelected+" b") {
		t.Fatalf("b is not highlighted:\n%s", m.View())
	}
	m, _ = typeKeys(m, "enter")

	if got := source(m).removed; len(got) != 1 || got[0] != 1 {
		t.Fatalf("removed %v, want [1] (b)", got)
	}
	if m.edit.mode != editRemove || m.edit.selected != 2 {
		t.Errorf("highlight did not move on to c: %+v", m.edit)
	}
	m, _ = typeKeys(m, "esc")
	if m.edit.mode != editNone || strings.Contains(m.View(), glyphSelected) {
		t.Error("Esc did not leave remove mode")
	}
}

func TestRemoveModeStopsAtTheEnds(t *testing.T) {
	two := []monitor.HostView{up("a", ms(1), ms(1), 0), up("b", ms(1), ms(1), 0)}
	m := newTestModel(two)

	m, _ = typeKeys(m, "x", "up", "up")
	if m.edit.selected != 0 {
		t.Errorf("selected %d after moving above the top, want 0", m.edit.selected)
	}
	m, _ = typeKeys(m, "down", "down", "down")
	if m.edit.selected != 1 {
		t.Errorf("selected %d after moving past the bottom, want 1", m.edit.selected)
	}
}

func TestRemovingTheLastHostLeavesAnEmptyTable(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("a", ms(1), ms(1), 0)}, []monitor.HostView{})
	m, _ = typeKeys(m, "x", "enter")

	if m.edit.mode != editNone {
		t.Error("remove mode stayed on with nothing left to remove")
	}
	if !strings.Contains(m.View(), "No hosts. n to add") {
		t.Errorf("empty table does not say how to add:\n%s", m.View())
	}
}

// A host's trend history and flash must follow its ID, not its row, or
// removing a row would hand its history to the host below.
func TestHistoryFollowsTheHostAcrossARemoval(t *testing.T) {
	a := up("a", ms(1), ms(10), 0)
	b := up("b", ms(1), ms(50), 0)
	bFaster := up("b", ms(1), ms(40), 0)
	bFaster.ID = 1
	m := newTestModel([]monitor.HostView{a, b}, []monitor.HostView{bFaster})
	m = advance(m)

	if got := m.avgTrend(0); got != TrendBetter {
		t.Errorf("b's average trend = %v after a moved out, want better (50 to 40ms)", got)
	}
}

// The prompt's keys go on their own line, and the table gives up a row for
// it so the screen does not overflow.
func TestAddPromptKeysSitBelowThePrompt(t *testing.T) {
	rows := make([]monitor.HostView, 40)
	for i := range rows {
		rows[i] = up("h", ms(1), ms(1), 0)
	}
	m := newTestModel(rows)
	m, _ = typeKeys(m, "n", "10.0.0.1")

	lines := strings.Split(m.View(), "\n")
	if len(lines) > m.height {
		t.Errorf("view is %d lines in a %d-line terminal", len(lines), m.height)
	}
	prompt, keys := lines[len(lines)-2], lines[len(lines)-1]
	if !strings.Contains(prompt, "Add 10.0.0.1") || strings.Contains(prompt, "esc") {
		t.Errorf("prompt line = %q", prompt)
	}
	if !strings.Contains(keys, "enter") || !strings.Contains(keys, "esc") {
		t.Errorf("keys line = %q", keys)
	}
}
