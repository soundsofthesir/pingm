package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxajbarlow/pingm/internal/journal"
	"github.com/maxajbarlow/pingm/internal/netstate"
)

// withSessionLog turns the session log on in the harness, writing to dir.
func withSessionLog(h *harness, dir string) {
	h.s.logDir = dir
	h.s.openLog = func() (*sessionLog, error) {
		j, err := journal.Open(dir, nil)
		if err != nil {
			return nil, err
		}
		return &sessionLog{
			j:       j,
			sources: journal.Sources{Network: func(context.Context) netstate.Snapshot { return netstate.Snapshot{} }},
		}, nil
	}
}

func readLatest(t *testing.T, dir string) []journal.Event {
	t.Helper()
	path, err := journal.Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := journal.Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestSessionLogRecordsStartProbesAndEnd(t *testing.T) {
	h := newHarness()
	dir := t.TempDir()
	withSessionLog(h, dir)

	if err := run(h.s, []string{"-i", "2s", "10.0.0.1"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	evs := readLatest(t, dir)
	if len(evs) < 3 {
		t.Fatalf("events = %+v", evs)
	}
	start, end := evs[0], evs[len(evs)-1]
	if start.Kind != journal.KindStart || start.Version != version || start.Interval != "2s" ||
		strings.Join(start.Args, " ") != "-i 2s 10.0.0.1" || len(start.Hosts) != 1 {
		t.Errorf("start = %+v", start)
	}
	if evs[1].Kind != journal.KindState || evs[1].To != "up" {
		t.Errorf("first probe event = %+v", evs[1])
	}
	sent := 0
	for _, tot := range end.Totals {
		sent += tot.Sent
	}
	if end.Kind != journal.KindEnd || sent == 0 {
		t.Errorf("end = %+v", end)
	}
}

func TestSessionLogAndCSVBothReceiveProbes(t *testing.T) {
	h := newHarness()
	dir := t.TempDir()
	withSessionLog(h, dir)
	csvPath := filepath.Join(t.TempDir(), "soak.csv")

	if err := run(h.s, []string{"-o", csvPath, "10.0.0.1"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(csvPath)
	if err != nil || strings.Count(string(data), "\n") < 2 {
		t.Fatalf("CSV = %q, %v", data, err)
	}
	if evs := readLatest(t, dir); evs[len(evs)-1].Kind != journal.KindEnd {
		t.Fatalf("session log did not end cleanly: %+v", evs)
	}
}

func TestSessionLogThatCannotOpenOnlyWarns(t *testing.T) {
	h := newHarness()
	h.s.openLog = func() (*sessionLog, error) { return nil, errors.New("read-only disk") }

	if err := run(h.s, []string{"10.0.0.1"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if h.uiStarts != 1 {
		t.Error("the table did not start")
	}
	if !strings.Contains(h.errOut.String(), "read-only disk") {
		t.Errorf("no warning: %q", h.errOut.String())
	}
}

func TestReportPrintsTheNewestSession(t *testing.T) {
	h := newHarness()
	dir := t.TempDir()
	withSessionLog(h, dir)
	if err := run(h.s, []string{"10.0.0.1"}); err != nil {
		t.Fatalf("run: %v", err)
	}

	h.out.Reset()
	if err := run(h.s, []string{"-report"}); err != nil {
		t.Fatalf("-report: %v", err)
	}
	got := h.out.String()
	if !strings.Contains(got, "pingm 10.0.0.1") || !strings.Contains(got, "No outages.") {
		t.Fatalf("report = %q", got)
	}
}

func TestReportWithoutLogsSaysHowToTurnThemOn(t *testing.T) {
	h := newHarness()
	h.s.logDir = filepath.Join(t.TempDir(), "missing")
	err := run(h.s, []string{"-report"})
	if err == nil || !strings.Contains(err.Error(), journal.EnvEnable) {
		t.Fatalf("err = %v", err)
	}
}

func TestReportOfANamedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := `{"t":"` + time.Now().Format(time.RFC3339) + `","ev":"note","text":"hello"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness()
	if err := run(h.s, []string{"-report", path}); err != nil {
		t.Fatalf("-report: %v", err)
	}
	if !strings.Contains(h.out.String(), "Log: "+path) {
		t.Fatalf("report = %q", h.out.String())
	}
}
