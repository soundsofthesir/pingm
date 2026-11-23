// Package netstate reads the parts of the machine's network setup that a
// person changes while fixing a connection: which interfaces are up and with
// what address, the default gateway, the DNS servers, and the Wi-Fi network.
//
// Every reading is best effort. A command that is missing or slow leaves its
// field empty rather than failing the snapshot, because a partial picture is
// still worth logging.
package netstate

import (
	"context"
	"net"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// commandTimeout bounds each helper command, so one hung tool cannot stall
// the poll that runs alongside the table.
const commandTimeout = 2 * time.Second

// Interface is one usable network interface and its IPv4 addresses.
//
// IPv6 is left out on purpose: privacy addresses rotate on their own, so
// logging them would bury the changes a person made under ones they did not.
type Interface struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
}

// Snapshot is the network setup at one moment.
type Snapshot struct {
	Interfaces   []Interface `json:"interfaces,omitempty"`
	Gateway      string      `json:"gateway,omitempty"`
	GatewayIface string      `json:"gateway_iface,omitempty"`
	DNS          []string    `json:"dns,omitempty"`
	WiFi         string      `json:"wifi,omitempty"`
}

// Collect reads the current network setup.
func Collect(ctx context.Context) Snapshot {
	s := Snapshot{Interfaces: interfaces()}
	s.Gateway, s.GatewayIface = gateway(ctx)
	s.DNS = dnsServers(ctx)
	s.WiFi = wifi(ctx)
	return s
}

// interfaces lists every interface that is up, is not loopback, and has at
// least one IPv4 address. An interface with only a link-local address is
// skipped: macOS keeps several of those around permanently, and none of them
// carry traffic.
func interfaces() []Interface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Interface
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		var v4 []string
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			v4 = append(v4, (&net.IPNet{IP: ip, Mask: net.CIDRMask(ones, 32)}).String())
		}
		if len(v4) == 0 {
			continue
		}
		sort.Strings(v4)
		out = append(out, Interface{Name: ifc.Name, Addrs: v4})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// output runs a helper command and returns its stdout, or "" if it failed.
func output(ctx context.Context, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(b)
}

// fieldAfter returns the trimmed text after the first "key:" on any line, or
// "" if no line has it. It tolerates the padding macOS and Windows tools put
// around the colon.
func fieldAfter(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// uniqueIPs keeps the valid IP addresses from candidates, in first-seen
// order, without repeats.
func uniqueIPs(candidates []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if net.ParseIP(c) == nil || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
