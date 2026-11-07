// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

// realCPUSession holds DBs 74 and 76, timers T0..T3 and counters C0..C1.
func realCPUSession(t *testing.T, pdu int) *session {
	t.Helper()
	srv, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	db74 := make([]byte, 120)
	copy(db74[108:], []byte{0x04, 0x0a, 0x0b})
	db76 := make([]byte, 410)
	copy(db76[404:], []byte{0x05, 0x44, 0x05, 0x3e})
	for _, err := range []error{
		srv.RegisterDB(74, db74, AreaOptions{}),
		srv.RegisterDB(76, db76, AreaOptions{}),
		srv.RegisterArea(AreaTimers, []byte{0x25, 0x85, 0x20, 0x00, 0x00, 0x11, 0x12, 0x34}, AreaOptions{}),
		srv.RegisterArea(AreaCounters, []byte{0x00, 0x17, 0x00, 0x11}, AreaOptions{}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return &session{srv: srv, pduSize: pdu, log: slog.New(slog.DiscardHandler)}
}

// dbreadItem builds a DBREAD item (syntax 0xB0) of ranges {length, DB, start}.
func dbreadItem(ranges ...[3]int) []byte {
	b := []byte{0x12, byte(2 + 5*len(ranges)), wire.SyntaxIDDBRead, byte(len(ranges))}
	for _, r := range ranges {
		b = append(b, byte(r[0]), byte(r[1]>>8), byte(r[1]), byte(r[2]>>8), byte(r[2]))
	}
	return b
}

// DBREAD as WinCC sends it to an S7-400 and the S7-400 answers it
// (testdata/captures/gymgit/wincc-advanced_s7-400_production.pcapng): per
// range the return code 0xFF and the bytes, no padding between ranges.
func TestDBRead(t *testing.T) {
	s := realCPUSession(t, 480)
	_, data := answer(t, s, readJob(dbreadItem([3]int{2, 74, 108}, [3]int{4, 76, 404}, [3]int{3, 74, 108})))
	want := []byte{0xFF, 0x09, 0x00, 0x0C, 0xFF, 0x04, 0x0a, 0xFF, 0x05, 0x44, 0x05, 0x3e, 0xFF, 0x04, 0x0a, 0x0b}
	if !bytes.Equal(data, want) {
		t.Fatalf("data % x\nwant % x", data, want)
	}
	// A DBREAD item next to an S7ANY item: an odd-length item is padded when another follows.
	_, data = answer(t, s, readJob(dbreadItem([3]int{2, 74, 108}), any12(wire.TransportSizeByte, 1, 74, wire.AreaDB, 108*8)))
	if want := []byte{0xFF, 0x09, 0x00, 0x03, 0xFF, 0x04, 0x0a, 0x00, 0xFF, 0x04, 0x00, 0x08, 0x04}; !bytes.Equal(data, want) {
		t.Fatalf("mixed % x", data)
	}
	for name, tc := range map[string]struct {
		item []byte
		code byte
	}{
		"DB not held":  {dbreadItem([3]int{2, 74, 0}, [3]int{2, 99, 0}), wire.RetCodeNotAvailable},
		"DB 0":         {dbreadItem([3]int{2, 0, 0}), wire.RetCodeNotAvailable},
		"past the end": {dbreadItem([3]int{20, 74, 110}), wire.RetCodeAddressFault},
		"no bytes":     {dbreadItem([3]int{0, 74, 0}), wire.RetCodeAddressFault},
	} {
		_, data := answer(t, s, readJob(tc.item))
		if !bytes.Equal(data, []byte{tc.code, 0, 0, 0}) {
			t.Errorf("%s: % x, want code 0x%02x", name, data, tc.code)
		}
	}
	// DBREAD only reads: in a Write Var job it is not an address.
	_, data = answer(t, s, writeJob(dbreadItem([3]int{2, 74, 108}), dataItem(wire.DataTransportSizeByte, 16, 1, 2)))
	if !bytes.Equal(data, []byte{wire.RetCodeAddressFault}) {
		t.Errorf("DBREAD write: % x", data)
	}
	// An answer past the PDU refuses the whole job.
	small := realCPUSession(t, 240)
	big := readJob(dbreadItem([3]int{120, 74, 0}, [3]int{120, 76, 0}))
	if class, code := headerError(t, small, big); class != errClassSupplies || code != errCodeFrames {
		t.Errorf("past the PDU: %02x%02x", class, code)
	}
}

// Timers and counters as a CPU 315-2 PN/DP answers them to WinCC and Snap7
// (testdata/captures): addressed by number with their own transport size,
// read as OCTET STRING of two bytes each; writes refused with 0x03.
func TestTimersAndCounters(t *testing.T) {
	s := realCPUSession(t, 480)
	_, data := answer(t, s, readJob(
		any12(transportTimer, 1, 0, wire.AreaTimer, 0),
		any12(transportTimer, 2, 0, wire.AreaTimer, 2),
		any12(transportCounter, 1, 0, wire.AreaCounter, 1)))
	want := []byte{0xFF, 0x09, 0x00, 0x02, 0x25, 0x85, 0xFF, 0x09, 0x00, 0x04, 0x00, 0x11, 0x12, 0x34, 0xFF, 0x09, 0x00, 0x02, 0x00, 0x11}
	if !bytes.Equal(data, want) {
		t.Fatalf("data % x\nwant % x", data, want)
	}
	for name, item := range map[string][]byte{
		"BYTE on timers":    any12(wire.TransportSizeByte, 1, 0, wire.AreaTimer, 0),
		"TIMER on markers":  any12(transportTimer, 1, 0, wire.AreaMerkers, 0),
		"COUNTER on timers": any12(transportCounter, 1, 0, wire.AreaTimer, 0),
		"past the end":      any12(transportTimer, 2, 0, wire.AreaTimer, 3),
		"zero count":        any12(transportCounter, 0, 0, wire.AreaCounter, 0),
	} {
		s := realCPUSession(t, 480)
		_ = s.srv.RegisterArea(AreaMerkers, make([]byte, 4), AreaOptions{})
		if _, data := answer(t, s, readJob(item)); !bytes.Equal(data, []byte{wire.RetCodeAddressFault, 0, 0, 0}) {
			t.Errorf("%s: % x", name, data)
		}
	}
	// The recorded CPU refuses writes to existing timers and counters.
	_, data = answer(t, s, writeJob(any12(transportTimer, 1, 0, wire.AreaTimer, 0), dataItem(wire.DataTransportSizeOctet, 2, 0x11, 0x11),
		any12(transportCounter, 1, 0, wire.AreaCounter, 0), dataItem(wire.DataTransportSizeOctet, 2, 0x22, 0x22)))
	if !bytes.Equal(data, []byte{wire.RetCodeAccessFault, wire.RetCodeAccessFault}) {
		t.Errorf("writes: % x", data)
	}
	// The host still sets them; element n at byte 2n.
	if err := s.srv.Write(AreaTimers, 0, 2, []byte{0x30, 0x01}); err != nil {
		t.Fatal(err)
	}
	if _, data = answer(t, s, readJob(any12(transportTimer, 1, 0, wire.AreaTimer, 1))); !bytes.Equal(data[4:], []byte{0x30, 0x01}) {
		t.Errorf("host write not seen: % x", data)
	}
	if AreaTimers.String() != "T" || AreaCounters.String() != "C" {
		t.Errorf("names %s %s", AreaTimers, AreaCounters)
	}
}
