package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
)

func TestCloseClearsConnection(t *testing.T) {
	c1, c2 := net.Pipe()
	errCh := make(chan error, 1)
	var srv *cotp.Conn
	go func() {
		var err error
		srv, err = cotp.Accept(context.Background(), c2, cotp.ServerConfig{MaxTPDULength: 1024})
		errCh <- err
	}()
	cli, err := cotp.Connect(context.Background(), c1, cotp.ClientConfig{MaxTPDULength: 1024})
	if err != nil {
		t.Fatalf("cotp.Connect: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("cotp.Accept: %v", err)
	}
	defer func() { _ = srv.Close() }()

	c := New("127.0.0.1")
	c.conn = cli

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
