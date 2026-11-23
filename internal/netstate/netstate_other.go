//go:build !(darwin || linux || windows)

package netstate

import "context"

func gateway(context.Context) (string, string) { return "", "" }
func dnsServers(context.Context) []string      { return nil }
func wifi(context.Context) string              { return "" }
