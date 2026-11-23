//go:build linux

package netstate

import (
	"slices"
	"testing"
)

func TestParseProcRoute(t *testing.T) {
	text := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\n" +
		"eth0\t0010A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\n" +
		"eth0\t00000000\t0100A8C0\t0003\t0\t0\t0\t00000000\n"
	gw, ifc := parseProcRoute(text)
	if gw != "192.168.0.1" || ifc != "eth0" {
		t.Fatalf("parseProcRoute = %q, %q", gw, ifc)
	}
}

func TestParseResolvConf(t *testing.T) {
	text := "# comment\nnameserver 127.0.0.53\nsearch lan\nnameserver 127.0.0.53\n"
	if got := parseResolvConf(text); !slices.Equal(got, []string{"127.0.0.53"}) {
		t.Fatalf("parseResolvConf = %q", got)
	}
}
