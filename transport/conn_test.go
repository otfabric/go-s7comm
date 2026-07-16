package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
)

func openPipePair(t *testing.T) (client, server *Conn) {
	t.Helper()
	c1, c2 := net.Pipe()
	errCh := make(chan error, 1)
	var srvCOTP *cotp.Conn
	go func() {
		var err error
		srvCOTP, err = cotp.Accept(context.Background(), c2, cotp.ServerConfig{MaxTPDULength: 1024})
		errCh <- err
	}()
	cli, err := Connect(context.Background(), c1, Config{
		LocalTSAP:     0x0100,
		RemoteTSAP:    0x0102,
		MaxTPDULength: 1024,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Accept: %v", err)
	}
	srv := FromCOTP(srvCOTP)
	t.Cleanup(func() {
		_ = cli.Close()
		_ = srv.Close()
	})
	return cli, srv
}

func TestReadWriteTSDUWithNetPipe(t *testing.T) {
	cli, srv := openPipePair(t)
	ctx := context.Background()
	want := []byte{0x32, 0x01, 0x00, 0x00}

	errCh := make(chan error, 1)
	var got []byte
	go func() {
		var err error
		got, err = srv.ReadTSDU(ctx)
		errCh <- err
	}()
	if err := cli.WriteTSDU(ctx, want); err != nil {
		t.Fatalf("WriteTSDU: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ReadTSDU: %v", err)
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWriteTSDUContextCancelled(t *testing.T) {
	cli, _ := openPipePair(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cli.WriteTSDU(ctx, []byte{0x32, 0x01, 0x00, 0x00})
	if err == nil {
		t.Fatal("WriteTSDU with cancelled context should return error")
	}
}

func TestReadTSDUContextCancelled(t *testing.T) {
	cli, _ := openPipePair(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cli.ReadTSDU(ctx)
	if err == nil {
		t.Fatal("ReadTSDU with cancelled context should return error")
	}
}

func TestCloseLocalAddrRemoteAddr(t *testing.T) {
	cli, _ := openPipePair(t)
	if cli.LocalAddr() != nil {
		t.Log("LocalAddr (pipe):", cli.LocalAddr())
	}
	if cli.RemoteAddr() != nil {
		t.Log("RemoteAddr (pipe):", cli.RemoteAddr())
	}
	if err := cli.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = cli.Close()
}

func TestDialAndConnectTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.Close() }()
		srv, err := cotp.Accept(context.Background(), conn, cotp.ServerConfig{MaxTPDULength: 1024})
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = srv.Close() }()
		tsdu, err := srv.ReadTSDU(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		errCh <- srv.WriteTSDU(context.Background(), tsdu)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cli, err := Dial(ctx, ln.Addr().String(), Config{
		LocalTSAP:  0x0100,
		RemoteTSAP: 0x0102,
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = cli.Close() }()

	if cli.LocalAddr() == nil {
		t.Error("LocalAddr should be non-nil for TCP")
	}
	if cli.RemoteAddr() == nil {
		t.Error("RemoteAddr should be non-nil for TCP")
	}

	want := []byte{0x32, 0x01, 0x00, 0x00, 0x00, 0x01}
	if err := cli.WriteTSDU(ctx, want); err != nil {
		t.Fatalf("WriteTSDU: %v", err)
	}
	got, err := cli.ReadTSDU(ctx)
	if err != nil {
		t.Fatalf("ReadTSDU: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestNilConnOperations(t *testing.T) {
	var c *Conn
	if err := c.WriteTSDU(context.Background(), []byte{1}); err != ErrConnectionNotEstablished {
		t.Fatalf("WriteTSDU nil: got %v", err)
	}
	if _, err := c.ReadTSDU(context.Background()); err != ErrConnectionNotEstablished {
		t.Fatalf("ReadTSDU nil: got %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close nil: %v", err)
	}
}

// BenchmarkWriteTSDU measures WriteTSDU throughput over a pipe.
func BenchmarkWriteTSDU(b *testing.B) {
	c1, c2 := net.Pipe()
	errCh := make(chan error, 1)
	var srv *cotp.Conn
	go func() {
		var err error
		srv, err = cotp.Accept(context.Background(), c2, cotp.ServerConfig{MaxTPDULength: 1024})
		errCh <- err
		if err != nil {
			return
		}
		buf := make([]byte, 0)
		_ = buf
		for {
			if _, err := srv.ReadTSDU(context.Background()); err != nil {
				return
			}
		}
	}()
	cli, err := Connect(context.Background(), c1, Config{MaxTPDULength: 1024})
	if err != nil {
		b.Fatalf("Connect: %v", err)
	}
	if err := <-errCh; err != nil {
		b.Fatalf("Accept: %v", err)
	}
	defer func() { _ = cli.Close() }()
	defer func() { _ = srv.Close() }()

	payload := []byte{0x32, 0x01, 0x00, 0x00}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cli.WriteTSDU(ctx, payload)
	}
}
