// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// readVarAnswer builds a one-item Read Var answer carrying payload.
func readVarAnswer(pduRef uint16, payload []byte) []byte {
	data := make([]byte, 4, 4+len(payload)+1)
	data[0] = wire.RetCodeSuccess
	data[1] = 0x04
	binary.BigEndian.PutUint16(data[2:4], uint16(len(payload)*8))
	data = append(data, payload...)
	if len(data)%2 != 0 {
		data = append(data, 0)
	}
	h := make([]byte, 12)
	h[0] = 0x32
	h[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(h[4:6], pduRef)
	binary.BigEndian.PutUint16(h[6:8], 2)
	binary.BigEndian.PutUint16(h[8:10], uint16(len(data)))
	return append(append(h, wire.FuncReadVar, 1), data...)
}

// A chunk answered with fewer bytes than asked ends the read: Data stays a
// prefix of the requested range and no further chunk is requested.
func TestReadAreaStopsAfterShortChunk(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	port, cleanup := startFakeBitServer(t, func(pduRef uint16, req []byte) []byte {
		start := (int(req[21])<<16 | int(req[22])<<8 | int(req[23])) >> 3
		n := int(binary.BigEndian.Uint16(req[16:18]))
		mu.Lock()
		requests++
		if requests == 2 {
			n -= 10
		}
		mu.Unlock()
		p := make([]byte, n)
		for i := range p {
			p[i] = byte((start + i) / 7)
		}
		return readVarAnswer(pduRef, p)
	})
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	defer func() { _ = c.Close() }()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	const start = 1000
	res, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: start, Size: 1000})
	if err != nil {
		t.Fatalf("ReadArea: %v", err)
	}
	if res.Status != ReadStatusShortRead {
		t.Fatalf("status = %s, want short-read", res.Status)
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 2 {
		t.Fatalf("requests = %d, want 2 (stop after the short chunk)", got)
	}
	for i, b := range res.Data {
		if b != byte((start+i)/7) {
			t.Fatalf("Data[%d] = %d, want the value of address %d", i, b, start+i)
		}
	}
}

// silentAfterSetup completes COTP and S7 setup, then never answers.
func silentAfterSetup(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = raw.Close() }()
		srv := acceptFakeCOTP(t, raw)
		serveFakeSetup(t, srv, 240)
		<-release
	}()
	t.Cleanup(func() { close(release); _ = ln.Close(); <-done })
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return port
}

// Each probe is bounded by RangeProbeRequest.Timeout even when the caller's
// context has no deadline.
func TestProbeReadableRangesTimeoutBoundsProbe(t *testing.T) {
	port := silentAfterSetup(t)
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(time.Second))
	defer func() { _ = c.Close() }()
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	begin := time.Now()
	res, err := c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaDB, DBNumber: 1, Start: 0, End: 16, ProbeSize: 8, Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ProbeReadableRanges: %v", err)
	}
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("scan took %v, want it bounded by the probe timeout", d)
	}
	for _, p := range res.Probes {
		if p.Result.Status == ReadStatusSuccess {
			t.Fatalf("probe at %d succeeded against a silent peer", p.Offset)
		}
	}
}

// A setup answer delivered as a message longer than any S7 PDU is refused
// instead of being reassembled.
func TestConnectRefusesOversizedMessage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	msg := make([]byte, 1<<17)
	copy(msg, buildS7SetupResponse(1, 480))
	var wg sync.WaitGroup
	wg.Go(func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = raw.Close() }()
		srv := acceptFakeCOTP(t, raw)
		if _, err := srv.ReadTSDU(context.Background()); err != nil {
			return
		}
		var frames []byte
		const seg = 1000
		for off := 0; off < len(msg); off += seg {
			end := min(off+seg, len(msg))
			eot := byte(0)
			if end == len(msg) {
				eot = 0x80
			}
			n := 7 + end - off
			frames = append(frames, 0x03, 0x00, byte(n>>8), byte(n), 0x02, 0xF0, eot)
			frames = append(frames, msg[off:end]...)
		}
		_, _ = raw.Write(frames)
	})
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(5*time.Second))
	err = c.Connect(context.Background())
	_ = c.Close()
	wg.Wait()
	if err == nil {
		t.Fatal("Connect accepted a 128 KiB setup answer")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect timed out instead of refusing the message: %v", err)
	}
}
