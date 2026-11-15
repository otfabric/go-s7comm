// SPDX-License-Identifier: MIT

package wire

import (
	"testing"

	"github.com/otfabric/go-cotp"
)

func encodeInspectDT(t *testing.T, s7 []byte) []byte {
	t.Helper()
	dt := &cotp.DT{EOT: true, UserData: s7}
	dtBytes, err := dt.MarshalBinary()
	if err != nil {
		t.Fatalf("DT.MarshalBinary: %v", err)
	}
	return dtBytes
}

func TestInspectTPDUEmptyDT(t *testing.T) {
	tpdu := encodeInspectDT(t, nil)
	s, err := InspectTPDU(tpdu)
	if err != nil {
		t.Fatalf("InspectTPDU error: %v", err)
	}
	if s.COTPType != byte(cotp.TypeDT) {
		t.Fatalf("unexpected COTP type: 0x%02X", s.COTPType)
	}
	if s.ROSCTR != 0 {
		t.Fatalf("expected no S7 ROSCTR, got 0x%02X", s.ROSCTR)
	}
}

func TestInspectTPDUWithS7(t *testing.T) {
	s7 := must(EncodeS7Header(ROSCTRJob, 1, 1, 0))
	s7 = append(s7, FuncReadVar)
	tpdu := encodeInspectDT(t, s7)
	s, err := InspectTPDU(tpdu)
	if err != nil {
		t.Fatalf("InspectTPDU error: %v", err)
	}
	if s.ROSCTR != byte(ROSCTRJob) {
		t.Fatalf("unexpected ROSCTR: 0x%02X", s.ROSCTR)
	}
	if s.Function != FuncReadVar {
		t.Fatalf("unexpected function: 0x%02X", s.Function)
	}
}

// FuzzInspectTPDU: the summary is refused exactly when the TPDU does not
// decode, or is a DT whose user data starts as an S7 PDU that
// ParseS7Header refuses; an accepted one holds the TPDU's length and type
// and, for an S7 PDU, the fields of its header.
func FuzzInspectTPDU(f *testing.F) {
	dt := &cotp.DT{EOT: true, UserData: must(EncodeS7Header(ROSCTRJob, 1, 2, 0))}
	tpdu, _ := dt.MarshalBinary()
	f.Add(tpdu)
	f.Add([]byte{0x02, 0xf0, 0x80, 0x32})
	f.Add([]byte{0x06, 0xd0, 0x00, 0x01, 0x00, 0x02, 0x00})
	types := map[ROSCTR]int{}
	for _, p := range seedPDUs(f, 8, func(p recordedPDU) bool {
		types[p.header.ROSCTR]++
		return types[p.header.ROSCTR] <= 2 && len(p.pdu) < 900
	}) {
		if tpdu, err := (&cotp.DT{EOT: true, UserData: p.pdu}).MarshalBinary(); err == nil {
			f.Add(tpdu)
		}
	}
	f.Fuzz(func(t *testing.T, tpdu []byte) {
		s, err := InspectTPDU(tpdu)
		dec, decErr := cotp.Decode(tpdu)
		var payload []byte
		if decErr == nil && dec.DT != nil {
			payload = dec.DT.UserData
		}
		isS7 := len(payload) > 0 && payload[0] == 0x32
		var h *S7Header
		var rest []byte
		var s7Err error
		if isS7 {
			h, rest, s7Err = ParseS7Header(payload)
		}
		if wantErr := decErr != nil || s7Err != nil; (err != nil) != wantErr {
			t.Fatalf("InspectTPDU = %v; the TPDU decodes with %v, its S7 header with %v", err, decErr, s7Err)
		}
		if err != nil {
			if s != nil {
				t.Fatalf("a refused TPDU returned %+v", s)
			}
			return
		}
		if s.TPDULength != len(tpdu) || s.COTPType != byte(dec.Type) {
			t.Fatalf("summary %+v of a %d-octet TPDU of type %#x", s, len(tpdu), byte(dec.Type))
		}
		if !isS7 {
			if *s != (FrameSummary{TPDULength: len(tpdu), COTPType: byte(dec.Type)}) {
				t.Fatalf("S7 fields set without an S7 PDU: %+v", s)
			}
			return
		}
		var function byte
		if len(rest) > 0 {
			function = rest[0]
		}
		if s.ROSCTR != byte(h.ROSCTR) || s.ParamLength != int(h.ParamLength) || s.DataLength != int(h.DataLength) ||
			s.ErrorClass != h.ErrorClass || s.ErrorCode != h.ErrorCode || s.Function != function {
			t.Fatalf("summary %+v, header %+v", s, h)
		}
		if s.ParamLength+s.DataLength > len(tpdu) {
			t.Fatalf("sections of %d octets in a TPDU of %d", s.ParamLength+s.DataLength, len(tpdu))
		}
	})
}
