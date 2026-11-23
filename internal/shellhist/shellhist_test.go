package shellhist

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Unix(1_800_000_000, 0) }

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func texts(cmds []Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Text
	}
	return out
}

func TestOnlyCommandsAddedAfterStartAreReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zsh_history")
	appendTo(t, path, "ls\ncd /tmp\n")

	w := NewWatcher([]string{path}, fixedNow)
	if got := w.Poll(); len(got) != 0 {
		t.Fatalf("old history reported: %q", texts(got))
	}

	appendTo(t, path, "sudo ifconfig en0 down\nsudo ifconfig en0 up\n")
	got := w.Poll()
	if !slices.Equal(texts(got), []string{"sudo ifconfig en0 down", "sudo ifconfig en0 up"}) {
		t.Fatalf("Poll = %q", texts(got))
	}
	if got[0].Source != path || !got[0].At.Equal(fixedNow()) {
		t.Fatalf("command = %+v", got[0])
	}
	if again := w.Poll(); len(again) != 0 {
		t.Fatalf("second poll repeated: %q", texts(again))
	}
}

func TestHalfWrittenLineWaitsForTheNextPoll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	appendTo(t, path, "")
	w := NewWatcher([]string{path}, fixedNow)

	appendTo(t, path, "ping 10.")
	if got := w.Poll(); len(got) != 0 {
		t.Fatalf("partial line reported: %q", texts(got))
	}
	appendTo(t, path, "0.0.1\n")
	if got := w.Poll(); !slices.Equal(texts(got), []string{"ping 10.0.0.1"}) {
		t.Fatalf("Poll = %q", texts(got))
	}
}

func TestZshExtendedHistoryUsesItsOwnTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	appendTo(t, path, "")
	w := NewWatcher([]string{path}, fixedNow)

	appendTo(t, path, ": 1700000000:0;too old\n: 1800000060:0;networksetup -setairportpower en0 off\n")
	got := w.Poll()
	if !slices.Equal(texts(got), []string{"networksetup -setairportpower en0 off"}) {
		t.Fatalf("Poll = %q", texts(got))
	}
	if !got[0].At.Equal(time.Unix(1_800_000_060, 0)) {
		t.Fatalf("At = %v", got[0].At)
	}
}

func TestFileThatAppearsLaterIsReadFromTheStart(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher([]string{filepath.Join(dir, "*", "zsh_history")}, fixedNow)

	if err := os.Mkdir(filepath.Join(dir, "term1"), 0o700); err != nil {
		t.Fatal(err)
	}
	appendTo(t, filepath.Join(dir, "term1", "zsh_history"), "dig example.com\n")
	if got := w.Poll(); !slices.Equal(texts(got), []string{"dig example.com"}) {
		t.Fatalf("Poll = %q", texts(got))
	}
}

func TestRewrittenFileResumesAfterTheLastSeenLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h")
	appendTo(t, path, "one\ntwo\nthree\n")
	w := NewWatcher([]string{path}, fixedNow)

	// zsh trims the oldest entries and renames a new file into place.
	tmp := filepath.Join(dir, "h.new")
	appendTo(t, tmp, "two\nthree\nfour\n")
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	if got := w.Poll(); !slices.Equal(texts(got), []string{"four"}) {
		t.Fatalf("Poll = %q", texts(got))
	}
}

func TestParseLineFormats(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"ping 1.1.1.1", "ping 1.1.1.1", true},
		{"ping 1.1.1.1\r", "ping 1.1.1.1", true},
		{"#1700000000", "", false},
		{"- cmd: ip route", "ip route", true},
		{"  when: 1700000000", "", false},
		{"  paths:", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		_, got, ok := parseLine(c.raw)
		if got != c.want || ok != c.ok {
			t.Errorf("parseLine(%q) = %q, %v; want %q, %v", c.raw, got, ok, c.want, c.ok)
		}
	}
}

func TestDefaultPatterns(t *testing.T) {
	env := map[string]string{
		"HISTFILE":      "/h/custom",
		"PINGM_HISTORY": "/a/*/zsh_history" + string(os.PathListSeparator) + "/b/hist",
	}
	got := DefaultPatterns(func(k string) string { return env[k] }, "/home/u")
	want := []string{
		"/h/custom",
		filepath.Join("/home/u", ".zsh_history"),
		filepath.Join("/home/u", ".bash_history"),
		filepath.Join("/home/u", ".local", "share", "fish", "fish_history"),
		"/a/*/zsh_history",
		"/b/hist",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("DefaultPatterns:\n got %q\nwant %q", got, want)
	}
}
