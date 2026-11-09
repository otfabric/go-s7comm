// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithDialerUsed(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	var gotNetwork, gotAddr string
	var gotDeadline bool
	var deadline, called time.Time
	start := time.Now()
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = time.Now()
		gotNetwork, gotAddr = network, addr
		deadline, gotDeadline = ctx.Deadline()
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	c := New("127.0.0.1", WithPort(port), WithTimeout(2*time.Second), WithDialer(dial))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	if gotNetwork != "tcp" {
		t.Errorf("network = %q, want tcp", gotNetwork)
	}
	if want := net.JoinHostPort("127.0.0.1", strconv.Itoa(port)); gotAddr != want {
		t.Errorf("addr = %q, want %q", gotAddr, want)
	}
	if !gotDeadline {
		t.Error("dialer context has no deadline from WithTimeout")
	} else if deadline.Before(start.Add(2*time.Second)) || deadline.After(called.Add(2*time.Second)) {
		t.Errorf("dialer deadline is %v after the dial call, want the WithTimeout of 2s", deadline.Sub(called))
	}
}

func TestWithDialerNilConn(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, nil }
	c := New("127.0.0.1", WithDialer(dial))
	err := c.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dialer returned no connection") {
		t.Fatalf("Connect error = %v, want the no-connection error", err)
	}
}

func TestWithDialerError(t *testing.T) {
	errBlocked := errors.New("blocked")
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, errBlocked }
	c := New("127.0.0.1", WithDialer(dial))
	err := c.Connect(context.Background())
	if !errors.Is(err, errBlocked) {
		t.Fatalf("Connect error = %v, want it to wrap the dialer error", err)
	}
}

func TestWithDialerNoTimeoutKeepsContext(t *testing.T) {
	var gotDeadline bool
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		_, gotDeadline = ctx.Deadline()
		return nil, errors.New("stop")
	}
	c := New("127.0.0.1", WithTimeout(0), WithDialer(dial))
	_ = c.Connect(context.Background())
	if gotDeadline {
		t.Error("dialer context has a deadline although the timeout is 0")
	}
}

// closeTrackingConn records whether Close was called.
type closeTrackingConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *closeTrackingConn) Close() error {
	c.closed.Store(true)
	return nil
}

func TestWithDialerConnAndErrorClosesConn(t *testing.T) {
	errBlocked := errors.New("blocked")
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	tracked := &closeTrackingConn{Conn: a}
	dial := func(context.Context, string, string) (net.Conn, error) { return tracked, errBlocked }
	c := New("127.0.0.1", WithDialer(dial))
	if err := c.Connect(context.Background()); !errors.Is(err, errBlocked) {
		t.Fatalf("Connect error = %v, want it to wrap the dialer error", err)
	}
	if !tracked.closed.Load() {
		t.Error("connection returned together with an error was not closed")
	}
}

func TestWithDialerAutoRackSlotWrapsLastError(t *testing.T) {
	errBlocked := errors.New("blocked")
	tests := []struct {
		name  string
		brute bool
		calls int32
	}{
		{"common", false, 5},
		{"brute", true, 24},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			dial := func(context.Context, string, string) (net.Conn, error) {
				calls.Add(1)
				return nil, errBlocked
			}
			c := New("127.0.0.1", WithAutoRackSlot(tt.brute), WithDialer(dial))
			err := c.Connect(context.Background())
			if !errors.Is(err, errBlocked) {
				t.Fatalf("Connect error = %v, want it to wrap the dialer error", err)
			}
			if !strings.Contains(err.Error(), "auto rack/slot connect failed") {
				t.Errorf("Connect error = %v, want the auto rack/slot prefix", err)
			}
			if got := calls.Load(); got != tt.calls {
				t.Errorf("dialer called %d times, want %d", got, tt.calls)
			}
		})
	}
}

func TestWithDialerAutoRackSlotSucceeds(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	var calls atomic.Int32
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		calls.Add(1)
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	c := New("127.0.0.1", WithPort(port), WithAutoRackSlot(false), WithDialer(dial))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	if got := calls.Load(); got != 1 {
		t.Errorf("dialer called %d times, want 1", got)
	}
}

func TestWithDialerExplicitTSAP(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	var called atomic.Bool
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		called.Store(true)
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	c := New("127.0.0.1", WithPort(port), WithTSAP(0x0100, 0x0102), WithDialer(dial))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	if !called.Load() {
		t.Error("dialer not used with WithTSAP")
	}
}

func TestWithDialerParentContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	c := New("127.0.0.1", WithTimeout(0), WithDialer(dial))
	if err := c.Connect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect error = %v, want context.Canceled", err)
	}
}

func TestWithDialerTimeoutExpires(t *testing.T) {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	c := New("127.0.0.1", WithTimeout(20*time.Millisecond), WithDialer(dial))
	if err := c.Connect(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect error = %v, want context.DeadlineExceeded", err)
	}
}

func TestWithDialerNilUsesDefault(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1", WithPort(port), WithDialer(nil))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect with nil dialer: %v", err)
	}
	_ = c.Close()
}
