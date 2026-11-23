//go:build windows

package netstate

import (
	"slices"
	"testing"
)

func TestParseRoutePrint(t *testing.T) {
	text := `IPv4 Route Table
===========================================================================
Active Routes:
Network Destination        Netmask          Gateway       Interface  Metric
          0.0.0.0          0.0.0.0      192.168.1.1    192.168.1.20     35
===========================================================================
`
	if got := parseRoutePrint(text); got != "192.168.1.1" {
		t.Fatalf("parseRoutePrint = %q", got)
	}
}

func TestParseNetshDNS(t *testing.T) {
	text := `
Configuration for interface "Wi-Fi"
    DNS servers configured through DHCP:  192.168.1.1
                                          1.1.1.1
    Register with which suffix:           Primary only
`
	if got := parseNetshDNS(text); !slices.Equal(got, []string{"192.168.1.1", "1.1.1.1"}) {
		t.Fatalf("parseNetshDNS = %q", got)
	}
}

func TestParseNetshWlanIgnoresBSSID(t *testing.T) {
	text := "    BSSID                  : aa:bb:cc:dd:ee:ff\n    SSID                   : Office\n"
	if got := parseNetshWlan(text); got != "Office" {
		t.Fatalf("parseNetshWlan = %q", got)
	}
}
