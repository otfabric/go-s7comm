// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/otfabric/go-s7comm/internal/capture"
	"github.com/otfabric/go-s7comm/wire"
)

// The server answers every Setup Communication, Read Var and Write Var job
// recorded in testdata/captures as the real S7-300/S7-400 CPU did, byte
// for byte, wherever it holds the addressed memory. The memory is seeded
// with what the CPU answered before each read. Three differences remain
// and are pinned here:
//   - setup: the CPUs grant up to two parallel jobs (AmQ), the server one;
//   - timer and counter areas (0x1C, 0x1D): the CPU holds them, the server
//     answers 0x0A for a read and a write (the CPU refuses the write: 0x03);
//   - DBREAD items (syntax 0xB0, S7-400): the server answers 0x05.

func elemBytes(ts byte) int {
	switch ts {
	case wire.TransportSizeBit, wire.TransportSizeByte, 0x03:
		return 1
	case 0x04, 0x05, 0x09, 0x0C, 0x1C, 0x1D:
		return 2
	case 0x06, 0x07, 0x08, 0x0A, 0x0B:
		return 4
	case 0x0F:
		return 8
	}
	return 0
}

func held(s wire.VarSpec) bool {
	switch s.Area {
	case wire.AreaInputs, wire.AreaOutputs, wire.AreaMerkers, wire.AreaDB:
		return s.Syntax == wire.SyntaxIDS7Any
	}
	return false
}

type exchange struct{ q, r []byte }

func exchanges(c capture.Conversation) []exchange {
	open := map[uint16][]byte{}
	var out []exchange
	for _, p := range c.PDUs {
		h, _, err := wire.ParseS7Header(p.Data)
		if err != nil {
			continue
		}
		if p.FromClient && h.ROSCTR.IsJob() {
			open[h.PDURef] = p.Data
		} else if !p.FromClient && h.ROSCTR.IsAckData() {
			if q, ok := open[h.PDURef]; ok {
				out = append(out, exchange{q, p.Data})
				delete(open, h.PDURef)
			}
		}
	}
	return out
}

func sections(pdu []byte) ([]byte, []byte) {
	h, rest, err := wire.ParseS7Header(pdu)
	if err != nil {
		return nil, nil
	}
	return rest[:h.ParamLength], rest[h.ParamLength : h.ParamLength+h.DataLength]
}

// specsOf returns the item specifications of a Read Var or Write Var job.
func specsOf(job []byte) []wire.VarSpec {
	p, d := sections(job)
	if len(p) == 0 {
		return nil
	}
	if p[0] == wire.FuncReadVar {
		s, _ := wire.ParseReadVarRequest(p)
		return s
	}
	s, _, _ := wire.ParseWriteVarRequest(p, d)
	return s
}

// itemCodes returns the item return codes of a Read Var or Write Var answer.
func itemCodes(resp []byte) []byte {
	p, d := sections(resp)
	if len(p) == 0 {
		return nil
	}
	if p[0] == wire.FuncWriteVar {
		c, _ := wire.ParseWriteVarResponseCodes(p, d)
		return c
	}
	items, _ := wire.ParseReadVarResponse(p, d)
	out := make([]byte, len(items))
	for i, it := range items {
		out[i] = it.ReturnCode
	}
	return out
}

