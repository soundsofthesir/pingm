//go:build windows

package netstate

import (
	"context"
	"net"
	"strings"
)

func gateway(ctx context.Context) (string, string) {
	return parseRoutePrint(output(ctx, "route", "print", "-4", "0.0.0.0")), ""
}

// parseRoutePrint finds the gateway on the 0.0.0.0/0 line of `route print`,
// whose columns are destination, mask, gateway, interface, metric.
func parseRoutePrint(text string) string {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "0.0.0.0" && f[1] == "0.0.0.0" && net.ParseIP(f[2]) != nil {
			return f[2]
		}
	}
	return ""
}

func dnsServers(ctx context.Context) []string {
	return parseNetshDNS(output(ctx, "netsh", "interface", "ipv4", "show", "dnsservers"))
}

// parseNetshDNS takes every address netsh lists. The first server on each
// interface follows a label and a colon; any others sit alone on the next
// lines, so both shapes are read.
func parseNetshDNS(text string) []string {
	var found []string
	for _, line := range strings.Split(text, "\n") {
		if _, v, ok := strings.Cut(line, ":"); ok {
			found = append(found, v)
			continue
		}
		found = append(found, line)
	}
	return uniqueIPs(found)
}

func wifi(ctx context.Context) string {
	return parseNetshWlan(output(ctx, "netsh", "wlan", "show", "interfaces"))
}

// parseNetshWlan reads the SSID line, taking care not to match BSSID.
func parseNetshWlan(text string) string {
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "SSID" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
