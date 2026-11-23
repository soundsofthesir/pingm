package journal

import (
	"strings"
	"testing"
	"time"
)

func state(sec int, host, from, to string) Event {
	return Event{T: at(sec), Kind: KindState, Host: host, From: from, To: to}
}

func TestSummariseMergesOverlappingOutagesAndKeepsActivity(t *testing.T) {
	events := []Event{
		{T: at(0), Kind: KindStart, Args: []string{"gw,dns"}, Hosts: []HostInfo{{Name: "gw"}, {Name: "dns"}, {Name: "typo.invalid", Error: "no such host"}}},
		state(1, "gw", "", "up"),
		state(1, "dns", "", "up"),
		state(1, "dead", "", "down"),
		state(10, "gw", "up", "down"),
		{T: at(12), Kind: KindCmd, Cmd: "sudo ifconfig en0 down"},
		state(15, "dns", "up", "down"),
		{T: at(20), Kind: KindNet, Changes: []string{"Wi-Fi Office to Home"}},
		{T: at(25), Kind: KindNote, Text: "restarted router"},
		state(30, "gw", "down", "up"),
		state(40, "dns", "down", "up"),
		{T: at(45), Kind: KindCmd, Cmd: "ls"},
		{T: at(60), Kind: KindEnd},
	}

	s := Summarise(events)
	if len(s.Incidents) != 1 {
		t.Fatalf("incidents = %+v", s.Incidents)
	}
	inc := s.Incidents[0]
	if !inc.Start.Equal(at(10)) || !inc.End.Equal(at(40)) || !inc.Fixed {
		t.Errorf("incident = %+v", inc)
	}
	if strings.Join(inc.Hosts, ",") != "gw,dns" {
		t.Errorf("hosts = %v", inc.Hosts)
	}
	if got := strings.Join(kinds(inc.Events), ","); got != "cmd,net,note" {
		t.Errorf("activity = %s", got)
	}
	if strings.Join(s.NeverUp, ",") != "dead" {
		t.Errorf("never up = %v", s.NeverUp)
	}
	if len(s.Unresolved) != 1 || s.Unresolved[0].Name != "typo.invalid" {
		t.Errorf("unresolved = %v", s.Unresolved)
	}
}

func TestHostDownFromTheStartThatComesBackIsAnIncident(t *testing.T) {
	events := []Event{
		{T: at(0), Kind: KindStart},
		state(1, "gw", "", "down"),
		{T: at(5), Kind: KindCmd, Cmd: "sudo dhclient -r"},
		state(9, "gw", "down", "up"),
		{T: at(20), Kind: KindEnd},
	}
	s := Summarise(events)
	if len(s.Incidents) != 1 || !s.Incidents[0].Fixed || len(s.Incidents[0].Events) != 1 {
		t.Fatalf("incidents = %+v", s.Incidents)
	}
	if len(s.NeverUp) != 0 {
		t.Fatalf("never up = %v", s.NeverUp)
	}
}

func TestOutageStillOpenAtTheEndIsNotFixed(t *testing.T) {
	events := []Event{
		state(1, "gw", "", "up"),
		state(5, "gw", "up", "down"),
		{T: at(30), Kind: KindEnd},
	}
	s := Summarise(events)
	if len(s.Incidents) != 1 || s.Incidents[0].Fixed || !s.Incidents[0].End.Equal(at(30)) {
		t.Fatalf("incidents = %+v", s.Incidents)
	}
}

func TestSeparateOutagesStaySeparate(t *testing.T) {
	events := []Event{
		state(1, "a", "", "up"),
		state(5, "a", "up", "down"),
		state(8, "a", "down", "up"),
		state(20, "a", "up", "down"),
		state(25, "a", "down", "up"),
	}
	if n := len(Summarise(events).Incidents); n != 2 {
		t.Fatalf("incidents = %d, want 2", n)
	}
}

func TestReportText(t *testing.T) {
	events := []Event{
		{T: at(0), Kind: KindStart, Args: []string{"-i", "1s", "gw"}},
		state(1, "gw", "", "up"),
		state(10, "gw", "up", "down"),
		{T: at(20), Kind: KindNet, Changes: []string{"en0 down (was 10.0.0.5/24)", "gateway 10.0.0.1 via en0 to none"}},
		{T: at(25), Kind: KindCmd, Cmd: "sudo ifconfig en0 up"},
		{T: at(28), Kind: KindNote, Text: "replugged cable"},
		state(162, "gw", "down", "up"),
		{T: at(724), Kind: KindEnd},
	}
	var b strings.Builder
	if err := Report(events, "/logs/x.jsonl", &b, time.UTC); err != nil {
		t.Fatal(err)
	}
	want := `Session 2026-11-22 16:50:00 to 17:02:04 (12m 4s)
pingm -i 1s gw
Log: /logs/x.jsonl

Incident 1: 16:50:10 to 16:52:42 (2m 32s), fixed
  Down: gw
  16:50:20  network  en0 down (was 10.0.0.5/24); gateway 10.0.0.1 via en0 to none
  16:50:25  command  sudo ifconfig en0 up
  16:50:28  note     replugged cable
`
	if b.String() != want {
		t.Fatalf("Report:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestReportWithNoOutages(t *testing.T) {
	var b strings.Builder
	_ = Report([]Event{state(1, "gw", "", "up")}, "x", &b, time.UTC)
	if !strings.Contains(b.String(), "No outages.") {
		t.Fatalf("Report = %q", b.String())
	}
}

func TestListedCapsLongLists(t *testing.T) {
	names := make([]string, 13)
	for i := range names {
		names[i] = "h"
	}
	if got := listed(names); !strings.HasSuffix(got, "and 3 more") {
		t.Fatalf("listed = %q", got)
	}
}
