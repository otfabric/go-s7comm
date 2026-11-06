// SPDX-License-Identifier: MIT

package server_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/server"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// start runs srv on a loopback port and stops it when the test ends.
func start(t *testing.T, srv *server.Server) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; !errors.Is(err, server.ErrServerClosed) {
			t.Errorf("Serve = %v, want ErrServerClosed", err)
		}
	})
	return ln.Addr().(*net.TCPAddr).Port
}

func newServer(t *testing.T, cfg server.Config) *server.Server {
	t.Helper()
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	db1 := make([]byte, 64)
	copy(db1, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	must(t, srv.RegisterDB(1, db1, server.AreaOptions{}))
	must(t, srv.RegisterDB(2, bytes.Repeat([]byte{0xAA}, 16), server.AreaOptions{ReadOnly: true}))
	must(t, srv.RegisterArea(server.AreaMerkers, make([]byte, 32), server.AreaOptions{}))
	must(t, srv.RegisterArea(server.AreaInputs, []byte{0x11, 0x22, 0x33, 0x44}, server.AreaOptions{}))
	must(t, srv.RegisterArea(server.AreaOutputs, []byte{0x55, 0x66}, server.AreaOptions{}))
	return srv
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func dial(t *testing.T, port int, opts ...client.Option) *client.Client {
	t.Helper()
	opts = append([]client.Option{client.WithPort(port), client.WithRackSlot(0, 2), client.WithTimeout(2 * time.Second)}, opts...)
	c := client.New("127.0.0.1", opts...)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func readDB(t *testing.T, c *client.Client, db, off, size int) []byte {
	t.Helper()
	res, err := c.ReadDB(context.Background(), db, off, size)
	if err != nil {
		t.Fatalf("ReadDB(%d,%d,%d): %v", db, off, size, err)
	}
	if err := res.Err(); err != nil {
		t.Fatalf("ReadDB(%d,%d,%d) outcome: %v", db, off, size, err)
	}
	return res.Data
}

func TestClientReadsAndWritesEveryArea(t *testing.T) {
	srv := newServer(t, server.Config{})
	c := dial(t, start(t, srv))
	ctx := context.Background()

	if got := c.PDUSize(); got != 480 {
		t.Errorf("PDUSize = %d, want 480", got)
	}
	if got := readDB(t, c, 1, 0, 8); !bytes.Equal(got, []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("DB1 = %x", got)
	}
	must(t, c.WriteDB(ctx, 1, 9, []byte{0xDE, 0xAD, 0xBE}))
	if got := readDB(t, c, 1, 9, 3); !bytes.Equal(got, []byte{0xDE, 0xAD, 0xBE}) {
		t.Errorf("DB1 after write = %x", got)
	}
	if got, _ := srv.Read(server.AreaDB, 1, 9, 3); !bytes.Equal(got, []byte{0xDE, 0xAD, 0xBE}) {
		t.Errorf("host Read = %x", got)
	}

	in, err := c.ReadInputs(ctx, 1, 2)
	must(t, err)
	if !bytes.Equal(in.Data, []byte{0x22, 0x33}) {
		t.Errorf("inputs = %x", in.Data)
	}
	out, err := c.ReadOutputs(ctx, 0, 2)
	must(t, err)
	if !bytes.Equal(out.Data, []byte{0x55, 0x66}) {
		t.Errorf("outputs = %x", out.Data)
	}
	must(t, c.WriteArea(ctx, model.Address{Area: model.AreaMerkers, Start: 4, Size: 2}, []byte{0x12, 0x34}))
	mk, err := c.ReadMerkers(ctx, 4, 2)
	must(t, err)
	if !bytes.Equal(mk.Data, []byte{0x12, 0x34}) {
		t.Errorf("markers = %x", mk.Data)
	}
}

func TestClientBitAccessChangesOneBit(t *testing.T) {
	srv := newServer(t, server.Config{})
	c := dial(t, start(t, srv))
	ctx := context.Background()
	must(t, srv.Write(server.AreaDB, 1, 20, []byte{0b1010_0000}))

	must(t, c.WriteDBBit(ctx, 1, 20, 0, true))
	must(t, c.WriteDBBit(ctx, 1, 20, 7, false))
	if got, _ := srv.Read(server.AreaDB, 1, 20, 1); got[0] != 0b0010_0001 {
		t.Errorf("byte after bit writes = %08b", got[0])
	}
	for bit, want := range []bool{true, false, false, false, false, true, false, false} {
		got, err := c.ReadDBBit(ctx, 1, 20, bit)
		must(t, err)
		if got != want {
			t.Errorf("bit %d = %v, want %v", bit, got, want)
		}
	}
}

func TestClientSeesRefusals(t *testing.T) {
	srv := newServer(t, server.Config{})
	c := dial(t, start(t, srv))
	ctx := context.Background()

	res, err := c.ReadDB(ctx, 1, 60, 8)
	must(t, err)
	if res.Err() == nil {
		t.Error("read past the end of DB1 succeeded")
	}
	res, err = c.ReadDB(ctx, 9, 0, 1)
	must(t, err)
	if res.Err() == nil {
		t.Error("read of unregistered DB9 succeeded")
	}
	if err := c.WriteDB(ctx, 1, 63, []byte{1, 2}); err == nil {
		t.Error("write past the end of DB1 succeeded")
	}
	if err := c.WriteDB(ctx, 2, 0, []byte{0}); err == nil {
		t.Error("write to read-only DB2 succeeded")
	}
	if got := readDB(t, c, 2, 0, 1); got[0] != 0xAA {
		t.Errorf("read-only DB2 changed: %x", got)
	}
}

func TestClientLargeReadWithinThePDU(t *testing.T) {
	srv, err := server.New(server.Config{})
	must(t, err)
	big := make([]byte, 1024)
	for i := range big {
		big[i] = byte(i)
	}
	must(t, srv.RegisterDB(5, big, server.AreaOptions{}))
	c := dial(t, start(t, srv))
	// The client splits reads larger than its PDU into several requests.
	got := readDB(t, c, 5, 0, len(big))
	if !bytes.Equal(got, big) {
		t.Error("large read differs")
	}
}

func TestPDUSizeIsTheSmallerOfBoth(t *testing.T) {
	srv, err := server.New(server.Config{MaxPDUSize: 960})
	must(t, err)
	must(t, srv.RegisterDB(1, make([]byte, 4), server.AreaOptions{}))
	port := start(t, srv)
	for _, tc := range []struct{ ask, want int }{{100, 100}, {480, 480}, {2000, 960}} {
		c := dial(t, port, client.WithMaxPDU(tc.ask))
		if got := c.PDUSize(); got != tc.want {
			t.Errorf("asked %d: PDUSize = %d, want %d", tc.ask, got, tc.want)
		}
	}
}

func TestConcurrentClientsAndReconnect(t *testing.T) {
	srv := newServer(t, server.Config{})
	port := start(t, srv)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			c := client.New("127.0.0.1", client.WithPort(port), client.WithRackSlot(0, 2), client.WithTimeout(2*time.Second))
			if err := c.Connect(context.Background()); err != nil {
				t.Errorf("Connect: %v", err)
				return
			}
			defer func() { _ = c.Close() }()
			for range 20 {
				res, err := c.ReadDB(context.Background(), 1, 0, 8)
				if err != nil || res.Err() != nil || !bytes.Equal(res.Data, []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
					t.Errorf("concurrent read: %v %v", err, res)
					return
				}
			}
		})
	}
	wg.Wait()

	c := dial(t, port)
	must(t, c.WriteDB(context.Background(), 1, 30, []byte{0x42}))
	_ = c.Close()
	c2 := dial(t, port)
	if got := readDB(t, c2, 1, 30, 1); got[0] != 0x42 {
		t.Errorf("after reconnect = %x", got)
	}
}

