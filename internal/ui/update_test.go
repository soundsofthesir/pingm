package ui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/maxajbarlow/pingm/internal/monitor"
	"github.com/maxajbarlow/pingm/internal/update"
)

// fakeUpdater stands in for GitHub and for the filesystem.
type fakeUpdater struct {
	version      string
	availableErr error

	artifact   *update.Artifact
	prepareErr error

	done     bool
	cmd      *exec.Cmd
	applyErr error

	prepared, applied int
}

func (f *fakeUpdater) Available(context.Context, string) (string, error) {
	return f.version, f.availableErr
}

func (f *fakeUpdater) Prepare(context.Context, string) (*update.Artifact, error) {
	f.prepared++
	return f.artifact, f.prepareErr
}

func (f *fakeUpdater) Apply(*update.Artifact) (bool, *exec.Cmd, error) {
	f.applied++
	return f.done, f.cmd, f.applyErr
}

func withUpdate(version string) (Model, *fakeUpdater) {
	u := &fakeUpdater{version: version, artifact: &update.Artifact{Version: version}}
	m := newTestModel([]monitor.HostView{up("10.0.0.1", ms(5), ms(5), 0)})
	return m.WithUpdater(u, "2.2.0"), u
}

func offer(m Model, version string) Model {
	next, _ := m.Update(updateAvailableMsg{version: version})
	return next.(Model)
}

func TestANewerReleaseIsOfferedQuietly(t *testing.T) {
	m, _ := withUpdate("2.3.0")
	m = offer(m, "2.3.0")

	out := plain(m.View())
	for _, want := range []string{"v2.3.0 available", "U to update"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer is missing %q:\n%s", want, out)
		}
	}
}

func TestNoUpdateNoteWithoutANewerRelease(t *testing.T) {
	m, _ := withUpdate("")
	m = offer(m, "")

	if out := plain(m.View()); strings.Contains(out, "available") {
		t.Errorf("footer offered an update that does not exist:\n%s", out)
	}
}

func TestAFailedCheckIsSilent(t *testing.T) {
	m, _ := withUpdate("")
	next, _ := m.Update(updateAvailableMsg{err: errors.New("no network")})
	m = next.(Model)

	if out := plain(m.View()); strings.Contains(out, "Update") {
		t.Errorf("a failed check left a message in the footer:\n%s", out)
	}
}

func TestUppercaseUDoesNotChangeTheFilter(t *testing.T) {
	m, _ := withUpdate("2.3.0")
	m = offer(m, "2.3.0")

	m = press(m, "U")
	if m.filter != FilterAll {
		t.Errorf("U changed the filter to %v", m.filter)
	}
	if m.updateState != updateConfirming {
		t.Errorf("U did not open the update prompt (state %v)", m.updateState)
	}

	m = press(m, "n")
	m = press(m, "u")
	if m.filter != FilterUp {
		t.Errorf("lowercase u no longer filters up: %v", m.filter)
	}
}

func TestUpdateAsksBeforeDownloading(t *testing.T) {
	m, u := withUpdate("2.3.0")
	m = offer(m, "2.3.0")
	m = press(m, "U")

	out := plain(m.View())
	for _, want := range []string{"Update pingm v2.2.0 → v2.3.0?", "y update", "n cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt is missing %q:\n%s", want, out)
		}
	}
	if u.prepared != 0 || u.applied != 0 {
		t.Errorf("prompt started work: prepared=%d applied=%d", u.prepared, u.applied)
	}

	m = press(m, "n")
	if m.updateState != updateIdle {
		t.Errorf("n left the prompt open (state %v)", m.updateState)
	}
	if u.prepared != 0 {
		t.Error("declining still downloaded the release")
	}
}

func TestAgreeingInstallsTheRelease(t *testing.T) {
	m, u := withUpdate("2.3.0")
	u.done = true
	m = offer(m, "2.3.0")
	m = press(m, "U")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if m.updateState != updateDownloading {
		t.Fatalf("state after y = %v, want downloading", m.updateState)
	}
	if cmd == nil {
		t.Fatal("y returned no download command")
	}

	next, cmd = m.Update(cmd())
	m = next.(Model)
	if m.updateState != updateInstalled {
		t.Fatalf("state after download = %v, want installed", m.updateState)
	}
	if u.prepared != 1 || u.applied != 1 {
		t.Errorf("prepared=%d applied=%d, want one of each", u.prepared, u.applied)
	}
	if cmd != nil {
		t.Error("a writable install returned an elevation command")
	}

	out := plain(m.View())
	for _, want := range []string{"Updated to v2.3.0", "restart pingm"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "available") {
		t.Errorf("footer still offers the installed version:\n%s", out)
	}
}

func TestAnElevatedInstallHandsOverTheTerminal(t *testing.T) {
	m, u := withUpdate("2.3.0")
	u.done = false
	u.cmd = exec.Command("true")
	m = offer(m, "2.3.0")
	m = press(m, "U")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	next, cmd = m.Update(cmd())
	m = next.(Model)

	if m.updateState != updateInstalling {
		t.Fatalf("state = %v, want installing", m.updateState)
	}
	if cmd == nil {
		t.Fatal("elevated install returned no command for the terminal hand-over")
	}
	if m.pending == nil {
		t.Error("artifact was released before the elevated command ran")
	}

	next, _ = m.Update(updateAppliedMsg{})
	m = next.(Model)
	if m.updateState != updateInstalled {
		t.Errorf("state after the elevated install = %v, want installed", m.updateState)
	}
	if m.pending != nil {
		t.Error("artifact was not released after the elevated install")
	}
}

func TestAFailedDownloadStaysVisibleAndCanBeRetried(t *testing.T) {
	m, u := withUpdate("2.3.0")
	u.prepareErr = errors.New("offline")
	m = offer(m, "2.3.0")
	m = press(m, "U")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	if m.updateState != updateFailed {
		t.Fatalf("state = %v, want failed", m.updateState)
	}
	if out := plain(m.View()); !strings.Contains(out, "Update failed — U to retry: offline") {
		t.Errorf("failure is not in the footer:\n%s", out)
	}

	m = press(m, "U")
	if m.updateState != updateConfirming {
		t.Errorf("U did not offer a retry (state %v)", m.updateState)
	}
}

func TestUpdateCheckIsSkippedWithoutAnUpdater(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("h", ms(1), ms(1), 0)})
	if cmd := m.checkForUpdate(); cmd != nil {
		t.Error("a model without an updater still ran a check")
	}
}

func TestUpdateCheckRunsWhenEnabled(t *testing.T) {
	m, _ := withUpdate("2.3.0")
	if cmd := m.checkForUpdate(); cmd == nil {
		t.Error("an updater was set but no check was scheduled")
	}
}

func TestHelpMentionsTheUpdateKey(t *testing.T) {
	m := newTestModel([]monitor.HostView{up("h", ms(1), ms(1), 0)})
	m = press(m, "?")
	if out := plain(m.View()); !strings.Contains(out, "install a newer release") {
		t.Errorf("help does not mention the update key:\n%s", out)
	}
}
