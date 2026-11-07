// SPDX-License-Identifier: MIT

package server_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/server"
	"github.com/otfabric/go-s7comm/wire"
)

func rawJob(t *testing.T, ref uint16, param []byte) []byte {
	t.Helper()
	h, err := wire.EncodeS7Header(wire.ROSCTRJob, ref, len(param), 0)
	if err != nil {
		t.Fatal(err)
	}
	return append(h, param...)
}

// Over TCP and COTP: the server grants two parallel jobs, a client sends a
// DBREAD job and a timer read before reading any answer, and gets both
// answers in order with their PDU references.
func TestPipelinedDBReadAndTimerJobs(t *testing.T) {
	srv := newServer(t, server.Config{})
	must(t, srv.RegisterDB(74, append(make([]byte, 108), 0x04, 0x0a), server.AreaOptions{}))
	must(t, srv.RegisterArea(server.AreaTimers, []byte{0x25, 0x85}, server.AreaOptions{}))
	conn := openCOTP(t, start(t, srv))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	setup, err := wire.EncodeSetupCommRequest(1, 1, 3, 480)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteTSDU(ctx, setup); err != nil {
		t.Fatal(err)
	}
	out, err := conn.ReadTSDU(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h, rest, err := wire.ParseS7Header(out)
	if err != nil {
		t.Fatal(err)
	}
	granted, err := wire.ParseSetupCommResponse(rest[:h.ParamLength])
	if err != nil || granted.MaxAmqCalling != 1 || granted.MaxAmqCalled != 2 {
		t.Fatalf("setup %+v, %v; want AmQ 1/2 as a CPU 315-2 PN/DP grants WinCC", granted, err)
	}

	dbread := []byte{wire.FuncReadVar, 1, 0x12, 0x07, wire.SyntaxIDDBRead, 1, 2, 0, 74, 0, 108}
	timer := []byte{wire.FuncReadVar, 1, 0x12, 0x0A, wire.SyntaxIDS7Any, 0x1D, 0, 1, 0, 0, wire.AreaTimer, 0, 0, 0}
	for ref, param := range [][]byte{dbread, timer} {
		if err := conn.WriteTSDU(ctx, rawJob(t, uint16(10+ref), param)); err != nil {
			t.Fatal(err)
		}
	}
	for ref, want := range [][]byte{{0xFF, 0x09, 0x00, 0x03, 0xFF, 0x04, 0x0a}, {0xFF, 0x09, 0x00, 0x02, 0x25, 0x85}} {
		out, err := conn.ReadTSDU(ctx)
		if err != nil {
			t.Fatalf("answer %d: %v", ref, err)
		}
		h, rest, err := wire.ParseS7Header(out)
		if err != nil || h.PDURef != uint16(10+ref) || !bytes.Equal(rest[h.ParamLength:], want) {
			t.Fatalf("answer %d: % x (%+v, %v), want data % x", ref, out, h, err, want)
		}
	}
}
