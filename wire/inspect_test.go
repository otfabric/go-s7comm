package wire

import (
	"testing"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-tpkt"
)

func encodeInspectDTFrame(t *testing.T, s7 []byte) []byte {
	t.Helper()
	dt := &cotp.DT{EOT: true, UserData: s7}
	dtBytes, err := dt.MarshalBinary()
	if err != nil {
		t.Fatalf("DT.MarshalBinary: %v", err)
	}
	frame, err := tpkt.EncodePacket(dtBytes)
	if err != nil {
		t.Fatalf("tpkt.EncodePacket: %v", err)
	}
	return frame
}

func TestInspectFrameTPKTOnly(t *testing.T) {
	frame := encodeInspectDTFrame(t, nil)
	s, err := InspectFrame(frame)
	if err != nil {
		t.Fatalf("InspectFrame error: %v", err)
	}
	if s.COTPType != byte(cotp.TypeDT) {
		t.Fatalf("unexpected COTP type: 0x%02X", s.COTPType)
	}
	if s.ROSCTR != 0 {
		t.Fatalf("expected no S7 ROSCTR, got 0x%02X", s.ROSCTR)
	}
}

func TestInspectFrameWithS7(t *testing.T) {
	s7 := EncodeS7Header(ROSCTRJob, 1, 1, 0)
	s7 = append(s7, FuncReadVar)
	frame := encodeInspectDTFrame(t, s7)
	s, err := InspectFrame(frame)
	if err != nil {
		t.Fatalf("InspectFrame error: %v", err)
	}
	if s.ROSCTR != byte(ROSCTRJob) {
		t.Fatalf("unexpected ROSCTR: 0x%02X", s.ROSCTR)
	}
	if s.Function != FuncReadVar {
		t.Fatalf("unexpected function: 0x%02X", s.Function)
	}
}

func FuzzInspectFrame(f *testing.F) {
	dt := &cotp.DT{EOT: true, UserData: EncodeS7Header(ROSCTRJob, 1, 2, 0)}
	dtBytes, _ := dt.MarshalBinary()
	frame, _ := tpkt.EncodePacket(dtBytes)
	f.Add(frame)
	f.Fuzz(func(t *testing.T, frame []byte) {
		_, _ = InspectFrame(frame)
	})
}
