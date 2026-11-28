package ui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/maxajbarlow/pingm/internal/hostlist"
)

// DefaultHostLimit caps how many hosts the table will hold when the caller
// sets no limit of its own.
const DefaultHostLimit = 1024

// maxInputLen keeps a stray paste from growing the prompt without bound.
const maxInputLen = 512

// editMode is which host-list interaction, if any, has the keyboard.
type editMode int

const (
	editNone   editMode = iota
	editAdd             // Typing hosts to add.
	editRemove          // Picking a row to remove.
)

// editState is the add or remove interaction in progress.
type editState struct {
	mode     editMode
	input    []rune // What has been typed at the add prompt.
	selected int    // ID of the host picked for removal.
	notice   string // A one-off message for the footer, cleared by the next key.
}

// hostsAddedMsg reports that an addition has been resolved and handed to the
// monitor.
type hostsAddedMsg struct {
	added int
}

// WithHostLimit caps the total number of hosts the add prompt will accept.
func (m Model) WithHostLimit(n int) Model {
	if n > 0 {
		m.hostLimit = n
	}
	return m
}

// startAdding opens the add prompt. Once a -c run has finished nothing more
// will be probed, so there is nothing to add to.
func (m Model) startAdding() (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	m.edit = editState{mode: editAdd}
	return m, nil
}

// startRemoving picks the top visible row, so the highlight starts where the
// user is already looking.
func (m *Model) startRemoving() {
	matched := m.matching()
	if len(matched) == 0 {
		return
	}
	pos := m.offset
	if pos >= len(matched) {
		pos = len(matched) - 1
	}
	m.edit = editState{mode: editRemove, selected: m.views[matched[pos]].ID}
}

func (m Model) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	if m.edit.mode == editAdd {
		return m.handleAddKey(msg)
	}
	m.handleRemoveKey(msg)
	return m, nil
}

func (m Model) handleAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.edit = editState{}
		return m, nil
	case tea.KeyEnter:
		return m.submitAdd()
	case tea.KeyBackspace:
		if n := len(m.edit.input); n > 0 {
			m.edit.input = m.edit.input[:n-1]
		}
	case tea.KeyCtrlU:
		m.edit.input = nil
	case tea.KeySpace:
		m.edit.input = appendInput(m.edit.input, []rune{' '})
	case tea.KeyRunes:
		m.edit.input = appendInput(m.edit.input, msg.Runes)
	default:
		return m, nil
	}
	m.edit.notice = ""
	return m, nil
}

// appendInput adds printable runes to the prompt, dropping control characters
// a paste can carry, up to maxInputLen.
func appendInput(input, runes []rune) []rune {
	out := append([]rune(nil), input...)
	for _, r := range runes {
		if len(out) >= maxInputLen {
			break
		}
		if unicode.IsPrint(r) {
			out = append(out, r)
		}
	}
	return out
}

// submitAdd parses what was typed and hands the hosts to the monitor in the
// background, because resolving names can take a while. A mistake keeps the
// prompt open with the text intact so it can be corrected.
func (m Model) submitAdd() (tea.Model, tea.Cmd) {
	spec := strings.TrimSpace(string(m.edit.input))
	if spec == "" {
		m.edit = editState{}
		return m, nil
	}

	room := m.hostLimit - len(m.views)
	if room <= 0 {
		m.edit.notice = fmt.Sprintf("Table is full (%d hosts)", m.hostLimit)
		return m, nil
	}
	hosts, err := hostlist.Parse(spec, m.hostLimit)
	if err != nil {
		m.edit.notice = capitalise(err.Error())
		return m, nil
	}
	if len(hosts) > room {
		m.edit.notice = fmt.Sprintf("Only %d more hosts fit", room)
		return m, nil
	}

	m.edit = editState{}
	mon := m.mon
	return m, func() tea.Msg {
		return hostsAddedMsg{added: mon.Add(hosts)}
	}
}

func (m Model) hostsAdded(msg hostsAddedMsg) Model {
	if msg.added == 0 {
		m.edit.notice = "Already in the table"
	}
	m.refreshViews()
	return m
}

func (m *Model) handleRemoveKey(msg tea.KeyMsg) {
	matched := m.matching()
	pos := m.selectedPos(matched)

	switch msg.String() {
	case "esc", "q":
		m.edit = editState{}
		return
	case "up", "k":
		pos--
	case "down", "j":
		pos++
	case "home", "g":
		pos = 0
	case "end", "G":
		pos = len(matched) - 1
	case "enter", "x", "delete", "backspace":
		if pos >= 0 {
			m.removeAt(matched, pos)
		}
		return
	default:
		return
	}
	m.selectAt(matched, pos)
}

// removeAt drops the host at pos and moves the highlight to the row that
// takes its place, so several hosts can be removed in a row.
func (m *Model) removeAt(matched []int, pos int) {
	m.mon.Remove(m.views[matched[pos]].ID)
	m.refreshViews()

	rest := m.matching()
	if len(rest) == 0 {
		m.edit = editState{}
		return
	}
	m.selectAt(rest, pos)
}

// selectAt highlights the row at pos, clamped to the list, and scrolls it
// into view.
func (m *Model) selectAt(matched []int, pos int) {
	if len(matched) == 0 {
		return
	}
	if pos < 0 {
		pos = 0
	}
	if pos >= len(matched) {
		pos = len(matched) - 1
	}
	m.edit.selected = m.views[matched[pos]].ID
	m.ensureVisible(pos, len(matched))
}

// selectedPos is where the highlighted host sits among the matching rows. A
// host that has left the view since (a filter hides hosts as their state
// changes) falls back to the top visible row.
func (m Model) selectedPos(matched []int) int {
	for pos, i := range matched {
		if m.views[i].ID == m.edit.selected {
			return pos
		}
	}
	if len(matched) == 0 {
		return -1
	}
	if m.offset < len(matched) {
		return m.offset
	}
	return len(matched) - 1
}

// ensureVisible scrolls just far enough to bring row pos on screen.
func (m *Model) ensureVisible(pos, total int) {
	visible := m.rowBudget()
	if total > visible {
		visible-- // One row goes to the scroll indicator.
	}
	if visible < 1 {
		visible = 1
	}
	if pos < m.offset {
		m.offset = pos
	}
	if pos >= m.offset+visible {
		m.offset = pos - visible + 1
	}
	m.clampOffset()
}

func (m Model) isSelected(i int) bool {
	return m.edit.mode == editRemove && m.views[i].ID == m.edit.selected
}

// bottomLine is the key hints, or the prompt while an edit has the keyboard.
func (m Model) bottomLine() string {
	sep := styleFooter.Render("  ")
	switch m.edit.mode {
	case editAdd:
		// The keys sit on a line of their own below the prompt, so the
		// prompt has the full width for typing.
		room := m.width - len("Add ") - 1
		input := string(m.edit.input)
		if room > 0 && len([]rune(input)) > room {
			// Keep the end in view, where the typing is.
			r := []rune(input)
			input = string(r[len(r)-room:])
		}
		return styleKeyName.Render("Add ") + styleInput.Render(input) +
			styleSelected.Render("▏") + "\n" +
			key("enter", "add") + sep + key("esc", "cancel")
	case editRemove:
		return key("↑↓", "select") + sep + key("enter", "remove") + sep + key("esc", "done")
	default:
		return m.keyHints()
	}
}

// capitalise starts a library error with a capital, to match the footer.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
