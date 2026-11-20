//go:build windows

package monitor

import (
	"net"
	"testing"
	"time"
	"unsafe"
)

// The reply header must match ICMP_ECHO_REPLY byte for byte, or every field
// after the first pointer is read from the wrong place.
func TestEchoReplyMatchesTheWindowsLayout(t *testing.T) {
	want := uintptr(28) // 32-bit
	if unsafe.Sizeof(uintptr(0)) == 8 {
		want = 40
	}
	if got := unsafe.Sizeof(icmpEchoReply{}); got != want {
		t.Errorf("sizeof(icmpEchoReply) = %d, want %d", got, want)
	}
}

func TestSendEchoReachesLoopback(t *testing.T) {
	if !sendEcho(net.IPv4(127, 0, 0, 1), makePayload(0, 1), time.Second) {
		t.Fatal("no echo reply from 127.0.0.1")
	}
}

func TestListenUsesTheICMPAPIWithoutAdministrator(t *testing.T) {
	conn, raw, err := listen(4)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()
	if raw {
		t.Error("fell back to a raw socket, which needs Administrator")
	}
	if _, ok := conn.(*echoConn); !ok {
		t.Errorf("listen returned %T, want *echoConn", conn)
	}
}
