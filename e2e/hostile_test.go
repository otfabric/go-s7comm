// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/wire"
)

func (p *plc) setRewrite(f func([]byte) []byte) { p.rewrite.Store(&f) }

// A PLC that returns more bytes than an item asked for must not shift the
// following chunks: the client would hand back data at the wrong addresses.
func TestOverlongReadItemIsAProtocolError(t *testing.T) {
	p := startPLC(t, 240)
	c := connect(t, p)
	p.setRewrite(func(out []byte) []byte {
		// One Read Var item: grow its payload by two bytes and fix the lengths.
		if len(out) < 16 || out[12] != wire.FuncReadVar || out[14] != wire.RetCodeSuccess {
			return out
		}
		bits := binary.BigEndian.Uint16(out[16:18])
		binary.BigEndian.PutUint16(out[16:18], bits+16)
		binary.BigEndian.PutUint16(out[8:10], binary.BigEndian.Uint16(out[8:10])+2)
		return append(out, 0xEE, 0xEE)
	})
	// 500 bytes need several chunks at PDU 240.
	res, err := c.ReadDB(context.Background(), 1, 0, 500)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if res.Status != client.ReadStatusProtocolErr {
		t.Fatalf("status = %s (%d bytes), want protocol-error", res.Status, len(res.Data))
	}
}

// mutate corrupts one S7 answer the way a broken or hostile peer would.
func mutate(rng *rand.Rand, out []byte) []byte {
	out = append([]byte(nil), out...)
	switch rng.IntN(8) {
	case 0: // flip a byte past the PDU reference, so most answers still correlate
		if len(out) > 6 {
			out[6+rng.IntN(len(out)-6)] ^= byte(1 + rng.IntN(255))
		}
	case 1: // cut short
		out = out[:rng.IntN(len(out)+1)]
	case 2: // trailing garbage
		for range 1 + rng.IntN(64) {
			out = append(out, byte(rng.IntN(256)))
		}
	case 3: // parameter length lies
		if len(out) >= 8 {
			binary.BigEndian.PutUint16(out[6:8], uint16(rng.IntN(1<<16)))
		}
	case 4: // data length lies
		if len(out) >= 10 {
			binary.BigEndian.PutUint16(out[8:10], uint16(rng.IntN(1<<16)))
		}
	case 5: // item count lies
		if len(out) > 13 {
			out[13] = byte(rng.IntN(256))
		}
	case 6: // an item length field lies
		if len(out) >= 18 {
			binary.BigEndian.PutUint16(out[16:18], uint16(rng.IntN(1<<16)))
		}
	case 7: // a successful item carries more bytes than asked, lengths consistent
		if len(out) >= 18 && out[14] == wire.RetCodeSuccess && out[15] != wire.DataTransportSizeBit {
			n := 1 + rng.IntN(8)
			binary.BigEndian.PutUint16(out[16:18], binary.BigEndian.Uint16(out[16:18])+uint16(n*8))
			binary.BigEndian.PutUint16(out[8:10], binary.BigEndian.Uint16(out[8:10])+uint16(n))
			out = append(out, make([]byte, n)...)
		}
	}
	return out
}

// Every answer to a job is corrupted. Each call must come back within its
// deadline with an error or a result no longer than asked for, never panic,
// and the client must stay closeable; goleak (TestMain) checks no goroutine
// is left behind.
func TestHostilePLCAnswers(t *testing.T) {
	p := startPLC(t, 240)
	c := connect(t, p)
	var mu sync.Mutex
	rng := rand.New(rand.NewPCG(1, 2)) // deterministic: a failure replays
	p.setRewrite(func(out []byte) []byte {
		mu.Lock()
		defer mu.Unlock()
		return mutate(rng, out)
	})

	ops := rand.New(rand.NewPCG(3, 4))
	for i := range 400 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		start := time.Now()
		var err error
		switch ops.IntN(6) {
		case 0, 1:
			size := 1 + ops.IntN(600)
			var res *client.ReadResult
			res, err = c.ReadDB(ctx, 1, ops.IntN(400), size)
			if err == nil && len(res.Data) > size {
				t.Fatalf("op %d: ReadDB returned %d bytes for %d asked", i, len(res.Data), size)
			}
		case 2:
			err = c.WriteDB(ctx, 1, ops.IntN(400), make([]byte, 1+ops.IntN(300)))
		case 3:
			_, err = c.ReadDBBit(ctx, 1, ops.IntN(100), ops.IntN(8))
		case 4:
			err = c.WriteDBBit(ctx, 1, ops.IntN(100), ops.IntN(8), ops.IntN(2) == 1)
		case 5:
			_, err = c.GetCPUState(ctx)
		}
		cancel()
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("op %d took %v", i, d)
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			// A corrupted stream may leave the connection unusable: reconnect.
			_ = c.Close()
			if cerr := c.Connect(context.Background()); cerr != nil {
				t.Fatalf("op %d: reconnect: %v", i, cerr)
			}
		}
	}
}
