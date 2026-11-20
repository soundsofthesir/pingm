package monitor

import (
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// fakeEcho answers every address in up after delay, and leaves the rest
// unanswered until the timeout runs out.
func fakeEcho(up map[string]bool, delay time.Duration) echoFunc {
	return func(dst net.IP, _ []byte, timeout time.Duration) bool {
		if up[dst.String()] {
			time.Sleep(delay)
			return true
		}
		time.Sleep(timeout)
		return false
	}
}

func runEchoProber(t *testing.T, conn *echoConn, targets []Target, rounds int, timeout time.Duration) map[int]*Stats {
	t.Helper()
	p := newProberOn(conn, false, targets, 50*time.Millisecond, timeout)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go p.Run(ctx, rounds)

	stats := make(map[int]*Stats, len(targets))
	for i := range targets {
		stats[i] = &Stats{}
	}
	for r := range p.Results() {
		stats[r.Index].Record(r)
	}
	return stats
}

func TestEchoConnReportsAnsweringHostAsUpAndSilentHostAsDown(t *testing.T) {
	conn := newEchoConn(fakeEcho(map[string]bool{"10.0.0.1": true}, 2*time.Millisecond), 64)
	targets := []Target{
		{Display: "up", IP: net.IPv4(10, 0, 0, 1)},
		{Display: "down", IP: net.IPv4(10, 0, 0, 2)},
	}

	stats := runEchoProber(t, conn, targets, 4, 100*time.Millisecond)

	if got := stats[0]; got.Sent != 4 || got.Recv != 4 {
		t.Errorf("answering host: sent %d recv %d, want 4 and 4", got.Sent, got.Recv)
	}
	if got := stats[1]; got.Sent != 4 || got.Recv != 0 {
		t.Errorf("silent host: sent %d recv %d, want 4 and 0", got.Sent, got.Recv)
	}
}

func TestEchoConnMeasuresTheRoundTrip(t *testing.T) {
	const delay = 20 * time.Millisecond
	conn := newEchoConn(fakeEcho(map[string]bool{"10.0.0.1": true}, delay), 64)

	p := newProberOn(conn, false, []Target{{Display: "h", IP: net.IPv4(10, 0, 0, 1)}}, 50*time.Millisecond, time.Second)
	defer p.Close()
	go p.Run(context.Background(), 1)

	r := <-p.Results()
	if !r.OK {
		t.Fatal("probe was not answered")
	}
	if r.RTT < delay || r.RTT > delay+50*time.Millisecond {
		t.Errorf("RTT = %v, want about %v", r.RTT, delay)
	}
	for range p.Results() {
	}
}

// The Prober's timeout must reach each echo call, including a change made
// while probing is under way.
func TestEchoConnUsesTheProberTimeout(t *testing.T) {
	var seen atomic.Int64
	conn := newEchoConn(func(_ net.IP, _ []byte, timeout time.Duration) bool {
		seen.Store(int64(timeout))
		return true
	}, 4)
	p := newProberOn(conn, false, nil, time.Second, 300*time.Millisecond)
	defer p.Close()

	send := func() {
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: 1, Seq: 1, Data: makePayload(0, 1)}}
		b, _ := msg.Marshal(nil)
		if _, err := conn.WriteTo(b, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1)}); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64)
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := conn.ReadFrom(buf); err != nil {
			t.Fatal(err)
		}
	}

	send()
	if got := time.Duration(seen.Load()); got != 300*time.Millisecond {
		t.Errorf("echo timeout = %v, want 300ms", got)
	}
	p.SetTimeout(700 * time.Millisecond)
	send()
	if got := time.Duration(seen.Load()); got != 700*time.Millisecond {
		t.Errorf("echo timeout after change = %v, want 700ms", got)
	}
}

func TestEchoConnReplyMirrorsTheRequest(t *testing.T) {
	conn := newEchoConn(fakeEcho(map[string]bool{"10.0.0.1": true}, 0), 4)
	defer conn.Close()

	req := &icmp.Echo{ID: 7, Seq: 42, Data: makePayload(3, 9)}
	b, _ := (&icmp.Message{Type: ipv4.ICMPTypeEcho, Body: req}).Marshal(nil)
	if _, err := conn.WriteTo(b, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1)}); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1500)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, from, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := icmp.ParseMessage(1, buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	echo, ok := msg.Body.(*icmp.Echo)
	if msg.Type != ipv4.ICMPTypeEchoReply || !ok {
		t.Fatalf("got %v, want an echo reply", msg.Type)
	}
	if echo.Seq != 42 {
		t.Errorf("seq = %d, want 42", echo.Seq)
	}
	if target, number, ok := readPayload(echo.Data); !ok || target != 3 || number != 9 {
		t.Errorf("payload = %d/%d/%v, want 3/9/true", target, number, ok)
	}
	if from.String() != "10.0.0.1" {
		t.Errorf("from = %v, want 10.0.0.1", from)
	}
}

func TestEchoConnReadTimesOutLikeASocket(t *testing.T) {
	conn := newEchoConn(fakeEcho(nil, 0), 4)
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, _, err := conn.ReadFrom(make([]byte, 64))

	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("err = %v, want a timeout the receive loop will retry", err)
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("err = %v, want os.ErrDeadlineExceeded", err)
	}
}

func TestEchoConnCloseUnblocksAReader(t *testing.T) {
	conn := newEchoConn(fakeEcho(nil, 0), 4)

	done := make(chan error, 1)
	go func() {
		_, _, err := conn.ReadFrom(make([]byte, 64))
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	_ = conn.Close()

	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("err = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock ReadFrom")
	}
	if _, err := conn.WriteTo(nil, &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}); !errors.Is(err, net.ErrClosed) {
		t.Errorf("WriteTo after Close: err = %v, want net.ErrClosed", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestEchoConnRefusesPastItsLimit(t *testing.T) {
	release := make(chan struct{})
	conn := newEchoConn(func(net.IP, []byte, time.Duration) bool {
		<-release
		return false
	}, 2)
	defer conn.Close()
	defer close(release)

	b, _ := (&icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{Data: makePayload(0, 1)}}).Marshal(nil)
	dst := &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}
	for i := 0; i < 2; i++ {
		if _, err := conn.WriteTo(b, dst); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := conn.WriteTo(b, dst); !errors.Is(err, errEchoBusy) {
		t.Errorf("third send: err = %v, want errEchoBusy", err)
	}
}

func TestEchoConnRejectsWhatItCannotSend(t *testing.T) {
	conn := newEchoConn(fakeEcho(nil, 0), 4)
	defer conn.Close()

	reply, _ := (&icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: &icmp.Echo{Data: []byte("x")}}).Marshal(nil)
	if _, err := conn.WriteTo(reply, &net.IPAddr{IP: net.IPv4(10, 0, 0, 1)}); err == nil {
		t.Error("an echo reply was accepted for sending")
	}
	echo, _ := (&icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{Data: []byte("x")}}).Marshal(nil)
	if _, err := conn.WriteTo(echo, &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1)}); err == nil {
		t.Error("a TCP address was accepted")
	}
}
