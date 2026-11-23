package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/maxajbarlow/pingm/internal/monitor"
)

type fakeJournal struct {
	notes   []string
	actions []string
}

func (f *fakeJournal) Note(text string)   { f.notes = append(f.notes, text) }
func (f *fakeJournal) Action(text string) { f.actions = append(f.actions, text) }

func journalModel() (Model, *fakeJournal) {
	j := &fakeJournal{}
	m := newTestModel([]monitor.HostView{up("gw", ms(2), ms(2), 0)}).WithJournal(j)
	return m, j
}

func typeText(m Model, text string) Model {
	for _, r := range text {
		if r == ' ' {
			m = pressSpecial(m, tea.KeySpace)
			continue
		}
		m = press(m, string(r))
	}
	return m
}

func TestNoteIsTypedAndSavedOnEnter(t *testing.T) {
	m, j := journalModel()
	m = press(m, "n")
	// Keys that normally act on the table are text while a note is open.
	m = typeText(m, "restarted q router")
	m = pressSpecial(m, tea.KeyBackspace)
	if footer := m.View(); !strings.Contains(footer, "Note: restarted q route_") {
		t.Fatalf("footer does not show the note being typed:\n%s", footer)
	}
	m = pressSpecial(m, tea.KeyEnter)

	if len(j.notes) != 1 || j.notes[0] != "restarted q route" {
		t.Fatalf("notes = %q", j.notes)
	}
	if m.noting || m.quitting {
		t.Fatalf("model still noting=%v or quitting=%v", m.noting, m.quitting)
	}
}

func TestEscDropsTheNote(t *testing.T) {
	m, j := journalModel()
	m = press(m, "n")
	m = typeText(m, "oops")
	m = pressSpecial(m, tea.KeyEsc)
	if len(j.notes) != 0 || m.noting || m.quitting {
		t.Fatalf("notes = %q, noting = %v, quitting = %v", j.notes, m.noting, m.quitting)
	}
}

func TestBlankNoteIsNotSaved(t *testing.T) {
	m, j := journalModel()
	m = press(m, "n")
	m = typeText(m, "   ")
	_ = pressSpecial(m, tea.KeyEnter)
	if len(j.notes) != 0 {
		t.Fatalf("notes = %q", j.notes)
	}
}

func TestNoteKeyNeedsASessionLog(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("gw", ms(2), ms(2), 0)})
	m = press(m, "n")
	if m.noting {
		t.Fatal("n opened a note with no session log")
	}
	if strings.Contains(m.keyHints(), "note") {
		t.Fatal("key hints offer notes with no session log")
	}
}

func TestKeyPressesAreLogged(t *testing.T) {
	m, j := journalModel()
	m = press(m, "d")
	m = press(m, "+")
	m = press(m, "p")
	m = press(m, "p")
	m = press(m, "q")
	want := "show down,interval 2s,pause,resume,quit"
	if got := strings.Join(j.actions, ","); got != want {
		t.Fatalf("actions = %s, want %s", got, want)
	}
}
