package client

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
)

// startFakeSetupServer starts a TCP listener that accepts one connection and performs
// COTP Accept + S7 setup. pduSizeInResponse is the PDU size to return in the setup response (e.g. 480 or 960).
// Returns the port and a cleanup function.
func startFakeSetupServer(t *testing.T, pduSizeInResponse int) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, pduSizeInResponse)
	}()
	return port, func() { _ = ln.Close() }
}

// startFakeSetupAndReadServer is like startFakeSetupServer but also responds to Read Var requests.
func startFakeSetupAndReadServer(t *testing.T, pduSizeInResponse int) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, pduSizeInResponse)
		ctx := context.Background()
		for {
			tsdu, err := srv.ReadTSDU(ctx)
			if err != nil {
				return
			}
			if len(tsdu) < 12 || tsdu[0] != 0x32 {
				continue
			}
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			if err := srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0xAB, 0xCD})); err != nil {
				return
			}
		}
	}()
	return port, func() { _ = ln.Close() }
}

// startFakeSetupAndReadServerMultiAccept accepts multiple connections; each gets COTP+setup and read loop.
// Use for tests that reconnect (second Connect() gets a new connection).
func startFakeSetupAndReadServerMultiAccept(t *testing.T, pduSizeInResponse int) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				srv := acceptFakeCOTP(t, c)
				defer func() { _ = srv.Close() }()
				serveFakeSetup(t, srv, pduSizeInResponse)
				ctx := context.Background()
				for {
					tsdu, err := srv.ReadTSDU(ctx)
					if err != nil {
						return
					}
					if len(tsdu) < 12 || tsdu[0] != 0x32 {
						continue
					}
					pduRef := binary.BigEndian.Uint16(tsdu[4:6])
					if err := srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0xAB, 0xCD})); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return port, func() { _ = ln.Close() }
}

func TestWithTSAP(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1",
		WithTSAP(0x0100, 0x0200),
		WithPort(port),
		WithRackSlot(0, 1),
		WithTimeout(2*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect with WithTSAP: %v", err)
	}
	info := c.ConnectionInfo()
	if info.LocalTSAP != 0x0100 {
		t.Errorf("ConnectionInfo().LocalTSAP = 0x%04X, want 0x0100", info.LocalTSAP)
	}
	if info.RemoteTSAP != 0x0200 {
		t.Errorf("ConnectionInfo().RemoteTSAP = 0x%04X, want 0x0200", info.RemoteTSAP)
	}
	_ = c.Close()
}

func TestWithAutoRackSlot(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1",
		WithAutoRackSlot(false),
		WithPort(port),
		WithTimeout(2*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect with WithAutoRackSlot (common pairs): %v", err)
	}
	info := c.ConnectionInfo()
	if info.Rack != 0 || info.Slot != 1 {
		t.Errorf("after auto connect: Rack=%d Slot=%d, want 0,1 (first common pair)", info.Rack, info.Slot)
	}
	_ = c.Close()
}

func TestWithRateLimit(t *testing.T) {
	port, cleanup := startFakeSetupAndReadServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1",
		WithPort(port),
		WithRackSlot(0, 1),
		WithTimeout(2*time.Second),
		WithRateLimit(25*time.Millisecond),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Rate limit applies between chunks, not before the first. With PDU 480, maxData=462; request 500 to get 2 chunks.
	start := time.Now()
	_, _ = c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 500})
	elapsed := time.Since(start)
	if elapsed < 20*time.Millisecond {
		t.Errorf("two-chunk read with 25ms rate limit between chunks took %v, expected at least ~25ms", elapsed)
	}
}

func TestWithLogger(t *testing.T) {
	port, cleanup := startFakeSetupAndReadServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1",
		WithPort(port),
		WithRackSlot(0, 1),
		WithTimeout(2*time.Second),
		WithLogger(&mockLogger{}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect with WithLogger: %v", err)
	}
	res, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 2})
	if err != nil {
		t.Fatalf("ReadArea with logger set: %v", err)
	}
	if res == nil || !res.OK() {
		t.Errorf("ReadArea result: ok=%v", res != nil && res.OK())
	}
	_ = c.Close()
}

func TestWithMaxPDU(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 960)
	defer cleanup()

	c := New("127.0.0.1",
		WithPort(port),
		WithRackSlot(0, 1),
		WithTimeout(2*time.Second),
		WithMaxPDU(960),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect with WithMaxPDU(960): %v", err)
	}
	if got := c.PDUSize(); got != 960 {
		t.Errorf("PDUSize() = %d, want 960 (negotiated from setup response)", got)
	}
	_ = c.Close()
}

func TestConnectInvalidOptions(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c := New("127.0.0.1", WithPort(-1), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	if err := c.Connect(ctx); err == nil {
		t.Error("expected error for invalid port -1")
	}

	c = New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(-time.Second))
	if err := c.Connect(ctx); err == nil {
		t.Error("expected error for negative timeout")
	}

	c = New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithMaxPDU(0))
	if err := c.Connect(ctx); err == nil {
		t.Error("expected error for max PDU 0")
	}

	c = New("127.0.0.1", WithPort(port), WithRackSlot(-1, 1))
	if err := c.Connect(ctx); err == nil {
		t.Error("expected error for negative rack")
	}

	c = New("127.0.0.1", WithPort(port), WithRackSlot(8, 0))
	if err := c.Connect(ctx); err == nil {
		t.Error("expected error for rack 8 (valid range 0..7)")
	}

	c = New("127.0.0.1", WithPort(port), WithRackSlot(0, 32))
	if err := c.Connect(ctx); err == nil {
		t.Error("expected error for slot 32 (valid range 0..31)")
	}
}

type mockLogger struct{}

func (m *mockLogger) Debug(msg string, args ...interface{}) {}
func (m *mockLogger) Info(msg string, args ...interface{})  {}
func (m *mockLogger) Error(msg string, args ...interface{}) {}
