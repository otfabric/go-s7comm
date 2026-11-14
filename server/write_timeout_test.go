// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/server"
	"github.com/otfabric/go-s7comm/wire"
)

// servedWithin reports whether a new client gets a Setup Communication answer.
func servedWithin(port int, d time.Duration) bool {
	raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), d)
	if err != nil {
		return false
	}
	defer func() { _ = raw.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	conn, err := cotp.Connect(ctx, raw, cotp.ClientConfig{LocalSelector: []byte{1, 0}, RemoteSelector: []byte{1, 2}, MaxTPDULength: 1024})
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	setup, _ := wire.EncodeSetupCommRequest(1, 1, 1, 480)
	if err := conn.WriteTSDU(ctx, setup); err != nil {
		return false
	}
	_, err = conn.ReadTSDU(ctx)
	return err == nil
}

// IdleTimeout also covers answers: a client that sends requests but never
// reads the answers loses its connection and its slot.
func TestIdleTimeoutCoversUnreadAnswers(t *testing.T) {
	srv, err := server.New(server.Config{MaxPDUSize: 960, MaxConnections: 1, IdleTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	must(t, srv.RegisterDB(1, make([]byte, 900), server.AreaOptions{}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = srv.Serve(context.Background(), ln); close(done) }()
	defer func() { _ = srv.Close(); <-done }()
	port := ln.Addr().(*net.TCPAddr).Port

	raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	_ = raw.(*net.TCPConn).SetReadBuffer(4096)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	conn, err := cotp.Connect(ctx, raw, cotp.ClientConfig{LocalSelector: []byte{1, 0}, RemoteSelector: []byte{1, 2}, MaxTPDULength: 1024})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	req, err := wire.EncodeReadVarRequest(1, []wire.S7AnyAddress{{Area: wire.AreaDB, DBNumber: 1, Start: 0, Size: 900}})
	if err != nil {
		t.Fatal(err)
	}
	for range 5000 {
		wctx, wc := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := conn.WriteTSDU(wctx, req)
		wc()
		if err != nil {
			break
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if servedWithin(port, 500*time.Millisecond) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("a client that does not read its answers still holds the only slot")
}
