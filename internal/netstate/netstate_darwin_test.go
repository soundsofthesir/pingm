//go:build darwin

package netstate

import (
	"slices"
	"testing"
)

func TestParseRouteGet(t *testing.T) {
	text := `   route to: default
destination: default
       mask: default
    gateway: 172.16.132.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
`
	gw, ifc := parseRouteGet(text)
	if gw != "172.16.132.1" || ifc != "en0" {
		t.Fatalf("parseRouteGet = %q, %q", gw, ifc)
	}
	if gw, ifc := parseRouteGet(""); gw != "" || ifc != "" {
		t.Fatalf("no route should give no gateway, got %q, %q", gw, ifc)
	}
}

func TestParseScutilDNS(t *testing.T) {
	text := `DNS configuration

resolver #1
  nameserver[0] : 1.1.1.1
  nameserver[1] : 8.8.8.8
  if_index : 11 (en0)

resolver #2
  nameserver[0] : 1.1.1.1
`
	got := parseScutilDNS(text)
	if !slices.Equal(got, []string{"1.1.1.1", "8.8.8.8"}) {
		t.Fatalf("parseScutilDNS = %q", got)
	}
}

func TestParseSSIDTreatsRedactedAsUnknown(t *testing.T) {
	if got := parseSSID("  SSID : <redacted>\n"); got != "" {
		t.Fatalf("redacted SSID = %q, want empty", got)
	}
	if got := parseSSID("  BSSID : aa:bb\n  SSID : Office\n"); got != "Office" {
		t.Fatalf("SSID = %q, want Office", got)
	}
}
