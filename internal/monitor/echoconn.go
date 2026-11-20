package monitor

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// echoFunc sends one echo request carrying data to dst and blocks until the
// matching reply arrives or timeout passes. It reports whether a reply came.
type echoFunc func(dst net.IP, data []byte, timeout time.Duration) bool

// errEchoBusy is returned when too many echo requests are already waiting.
var errEchoBusy = errors.New("too many echo requests outstanding")

// echoConn is a net.PacketConn built on a blocking "send one echo and wait"
// call, for Windows, which has no ICMP socket an ordinary user can open but
// does have IcmpSendEcho.
//
// It lets the Prober run unchanged. WriteTo takes the encoded echo request,
// makes the blocking call on its own goroutine, and when a reply comes back
// it queues a matching echo reply for ReadFrom. Sequence matching, timeouts
// and loss accounting all stay in the Prober.
type echoConn struct {
	echo echoFunc

	// timeout is how long each call may wait. The Prober points it at its own
	// timeout before probing starts, so a change made while running applies
	// to the next probe.
	timeout func() time.Duration

	// slots caps the calls in flight. Each one holds an OS thread while it
	// waits, and the Go runtime aborts the process past 10,000 threads.
	slots chan struct{}

	replies   chan echoReply
	done      chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	deadline time.Time
}

type echoReply struct {
	msg  []byte
	from net.Addr
}

func newEchoConn(echo echoFunc, limit int) *echoConn {
	return &echoConn{
		echo:    echo,
		timeout: func() time.Duration { return MaxAutoTimeout },
		slots:   make(chan struct{}, limit),
		replies: make(chan echoReply, limit),
		done:    make(chan struct{}),
	}
}

func (c *echoConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	default:
	}

	ip := addrIP(addr)
	if ip == nil {
		return 0, &net.AddrError{Err: "not an IP address", Addr: addr.String()}
	}
	msg, err := icmp.ParseMessage(ipv4.ICMPTypeEcho.Protocol(), b)
	if err != nil {
		return 0, err
	}
	req, ok := msg.Body.(*icmp.Echo)
	if msg.Type != ipv4.ICMPTypeEcho || !ok {
		return 0, errors.New("only echo requests can be sent")
	}

	select {
	case c.slots <- struct{}{}:
	default:
		return 0, errEchoBusy
	}
	go c.exchange(ip, req)
	return len(b), nil
}

// exchange makes one blocking echo call and, if it was answered, queues the
// reply the Prober is waiting to read.
func (c *echoConn) exchange(ip net.IP, req *icmp.Echo) {
	answered := c.echo(ip, req.Data, c.timeout())
	<-c.slots
	if !answered {
		return // The Prober's sweeper counts it as lost.
	}

	reply := icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: req}
	encoded, err := reply.Marshal(nil)
	if err != nil {
		return
	}
	select {
	case c.replies <- echoReply{msg: encoded, from: &net.IPAddr{IP: ip}}:
	case <-c.done:
	}
}

func (c *echoConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()

	var expired <-chan time.Time
	if !deadline.IsZero() {
		wait := time.Until(deadline)
		if wait <= 0 {
			return 0, nil, os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		expired = timer.C
	}

	select {
	case r := <-c.replies:
		return copy(b, r.msg), r.from, nil
	case <-expired:
		return 0, nil, os.ErrDeadlineExceeded
	case <-c.done:
		return 0, nil, net.ErrClosed
	}
}

func (c *echoConn) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return nil
}

func (c *echoConn) LocalAddr() net.Addr { return &net.IPAddr{IP: net.IPv4zero} }

func (c *echoConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *echoConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

// SetWriteDeadline does nothing: WriteTo never blocks.
func (c *echoConn) SetWriteDeadline(time.Time) error { return nil }

func addrIP(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.IP
	case *net.IPAddr:
		return a.IP
	}
	return nil
}