func TestConnectionLimitRefusesExtraClients(t *testing.T) {
	srv := newServer(t, server.Config{MaxConnections: 1})
	port := start(t, srv)
	dial(t, port)
	c := client.New("127.0.0.1", client.WithPort(port), client.WithRackSlot(0, 2), client.WithTimeout(time.Second))
	if err := c.Connect(context.Background()); err == nil {
		_ = c.Close()
		t.Fatal("second client connected past MaxConnections 1")
	}
}

func TestIdleTimeoutClosesTheConnection(t *testing.T) {
	srv := newServer(t, server.Config{IdleTimeout: 100 * time.Millisecond})
	c := dial(t, start(t, srv))
	time.Sleep(300 * time.Millisecond)
	if res, err := c.ReadDB(context.Background(), 1, 0, 1); err == nil && res.Err() == nil {
		t.Fatal("read on an idle-closed connection succeeded")
	}
}

func TestServeEndsWithTheContext(t *testing.T) {
	srv := newServer(t, server.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx, "127.0.0.1:0") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ListenAndServe = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return")
	}
	// Closed: further Serve calls end at once.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	if err := srv.Serve(context.Background(), ln); !errors.Is(err, server.ErrServerClosed) {
		t.Errorf("Serve after close = %v", err)
	}
	must(t, srv.Close())
}

func TestListenAndServeBadAddress(t *testing.T) {
	srv := newServer(t, server.Config{})
	if err := srv.ListenAndServe(context.Background(), "127.0.0.1:-1"); err == nil {
		t.Fatal("ListenAndServe on a bad address succeeded")
	}
}
