//go:build darwin

package netstate

import (
	"context"
	"strings"
)

func gateway(ctx context.Context) (string, string) {
	return parseRouteGet(output(ctx, "route", "-n", "get", "default"))
}

// parseRouteGet reads `route -n get default`. With no default route the
// command fails and the text is empty, which yields no gateway.
func parseRouteGet(text string) (string, string) {
	return fieldAfter(text, "gateway"), fieldAfter(text, "interface")
}

func dnsServers(ctx context.Context) []string {
	return parseScutilDNS(output(ctx, "scutil", "--dns"))
}

// parseScutilDNS collects every "nameserver[n] : addr" line. scutil lists the
// same servers under several resolvers, so repeats are dropped.
func parseScutilDNS(text string) []string {
	var found []string
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.HasPrefix(strings.TrimSpace(k), "nameserver[") {
			found = append(found, v)
		}
	}
	return uniqueIPs(found)
}

// wifi reads the network name from the Wi-Fi interface. Recent macOS hides it
// as "<redacted>" from apps without Location access, which counts as unknown.
func wifi(ctx context.Context) string {
	dev := wifiDevice(ctx)
	if dev == "" {
		return ""
	}
	return parseSSID(output(ctx, "ipconfig", "getsummary", dev))
}

func parseSSID(text string) string {
	ssid := fieldAfter(text, "SSID")
	if strings.Contains(ssid, "redacted") {
		return ""
	}
	return ssid
}

// wifiDevice finds the BSD name of the Wi-Fi port, usually en0.
func wifiDevice(ctx context.Context) string {
	lines := strings.Split(output(ctx, "networksetup", "-listallhardwareports"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "Hardware Port: Wi-Fi" && i+1 < len(lines) {
			return fieldAfter(lines[i+1], "Device")
		}
	}
	return ""
}
