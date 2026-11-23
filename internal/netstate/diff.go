package netstate

import (
	"fmt"
	"slices"
	"strings"
)

// Diff describes what changed from a to b, one plain sentence per change, in
// a fixed order. It returns nothing when the two are the same.
func Diff(a, b Snapshot) []string {
	var out []string
	out = append(out, diffInterfaces(a.Interfaces, b.Interfaces)...)

	if a.Gateway != b.Gateway || a.GatewayIface != b.GatewayIface {
		out = append(out, fmt.Sprintf("gateway %s to %s", gatewayText(a), gatewayText(b)))
	}
	if !slices.Equal(a.DNS, b.DNS) {
		out = append(out, fmt.Sprintf("DNS %s to %s", listText(a.DNS), listText(b.DNS)))
	}
	if a.WiFi != b.WiFi {
		out = append(out, fmt.Sprintf("Wi-Fi %s to %s", orNone(a.WiFi), orNone(b.WiFi)))
	}
	return out
}

func diffInterfaces(a, b []Interface) []string {
	before := map[string][]string{}
	for _, i := range a {
		before[i.Name] = i.Addrs
	}
	after := map[string][]string{}
	for _, i := range b {
		after[i.Name] = i.Addrs
	}

	var out []string
	for _, i := range a {
		if _, ok := after[i.Name]; !ok {
			out = append(out, fmt.Sprintf("%s down (was %s)", i.Name, listText(i.Addrs)))
		}
	}
	for _, i := range b {
		old, ok := before[i.Name]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s up with %s", i.Name, listText(i.Addrs)))
		case !slices.Equal(old, i.Addrs):
			out = append(out, fmt.Sprintf("%s address %s to %s", i.Name, listText(old), listText(i.Addrs)))
		}
	}
	return out
}

func gatewayText(s Snapshot) string {
	if s.Gateway == "" {
		return "none"
	}
	if s.GatewayIface == "" {
		return s.Gateway
	}
	return s.Gateway + " via " + s.GatewayIface
}

func listText(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
