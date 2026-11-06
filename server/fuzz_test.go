// SPDX-License-Identifier: MIT

package server

import (
	"encoding/binary"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

// FuzzHandle feeds arbitrary PDUs to a session: it must not panic, a PDU
// whose header can be read must get an answer that parses as Ack-Data (or
// user data, for user data) with its PDU reference, and memory keeps its size.
func FuzzHandle(f *testing.F) {
	f.Add(must(wire.EncodeSetupCommRequest(1, 1, 1, 480)))
	f.Add(must(wire.EncodeReadVarRequest(2, []wire.S7AnyAddress{{Area: wire.AreaDB, DBNumber: 1, Start: 0, Size: 4}})))
	f.Add(must(wire.EncodeWriteVarRequest(3, wire.S7AnyAddress{Area: wire.AreaDB, DBNumber: 1, Start: 2, Size: 2}, []byte{9, 9})))
	f.Add(must(wire.EncodeWriteVarBitRequest(4, wire.S7AnyBitAddress{Area: wire.AreaMerkers, ByteOffset: 1, BitOffset: 3}, true)))
	f.Add(must(wire.EncodeReadVarBitRequest(5, wire.S7AnyBitAddress{Area: wire.AreaDB, DBNumber: 2, BitOffset: 7})))
	f.Add([]byte{0x32, 0x07, 0, 0, 0, 1, 0, 2, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, req []byte) {
		s := testSession(t, 240)
		out, keep := s.handle(req)
		if !keep {
			if out != nil {
				t.Fatal("answer on a dropped connection")
			}
			return
		}
		if out == nil {
			t.Fatal("readable PDU without an answer")
		}
		h, _, err := wire.ParseS7Header(out)
		if err != nil || h.PDURef != binary.BigEndian.Uint16(req[4:6]) ||
			(h.ROSCTR != wire.ROSCTRAckData && (h.ROSCTR != wire.ROSCTRUserdata || req[1] != byte(wire.ROSCTRUserdata))) {
			t.Fatalf("answer %x: %+v %v", out, h, err)
		}
		if len(out) > 4096 {
			t.Fatalf("answer of %d bytes", len(out))
		}
		if got, err := s.srv.Read(AreaDB, 3, 0, 600); err != nil || len(got) != 600 {
			t.Fatalf("DB3 lost its size: %v", err)
		}
	})
}
