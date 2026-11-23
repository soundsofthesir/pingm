// Package shellhist picks up the commands a person runs in other terminals
// while pingm is watching, by reading what their shells append to history
// files.
//
// Only commands added after the watcher starts are reported. Shells that save
// history only on exit (zsh and bash by default) show nothing until that
// terminal closes; zsh's INC_APPEND_HISTORY option makes it save each command
// as it runs.
package shellhist

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxRead caps how much of one file a single poll reads, so a history file
// rewritten in one go cannot stall the poll.
const maxRead = 1 << 20

// Command is one command line seen in a history file.
type Command struct {
	At     time.Time // From the history entry when it has one, otherwise when it was seen.
	Text   string    // Secrets already masked.
	Source string    // The history file it came from.
}

type file struct {
	offset int64
	info   os.FileInfo
	last   string // Last raw line read, to find our place again after a rewrite.
}

// Watcher follows a set of history files given as glob patterns.
type Watcher struct {
	patterns []string
	files    map[string]*file
	started  time.Time
	now      func() time.Time
}

// NewWatcher starts following the files matching patterns. Everything already
// in them is treated as old.
func NewWatcher(patterns []string, now func() time.Time) *Watcher {
	if now == nil {
		now = time.Now
	}
	w := &Watcher{patterns: patterns, files: map[string]*file{}, started: now(), now: now}
	for _, path := range w.matches() {
		if info, err := os.Stat(path); err == nil {
			w.files[path] = &file{offset: info.Size(), info: info, last: lastLine(path, info.Size())}
		}
	}
	return w
}

// Poll returns the commands added since the last poll, oldest first within
// each file.
func (w *Watcher) Poll() []Command {
	var out []Command
	for _, path := range w.matches() {
		out = append(out, w.pollFile(path)...)
	}
	return out
}

func (w *Watcher) matches() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range w.patterns {
		paths, err := filepath.Glob(p)
		if err != nil {
			continue
		}
		for _, path := range paths {
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	return out
}

func (w *Watcher) pollFile(path string) []Command {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil
	}

	f, known := w.files[path]
	if !known {
		// A file that appeared after the start belongs to a terminal opened
		// since, so all of it is new.
		f = &file{info: info}
		w.files[path] = f
	}

	switch {
	case known && !os.SameFile(f.info, info):
		// Replaced wholesale, as zsh does when it trims history on exit.
		// Pick up after the last line already seen, or nothing if it is gone.
		f.offset = offsetAfter(path, f.last, info.Size())
	case info.Size() < f.offset:
		f.offset = info.Size()
	}
	f.info = info
	if info.Size() == f.offset {
		return nil
	}

	data, err := readFrom(path, f.offset, info.Size())
	if err != nil {
		return nil
	}
	// Leave a half-written last line for the next poll.
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil
	}
	data = data[:end+1]
	f.offset += int64(len(data))

	var out []Command
	for _, raw := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		f.last = raw
		at, text, ok := parseLine(raw)
		if !ok {
			continue
		}
		if at.IsZero() {
			at = w.now()
		} else if at.Before(w.started) {
			// A timestamped entry from before the start, written late by a
			// shell that saves on exit.
			continue
		}
		out = append(out, Command{At: at, Text: Redact(text), Source: path})
	}
	return out
}

func readFrom(path string, from, to int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if to-from > maxRead {
		from = to - maxRead
	}
	buf := make([]byte, to-from)
	n, err := f.ReadAt(buf, from)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// lastLine returns the final complete line of the first size bytes of path.
func lastLine(path string, size int64) string {
	if size == 0 {
		return ""
	}
	data, err := readFrom(path, max(0, size-4096), size)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	return lines[len(lines)-1]
}

// offsetAfter finds where last ends in path, or returns size when it cannot,
// so a file it does not recognise is treated as all old.
func offsetAfter(path, last string, size int64) int64 {
	if last == "" {
		return size
	}
	data, err := readFrom(path, 0, size)
	if err != nil {
		return size
	}
	i := bytes.LastIndex(data, []byte(last+"\n"))
	if i < 0 {
		return size
	}
	// readFrom keeps only the tail of a very large file.
	return size - int64(len(data)) + int64(i+len(last)+1)
}

// zshExtended matches zsh's EXTENDED_HISTORY lines: ": 1700000000:0;command".
var zshExtended = regexp.MustCompile(`^: (\d+):\d+;(.*)$`)

// parseLine reads one history line in zsh, bash, fish or PowerShell form.
// Bash's "#1700000000" timestamp lines and fish's metadata lines carry no
// command and are skipped.
func parseLine(raw string) (time.Time, string, bool) {
	line := strings.TrimRight(raw, "\r")
	if m := zshExtended.FindStringSubmatch(line); m != nil {
		sec, _ := strconv.ParseInt(m[1], 10, 64)
		return time.Unix(sec, 0), m[2], strings.TrimSpace(m[2]) != ""
	}
	if cmd, ok := strings.CutPrefix(line, "- cmd: "); ok {
		return time.Time{}, cmd, true
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "when: ") || strings.HasPrefix(trimmed, "paths:") ||
		strings.HasPrefix(trimmed, "- /") || isBashStamp(trimmed) {
		return time.Time{}, "", false
	}
	return time.Time{}, line, true
}

func isBashStamp(s string) bool {
	if len(s) < 2 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseInt(s[1:], 10, 64)
	return err == nil
}

// DefaultPatterns lists the history files of the common shells, plus any
// extra globs in PINGM_HISTORY, separated like PATH.
func DefaultPatterns(getenv func(string) string, home string) []string {
	var out []string
	if h := getenv("HISTFILE"); h != "" {
		out = append(out, h)
	}
	if home != "" {
		out = append(out,
			filepath.Join(home, ".zsh_history"),
			filepath.Join(home, ".bash_history"),
			filepath.Join(home, ".local", "share", "fish", "fish_history"),
		)
	}
	if appdata := getenv("APPDATA"); appdata != "" {
		out = append(out, filepath.Join(appdata, "Microsoft", "Windows", "PowerShell", "PSReadLine", "*_history.txt"))
	}
	for _, p := range filepath.SplitList(getenv("PINGM_HISTORY")) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
