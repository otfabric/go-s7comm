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
	s7 := EncodeS7Header(ROSCTRJob, 1, 1, 0)
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

func FuzzInspectTPDU(f *testing.F) {
	dt := &cotp.DT{EOT: true, UserData: EncodeS7Header(ROSCTRJob, 1, 2, 0)}
	tpdu, _ := dt.MarshalBinary()
	f.Add(tpdu)
	f.Fuzz(func(t *testing.T, tpdu []byte) {
		_, _ = InspectTPDU(tpdu)
	})
}
