package netstate

import (
	"context"
	"slices"
	"testing"
)

func TestDiffNamesEachChange(t *testing.T) {
	a := Snapshot{
		Interfaces: []Interface{
			{Name: "en0", Addrs: []string{"10.0.0.5/24"}},
			{Name: "utun3", Addrs: []string{"100.64.0.2/32"}},
		},
		Gateway: "10.0.0.1", GatewayIface: "en0",
		DNS:  []string{"1.1.1.1"},
		WiFi: "Office",
	}
	b := Snapshot{
		Interfaces: []Interface{
			{Name: "en0", Addrs: []string{"192.168.1.20/24"}},
			{Name: "en7", Addrs: []string{"10.1.1.4/24"}},
		},
		Gateway: "192.168.1.1", GatewayIface: "en0",
		DNS: []string{"8.8.8.8", "8.8.4.4"},
	}

	got := Diff(a, b)
	want := []string{
		"utun3 down (was 100.64.0.2/32)",
		"en0 address 10.0.0.5/24 to 192.168.1.20/24",
		"en7 up with 10.1.1.4/24",
		"gateway 10.0.0.1 via en0 to 192.168.1.1 via en0",
		"DNS 1.1.1.1 to 8.8.8.8, 8.8.4.4",
		"Wi-Fi Office to none",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Diff:\n got %q\nwant %q", got, want)
	}
}

func TestDiffOfIdenticalSnapshotsIsEmpty(t *testing.T) {
	s := Snapshot{
		Interfaces: []Interface{{Name: "en0", Addrs: []string{"10.0.0.5/24"}}},
		Gateway:    "10.0.0.1",
		DNS:        []string{"1.1.1.1"},
	}
	if got := Diff(s, s); len(got) != 0 {
		t.Fatalf("Diff of equal snapshots = %q, want nothing", got)
	}
}

func TestDiffReportsLostGateway(t *testing.T) {
	got := Diff(Snapshot{Gateway: "10.0.0.1"}, Snapshot{})
	if !slices.Equal(got, []string{"gateway 10.0.0.1 to none"}) {
		t.Fatalf("Diff = %q", got)
	}
}

func TestUniqueIPsDropsJunkAndRepeats(t *testing.T) {
	got := uniqueIPs([]string{" 1.1.1.1", "Primary only", "1.1.1.1", "8.8.8.8 "})
	if !slices.Equal(got, []string{"1.1.1.1", "8.8.8.8"}) {
		t.Fatalf("uniqueIPs = %q", got)
	}
}

// Collect runs the real commands. It only has to come back without hanging
// or panicking; what it finds depends on the machine.
func TestCollectReturns(t *testing.T) {
	_ = Collect(context.Background())
}
