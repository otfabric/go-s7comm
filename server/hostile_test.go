// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/server"
	"github.com/otfabric/go-s7comm/wire"
)

// openCOTP connects a raw COTP session to the server.
func openCOTP(t *testing.T, port int) *cotp.Conn {
	t.Helper()
	raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := cotp.Connect(ctx, raw, cotp.ClientConfig{LocalSelector: []byte{1, 0}, RemoteSelector: []byte{1, 2}, MaxTPDULength: 1024})
	if err != nil {
		t.Fatalf("cotp.Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func itoa(n int) string { return (&net.TCPAddr{Port: n}).String()[1:] }

func TestMalformedPDUDropsTheConnection(t *testing.T) {
	port := start(t, newServer(t, server.Config{}))
	conn := openCOTP(t, port)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.WriteTSDU(ctx, []byte{0x32, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ReadTSDU(ctx); err == nil {
		t.Fatal("connection still answers after a malformed PDU")
	}
}

func TestUnsupportedJobKeepsTheConnection(t *testing.T) {
	port := start(t, newServer(t, server.Config{}))
	conn := openCOTP(t, port)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	hdr, err := wire.EncodeS7Header(wire.ROSCTRJob, 1, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteTSDU(ctx, append(hdr, wire.FuncPLCStop, 0)); err != nil {
		t.Fatal(err)
	}
	setup, err := wire.EncodeSetupCommRequest(2, 1, 1, 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteTSDU(ctx, setup); err != nil {
		t.Fatal(err)
	}
	out, err := conn.ReadTSDU(ctx)
	if err != nil {
		t.Fatalf("no answer after an unsupported job: %v", err)
	}
	if h, _, err := wire.ParseS7Header(out); err != nil || h.PDURef != 2 {
		t.Fatalf("answer %x: %v", out, err)
	}
}

func TestGarbageBeforeTheHandshakeIsClosed(t *testing.T) {
	port := start(t, newServer(t, server.Config{HandshakeTimeout: 200 * time.Millisecond}))
	for name, payload := range map[string][]byte{
		"not TPKT": []byte("GET / HTTP/1.0\r\n\r\n"),
		"silent":   nil,
	} {
		raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if payload != nil {
			_, _ = raw.Write(payload)
		}
		_ = raw.SetReadDeadline(time.Now().Add(2 * time.Second))
		// EOF or a reset both mean the server closed; a timeout does not.
		if _, err := io.ReadAll(raw); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Errorf("%s: server did not close: %v", name, err)
			}
		}
		_ = raw.Close()
	}
}

func TestCloseEndsOpenConnections(t *testing.T) {
	srv := newServer(t, server.Config{})
	port := start(t, srv)
	conn := openCOTP(t, port)
	done := make(chan error, 1)
	go func() {
		_, err := conn.ReadTSDU(context.Background())
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read succeeded after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection still open after Close")
	}
}
