//go:build darwin || linux || dragonfly || freebsd || netbsd || openbsd

package monitor

import (
	"syscall"
	"testing"
)

// The socket the prober actually opens must carry the enlarged buffer, or the
// fix only exists in theory.
func TestTheOpenedSocketHasAnEnlargedReceiveBuffer(t *testing.T) {
	conn, _, err := listen(254)
	if err != nil {
		t.Skipf("cannot open a probe socket here: %v", err)
	}
	defer conn.Close()

	sc, ok := conn.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		t.Skip("this socket does not expose its file descriptor")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		t.Skipf("cannot reach the descriptor: %v", err)
	}

	var size int
	var opErr error
	if err := raw.Control(func(fd uintptr) {
		size, opErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	}); err != nil {
		t.Skipf("cannot read the socket option: %v", err)
	}
	if opErr != nil {
		t.Skipf("cannot read SO_RCVBUF: %v", opErr)
	}

	// The kernel clamps to kern.ipc.maxsockbuf, so this asserts "much bigger
	// than the 8KB default" rather than the exact figure asked for.
	if size < 64<<10 {
		t.Errorf("SO_RCVBUF = %d, want well above the 8KB default that dropped replies", size)
	}
}
