package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/transport"
)

func TestCloseClearsConnection(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = right.Close() }()

	c := New("127.0.0.1")
	c.conn = transport.New(left, time.Second)

	if err := c.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if c.conn != nil {
		t.Fatal("expected connection to be cleared after close")
	}

	if err := c.Close(); err != nil {
		t.Fatalf("second close should be a no-op, got: %v", err)
	}
}

func TestConnectOnceFailureClearsConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New(
		"127.0.0.1",
		WithPort(port),
		WithTimeout(200*time.Millisecond),
	)

	err = c.connectOnce(context.Background(), 0, 1)
	if err == nil {
		t.Fatal("expected connectOnce to fail when peer closes during handshake")
	}

	<-accepted
	if c.conn != nil {
		t.Fatal("expected stale connection to be cleared on connect failure")
	}
}
