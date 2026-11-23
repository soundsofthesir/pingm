//go:build linux

package netstate

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"strings"
)

func gateway(context.Context) (string, string) {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", ""
	}
	return parseProcRoute(string(b))
}

// parseProcRoute finds the default route in /proc/net/route, whose addresses
// are little-endian hex.
func parseProcRoute(text string) (string, string) {
	for _, line := range strings.Split(text, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		raw, err := hex.DecodeString(f[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, binary.LittleEndian.Uint32(raw))
		return ip.String(), f[0]
	}
	return "", ""
}

func dnsServers(context.Context) []string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	return parseResolvConf(string(b))
}

func parseResolvConf(text string) []string {
	var found []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" {
			found = append(found, f[1])
		}
	}
	return uniqueIPs(found)
}

func wifi(ctx context.Context) string {
	if ssid := strings.TrimSpace(output(ctx, "iwgetid", "-r")); ssid != "" {
		return ssid
	}
	// nmcli prints "yes:Name" for the network in use.
	for _, line := range strings.Split(output(ctx, "nmcli", "-t", "-f", "active,ssid", "dev", "wifi"), "\n") {
		if ssid, ok := strings.CutPrefix(line, "yes:"); ok {
			return strings.TrimSpace(ssid)
		}
	}
	return ""
}