// replayServer builds a server holding every area the CPU answered
// successfully, as large as the furthest byte addressed.
func replayServer(t *testing.T, xs []exchange) *session {
	t.Helper()
	type key struct {
		area byte
		db   uint16
	}
	size := map[key]int{}
	pdu := MinPDUSize
	for _, x := range xs {
		p, _ := sections(x.r)
		if len(p) >= 8 && p[0] == wire.FuncSetupComm {
			pdu = max(pdu, int(binary.BigEndian.Uint16(p[6:8])))
			continue
		}
		codes := itemCodes(x.r)
		for i, s := range specsOf(x.q) {
			if i < len(codes) && codes[i] == wire.RetCodeSuccess && held(s) {
				k := key{s.Area, 0}
				if s.Area == wire.AreaDB {
					k.db = s.DBNumber
				}
				size[k] = max(size[k], int(s.StartBit/8)+int(s.Count)*elemBytes(s.TransportSize))
			}
		}
	}
	srv, err := New(Config{MaxPDUSize: pdu})
	if err != nil {
		t.Fatal(err)
	}
	for k, n := range size {
		if k.area == wire.AreaDB {
			err = srv.RegisterDB(int(k.db), make([]byte, n), AreaOptions{})
		} else {
			err = srv.RegisterArea(Area(k.area), make([]byte, n), AreaOptions{})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return &session{srv: srv, pduSize: srv.cfg.MaxPDUSize, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// seed writes what the CPU answered for a read into the server's memory.
func seed(t *testing.T, srv *Server, x exchange) {
	t.Helper()
	p, d := sections(x.r)
	items, err := wire.ParseReadVarResponse(p, d)
	if err != nil {
		return
	}
	for i, s := range specsOf(x.q) {
		if i >= len(items) || items[i].ReturnCode != wire.RetCodeSuccess || !held(s) {
			continue
		}
		start := int(s.StartBit / 8)
		data := items[i].Data
		if s.TransportSize == wire.TransportSizeBit {
			cur, err := srv.Read(Area(s.Area), int(s.DBNumber), start, 1)
			if err != nil {
				t.Fatal(err)
			}
			mask := byte(1) << (s.StartBit % 8)
			b := cur[0] &^ mask
			if data[0]&1 != 0 {
				b |= mask
			}
			data = []byte{b}
		}
		if err := srv.Write(Area(s.Area), int(s.DBNumber), start, data); err != nil {
			t.Fatal(err)
		}
	}
}

type replayTally struct{ same, amq, timers, dbread int }

// classify explains why the server's answer differs from the CPU's, or
// fails the test.
func classify(t *testing.T, where string, x exchange, ours []byte, tally *replayTally) {
	t.Helper()
	p, _ := sections(x.q)
	if p[0] == wire.FuncSetupComm {
		cpu, srv := append([]byte(nil), x.r...), append([]byte(nil), ours...)
		copy(cpu[14:18], srv[14:18]) // max AmQ calling, called
		if !bytes.Equal(cpu, srv) {
			t.Errorf("%s: setup\n cpu % x\n ours % x", where, x.r, ours)
		}
		tally.amq++
		return
	}
	cpuCodes, ourCodes := itemCodes(x.r), itemCodes(ours)
	specs := specsOf(x.q)
	if len(cpuCodes) != len(specs) || len(ourCodes) != len(specs) {
		t.Errorf("%s: %d/%d codes for %d items\n cpu % x\n ours % x", where, len(cpuCodes), len(ourCodes), len(specs), x.r, ours)
		return
	}
	kind := ""
	for i, s := range specs {
		switch {
		case s.Syntax == 0xB0: // DBREAD
			kind = "dbread"
			if ourCodes[i] != wire.RetCodeAddressFault {
				t.Errorf("%s item %d: DBREAD answered 0x%02x", where, i, ourCodes[i])
			}
		case s.Area == 0x1C || s.Area == 0x1D: // counters, timers
			kind = "timers"
			if ourCodes[i] != wire.RetCodeNotAvailable {
				t.Errorf("%s item %d: timer/counter answered 0x%02x", where, i, ourCodes[i])
			}
		case cpuCodes[i] != ourCodes[i]:
			t.Errorf("%s item %d (area 0x%02x): cpu 0x%02x, ours 0x%02x", where, i, s.Area, cpuCodes[i], ourCodes[i])
		}
	}
	switch kind {
	case "dbread":
		tally.dbread++
	case "timers":
		tally.timers++
	default:
		t.Errorf("%s: answers differ in items the server holds\n cpu % x\n ours % x", where, x.r, ours)
	}
}

func TestCaptureReplayMatchesRealCPUs(t *testing.T) {
	files, err := filepath.Glob("../testdata/captures/*/*.pcap*")
	if err != nil || len(files) != 25 {
		t.Fatalf("captures: %d, %v", len(files), err)
	}
	var tally replayTally
	byKind := map[byte]int{}
	for _, f := range files {
		convs, err := capture.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for ci, c := range convs {
			xs := exchanges(c)
			sess := replayServer(t, xs)
			for _, x := range xs {
				p, _ := sections(x.q)
				if len(p) == 0 || (p[0] != wire.FuncSetupComm && p[0] != wire.FuncReadVar && p[0] != wire.FuncWriteVar) {
					continue
				}
				if p[0] == wire.FuncReadVar {
					seed(t, sess.srv, x)
				}
				ours, keep := sess.handle(x.q)
				if !keep {
					t.Fatalf("%s: dropped a job the CPU answered", f)
				}
				if bytes.Equal(ours, x.r) {
					tally.same++
					byKind[p[0]]++
					continue
				}
				classify(t, fmt.Sprintf("%s conversation %d", filepath.Base(f), ci), x, ours, &tally)
			}
		}
	}
	t.Logf("identical: %d (setup %d, read %d, write %d); differ: AmQ %d, timers/counters %d, DBREAD %d",
		tally.same, byKind[wire.FuncSetupComm], byKind[wire.FuncReadVar], byKind[wire.FuncWriteVar], tally.amq, tally.timers, tally.dbread)
	want := replayTally{same: 22 + 3241 + 2080, amq: 4, timers: 87, dbread: 84}
	if tally != want || byKind[wire.FuncSetupComm] != 22 || byKind[wire.FuncReadVar] != 3241 || byKind[wire.FuncWriteVar] != 2080 {
		t.Fatalf("tally %+v (%v), want %+v", tally, byKind, want)
	}
}
