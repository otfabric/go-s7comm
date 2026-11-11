// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/otfabric/go-s7comm/internal/capture"
	"github.com/otfabric/go-s7comm/wire"
)

// The server answers every Setup Communication, Read Var and Write Var job
// recorded in testdata/captures as the real S7-300/S7-400 CPU did, byte
// for byte: S7ANY items on DBs, inputs, outputs, markers, timers and
// counters, the S7-400's DBREAD items, the refused timer and counter
// writes (0x03), the S7-1200's refused write to a write-protected DB
// (0x03), and the parallel jobs (AmQ) granted. The memory is seeded
// with what the CPU answered before each read.

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
	case wire.AreaInputs, wire.AreaOutputs, wire.AreaMerkers, wire.AreaDB, wire.AreaTimer, wire.AreaCounter:
		return s.Syntax == wire.SyntaxIDS7Any
	}
	return false
}

// span is the byte range an S7ANY item addresses in the server's memory:
// timers and counters by element number, two bytes each.
func span(s wire.VarSpec) (start, size int) {
	if s.Area == wire.AreaTimer || s.Area == wire.AreaCounter {
		return 2 * int(s.StartBit), 2 * int(s.Count)
	}
	return int(s.StartBit / 8), int(s.Count) * elemBytes(s.TransportSize)
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
// successfully, as large as the furthest byte addressed, and every DB whose
// write the CPU refused with 0x03 as a read-only DB.
func replayServer(t *testing.T, xs []exchange) *session {
	t.Helper()
	type key struct {
		area byte
		db   uint16
	}
	size := map[key]int{}
	readOnly := map[key]bool{}
	pdu := MinPDUSize
	for _, x := range xs {
		p, _ := sections(x.r)
		if len(p) >= 8 && p[0] == wire.FuncSetupComm {
			pdu = max(pdu, int(binary.BigEndian.Uint16(p[6:8])))
			continue
		}
		codes := itemCodes(x.r)
		var ranges [][]wire.DBReadArea
		if q, _ := sections(x.q); len(q) > 0 && q[0] == wire.FuncReadVar {
			ranges, _ = wire.ParseReadVarDBRead(q)
		}
		for i, s := range specsOf(x.q) {
			if i < len(codes) && codes[i] == wire.RetCodeAccessFault && s.Area == wire.AreaDB && s.Syntax == wire.SyntaxIDS7Any {
				k := key{wire.AreaDB, s.DBNumber}
				start, n := span(s)
				size[k], readOnly[k] = max(size[k], start+n), true
				continue
			}
			if i >= len(codes) || codes[i] != wire.RetCodeSuccess {
				continue
			}
			if i < len(ranges) {
				for _, r := range ranges[i] {
					k := key{wire.AreaDB, r.DBNumber}
					size[k] = max(size[k], int(r.Start)+int(r.Length))
				}
			}
			if held(s) {
				k := key{s.Area, 0}
				if s.Area == wire.AreaDB {
					k.db = s.DBNumber
				}
				start, n := span(s)
				size[k] = max(size[k], start+n)
			}
		}
	}
	srv, err := New(Config{MaxPDUSize: pdu})
	if err != nil {
		t.Fatal(err)
	}
	for k, n := range size {
		if k.area == wire.AreaDB {
			err = srv.RegisterDB(int(k.db), make([]byte, n), AreaOptions{ReadOnly: readOnly[k]})
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
	q, _ := sections(x.q)
	ranges, _ := wire.ParseReadVarDBRead(q)
	for i, s := range specsOf(x.q) {
		if i >= len(items) || items[i].ReturnCode != wire.RetCodeSuccess {
			continue
		}
		if i < len(ranges) && ranges[i] != nil { // per range: return code, bytes
			d := items[i].Data
			for _, r := range ranges[i] {
				if err := srv.Write(AreaDB, int(r.DBNumber), int(r.Start), d[1:1+int(r.Length)]); err != nil {
					t.Fatal(err)
				}
				d = d[1+int(r.Length):]
			}
			continue
		}
		if !held(s) {
			continue
		}
		start, _ := span(s)
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

func TestCaptureReplayMatchesRealCPUs(t *testing.T) {
	files, err := filepath.Glob("../testdata/captures/*/*.pcap*")
	if err != nil || len(files) != 26 {
		t.Fatalf("captures: %d, %v", len(files), err)
	}
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
				if !bytes.Equal(ours, x.r) {
					t.Errorf("%s conversation %d: job % x\n cpu  % x\n ours % x", filepath.Base(f), ci, x.q, x.r, ours)
					continue
				}
				byKind[p[0]]++
			}
		}
	}
	t.Logf("identical answers: setup %d, read %d, write %d", byKind[wire.FuncSetupComm], byKind[wire.FuncReadVar], byKind[wire.FuncWriteVar])
	if byKind[wire.FuncSetupComm] != 27 || byKind[wire.FuncReadVar] != 3411 || byKind[wire.FuncWriteVar] != 2082 {
		t.Fatalf("identical answers %v, want setup 27, read 3411, write 2082", byKind)
	}
}
