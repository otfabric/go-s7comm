package client

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

func startFakeBitServer(t *testing.T, handle func(pduRef uint16, req []byte) []byte) (port int, cleanup func()) {
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
		serveFakeSetup(t, srv, 480)
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
			if err := srv.WriteTSDU(ctx, handle(pduRef, tsdu)); err != nil {
				return
			}
		}
	}()
	return port, func() { _ = ln.Close() }
}

func TestReadBitTrueFalse(t *testing.T) {
	for _, want := range []bool{false, true} {
		want := want
		port, cleanup := startFakeBitServer(t, func(pduRef uint16, req []byte) []byte {
			if req[10] != wire.FuncReadVar {
				t.Errorf("expected Read Var, got 0x%02X", req[10])
			}
			v := byte(0)
			if want {
				v = 1
			}
			return buildReadVarBitResponse(pduRef, v)
		})
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
		defer func() { _ = c.Close() }()
		if err := c.Connect(ctx); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		got, err := c.ReadBit(ctx, model.BitAddress{
			Area: model.AreaDB, DBNumber: 1, ByteOffset: 10, BitOffset: 3,
		})
		if err != nil {
			t.Fatalf("ReadBit(%v): %v", want, err)
		}
		if got != want {
			t.Fatalf("ReadBit: got %v want %v", got, want)
		}
	}
}

func TestWriteBitTrueFalse(t *testing.T) {
	for _, value := range []bool{false, true} {
		value := value
		var sawPayload byte
		port, cleanup := startFakeBitServer(t, func(pduRef uint16, req []byte) []byte {
			if req[10] != wire.FuncWriteVar {
				t.Errorf("expected Write Var, got 0x%02X", req[10])
			}
			// Data section starts after 10-byte header + 14-byte param (2+12).
			if len(req) >= 29 {
				sawPayload = req[28]
			}
			return buildWriteVarResponse(pduRef, wire.RetCodeSuccess)
		})
		defer cleanup()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
		defer func() { _ = c.Close() }()
		if err := c.Connect(ctx); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		if err := c.WriteBit(ctx, model.BitAddress{
			Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0,
		}, value); err != nil {
			t.Fatalf("WriteBit(%v): %v", value, err)
		}
		wantPayload := byte(0)
		if value {
			wantPayload = 1
		}
		if sawPayload != wantPayload {
			t.Fatalf("write payload: got 0x%02X want 0x%02X", sawPayload, wantPayload)
		}
	}
}

func TestReadDBBitWriteDBBit(t *testing.T) {
	port, cleanup := startFakeBitServer(t, func(pduRef uint16, req []byte) []byte {
		switch req[10] {
		case wire.FuncWriteVar:
			return buildWriteVarResponse(pduRef, wire.RetCodeSuccess)
		case wire.FuncReadVar:
			return buildReadVarBitResponse(pduRef, 1)
		default:
			return buildReadVarBitResponse(pduRef, 0)
		}
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	defer func() { _ = c.Close() }()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.WriteDBBit(ctx, 1, 10, 3, true); err != nil {
		t.Fatalf("WriteDBBit: %v", err)
	}
	v, err := c.ReadDBBit(ctx, 1, 10, 3)
	if err != nil || !v {
		t.Fatalf("ReadDBBit: %v %v", v, err)
	}
}

func TestValidateBitAddress(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	cases := []struct {
		name string
		addr model.BitAddress
	}{
		{"byteOffset", model.BitAddress{Area: model.AreaDB, DBNumber: 1, ByteOffset: -1, BitOffset: 0}},
		{"bitNeg", model.BitAddress{Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: -1}},
		{"bit8", model.BitAddress{Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 8}},
		{"dbNeg", model.BitAddress{Area: model.AreaDB, DBNumber: -1, ByteOffset: 0, BitOffset: 0}},
		{"badArea", model.BitAddress{Area: model.Area(0xFE), ByteOffset: 0, BitOffset: 0}},
	}
	for _, tt := range cases {
		_, err := c.ReadBit(ctx, tt.addr)
		if err == nil {
			t.Fatalf("%s: expected validation error", tt.name)
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("%s: expected ValidationError, got %T %v", tt.name, err, err)
		}
		if err := c.WriteBit(ctx, tt.addr, true); err == nil {
			t.Fatalf("%s write: expected validation error", tt.name)
		}
	}
}

func TestReadBitNotConnected(t *testing.T) {
	c := New("host")
	_, err := c.ReadBit(context.Background(), model.BitAddress{
		Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0,
	})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
}

func TestWriteBitNotConnected(t *testing.T) {
	c := New("host")
	err := c.WriteBit(context.Background(), model.BitAddress{
		Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0,
	}, true)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
}

func TestReadBitContextCancelled(t *testing.T) {
	port, cleanup := startFakeBitServer(t, func(pduRef uint16, _ []byte) []byte {
		return buildReadVarBitResponse(pduRef, 1)
	})
	defer cleanup()

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer setupCancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	defer func() { _ = c.Close() }()
	if err := c.Connect(setupCtx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ReadBit(ctx, model.BitAddress{
		Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestReadBitItemRejected(t *testing.T) {
	port, cleanup := startFakeBitServer(t, func(pduRef uint16, _ []byte) []byte {
		return buildReadVarRejectedResponse(pduRef, wire.RetCodeAddressFault)
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	defer func() { _ = c.Close() }()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_, err := c.ReadBit(ctx, model.BitAddress{
		Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0,
	})
	if err == nil {
		t.Fatal("expected item rejection error")
	}
	var s7err *wire.S7Error
	if !errors.As(err, &s7err) || s7err.Code != wire.RetCodeAddressFault {
		t.Fatalf("expected address fault S7Error, got %v", err)
	}
}

func TestReadBitMalformedPayload(t *testing.T) {
	port, cleanup := startFakeBitServer(t, func(pduRef uint16, _ []byte) []byte {
		// BYTE transport instead of BIT — DecodeAsBit must fail.
		return buildReadVarResponse(pduRef, 8, []byte{0x01})
	})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	defer func() { _ = c.Close() }()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_, err := c.ReadBit(ctx, model.BitAddress{
		Area: model.AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0,
	})
	if err == nil {
		t.Fatal("expected protocol error for malformed bit payload")
	}
	if !errors.Is(err, ErrProtocolFailure) {
		t.Fatalf("expected ErrProtocolFailure wrap, got %v", err)
	}
}
