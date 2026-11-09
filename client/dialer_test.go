// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
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
	c := New("localhost", WithPort(port), WithTimeout(2*time.Second), WithDialer(dial))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	if gotNetwork != "tcp" {
		t.Errorf("network = %q, want tcp", gotNetwork)
	}
	if want := net.JoinHostPort("localhost", strconv.Itoa(port)); gotAddr != want {
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
