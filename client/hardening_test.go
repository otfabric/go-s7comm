// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// Addresses that do not fit the S7ANY fields are refused before anything is
// sent: truncating them would read or write other memory.
func TestAddressesOutsideS7AnyAreRefused(t *testing.T) {
	c := New("127.0.0.1") // never connected: validation comes first
	ctx := context.Background()
	var ve *ValidationError
	check := func(name string, err error) {
		t.Helper()
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want *ValidationError", name, err)
		}
	}
	_, err := c.ReadDB(ctx, wire.MaxS7AnyDBNumber+1, 0, 1)
	check("read DB 65536", err)
	check("write DB 65536", c.WriteDB(ctx, wire.MaxS7AnyDBNumber+1, 0, []byte{1}))
	_, err = c.ReadMerkers(ctx, wire.MaxS7AnyByteOffset+1, 1)
	check("read start past 24-bit", err)
	_, err = c.ReadDB(ctx, 1, wire.MaxS7AnyByteOffset, 2)
	check("read end past 24-bit", err)
	_, err = c.ReadDB(ctx, 1, 0, math.MaxInt)
	check("read huge size", err)
	check("write end past 24-bit", c.WriteDB(ctx, 1, wire.MaxS7AnyByteOffset, []byte{1, 2}))
	_, err = c.ReadArea(ctx, model.Address{Area: model.AreaMerkers, DBNumber: -1, Size: 1})
	check("negative DB number, non-DB area", err)
	_, err = c.ReadDBBit(ctx, 1<<16, 0, 0)
	check("bit DB 65536", err)
	check("bit offset past 24-bit", c.WriteDBBit(ctx, 1, wire.MaxS7AnyByteOffset+1, 0, true))

	// The last byte is still addressable.
	if _, err := c.ReadDB(ctx, wire.MaxS7AnyDBNumber, wire.MaxS7AnyByteOffset, 1); errors.As(err, &ve) {
		t.Errorf("last byte refused: %v", err)
	}
}

func TestConnectRefusesValuesOutsideTheirFields(t *testing.T) {
	var ve *ValidationError
	for name, c := range map[string]*Client{
		"max PDU 65536": New("127.0.0.1", WithMaxPDU(65536)),
		"port 65536":    New("127.0.0.1", WithPort(65536)),
	} {
		if err := c.Connect(context.Background()); !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want *ValidationError", name, err)
		}
	}
}

// The PDU reference wraps from 65535 to 1, never to 0 ("no correlation").
func TestPDURefSkipsZeroOnWrap(t *testing.T) {
	c := New("127.0.0.1")
	for _, start := range []uint32{0xFFFE, 0x1FFFE, math.MaxUint32 - 1} {
		c.pduRef = start
		if got := c.nextPDURef(); got != 0xFFFF {
			t.Errorf("after %#x: %#x, want 0xffff", start, got)
		}
		if got := c.nextPDURef(); got != 1 {
			t.Errorf("after %#x+1: %#x, want 1", start, got)
		}
	}
}

// A huge jitter setting neither panics (it once reached rand.Intn as n+1 <= 0)
// nor overflows the duration.
func TestRandomJitterBounds(t *testing.T) {
	for _, ms := range []int{-1, 0} {
		if d := randomJitter(ms); d != 0 {
			t.Errorf("randomJitter(%d) = %v", ms, d)
		}
	}
	for _, ms := range []int{1, 10, math.MaxInt} {
		limit := time.Duration(min(ms, maxJitterMS)) * time.Millisecond
		for range 50 {
			if d := randomJitter(ms); d < 0 || d > limit {
				t.Fatalf("randomJitter(%d) = %v, outside 0..%v", ms, d, limit)
			}
		}
	}
}

// An item carrying more bytes than requested is a protocol error, and the
// result keeps no byte of it: appending it would shift later chunks.
func TestReadAreaOverlongItem(t *testing.T) {
	port, cleanup := startFakeBitServer(t, func(pduRef uint16, _ []byte) []byte {
		// One successful item of 4 bytes for the 2 asked.
		data := []byte{wire.RetCodeSuccess, 0x04, 0x00, 32, 1, 2, 3, 4}
		resp := []byte{0x32, byte(wire.ROSCTRAckData), 0, 0, byte(pduRef >> 8), byte(pduRef), 0, 2, 0, byte(len(data)), 0, 0, wire.FuncReadVar, 1}
		return append(resp, data...)
	})
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	defer func() { _ = c.Close() }()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	res, err := c.ReadDB(ctx, 1, 0, 2)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if res.Status != ReadStatusProtocolErr || len(res.Data) != 0 || !errors.Is(res.Err(), ErrProtocolFailure) {
		t.Errorf("status %s, %d bytes, err %v", res.Status, len(res.Data), res.Err())
	}
}
