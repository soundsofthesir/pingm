//go:build windows

package monitor

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// maxConcurrentEchoes caps the IcmpSendEcho calls waiting at once. Each one
// holds an OS thread, so this stays well under the Go runtime's 10,000-thread
// limit. With the automatic timeout a round finishes before the next starts,
// so even 1024 hosts stay under the cap. Only a long -t on a large sweep can
// reach it, and probes past it are counted as lost.
const maxConcurrentEchoes = 2048

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
)

// icmpEchoReply is ICMP_ECHO_REPLY. Go lays out uintptr fields with the same
// alignment as the C pointers they stand for, on both 32- and 64-bit.
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

// ipOptionInformation is IP_OPTION_INFORMATION.
type ipOptionInformation struct {
	TTL, TOS, Flags, OptionsSize uint8
	OptionsData                  uintptr
}

// ipSuccess is the Status of an echo reply, as opposed to an ICMP error
// such as destination unreachable.
const ipSuccess = 0

// listenDatagram opens the Windows equivalent of the unprivileged ICMP
// socket. Windows has no such socket, and the raw-socket fallback needs
// Administrator and is often blocked by Windows Firewall, so this uses the
// ICMP API in iphlpapi.dll, which needs neither.
func listenDatagram(int) (net.PacketConn, error) {
	if err := procIcmpSendEcho.Find(); err != nil {
		return nil, fmt.Errorf("loading the Windows ICMP API: %w", err)
	}
	return newEchoConn(sendEcho, maxConcurrentEchoes), nil
}

// tune does nothing: the ICMP API has no receive queue to size.
func tune(net.PacketConn, int) {}

// sendEcho sends one echo request with IcmpSendEcho and waits for the reply.
//
// Each call opens its own ICMP handle, so concurrent calls share no state.
// Opening a handle costs far less than the round trip it is used for.
func sendEcho(dst net.IP, data []byte, timeout time.Duration) bool {
	ip4 := dst.To4()
	if ip4 == nil || len(data) == 0 {
		return false
	}
	// IPAddr is an in_addr: the address bytes in network order, read as a
	// little-endian uint32 on every architecture Windows runs on.
	addr := binary.LittleEndian.Uint32(ip4)

	h, _, _ := procIcmpCreateFile.Call()
	if windows.Handle(h) == windows.InvalidHandle {
		return false
	}
	defer procIcmpCloseHandle.Call(h)

	// The reply buffer must hold the reply header, the echoed data, and 8
	// bytes for an ICMP error. A []uint64 keeps it aligned for the header.
	size := int(unsafe.Sizeof(icmpEchoReply{})) + len(data) + 8 + 8
	buf := make([]uint64, (size+7)/8)

	n, _, _ := procIcmpSendEcho.Call(
		h,
		uintptr(addr),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0, // No IP options.
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)*8),
		uintptr(timeoutMillis(timeout)),
	)
	if n == 0 {
		return false // Timed out, or the request could not be sent.
	}
	reply := (*icmpEchoReply)(unsafe.Pointer(&buf[0]))
	return reply.Status == ipSuccess && reply.Address == addr
}

// timeoutMillis rounds up, so a sub-millisecond timeout still waits at all.
func timeoutMillis(d time.Duration) uint32 {
	ms := (d + time.Millisecond - 1) / time.Millisecond
	if ms < 1 {
		return 1
	}
	return uint32(ms)
}
