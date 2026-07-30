// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"testing"
)

func TestEncodeParseS7Header(t *testing.T) {
	h := EncodeS7Header(ROSCTRJob, 7, 12, 4)
	h = append(h, make([]byte, 16)...)
	parsed, rest, err := ParseS7Header(h)
	if err != nil {
		t.Fatalf("ParseS7Header error: %v", err)
	}
	if parsed.PDURef != 7 {
		t.Fatalf("unexpected pdu ref: %d", parsed.PDURef)
	}
	if parsed.ParamLength != 12 || parsed.DataLength != 4 {
		t.Fatalf("unexpected lengths: %d/%d", parsed.ParamLength, parsed.DataLength)
	}
	if len(rest) != 16 {
		t.Fatalf("expected rest length 16, got %d", len(rest))
	}
}

func TestParseS7HeaderRejectsInvalidProtocol(t *testing.T) {
	_, _, err := ParseS7Header([]byte{0x31, 0x01, 0, 0, 0, 0, 0, 0, 0, 0})
	if err == nil {
		t.Fatal("expected invalid protocol error")
	}
}

func TestParseS7HeaderTooShort(t *testing.T) {
	_, _, err := ParseS7Header([]byte{0x32, 0x01, 0, 0})
	if err == nil {
		t.Fatal("expected too short error")
	}
}

func TestParseS7HeaderAck(t *testing.T) {
	// ROSCTRAck with error class/code
	h := make([]byte, 14)
	h[0] = 0x32
	h[1] = byte(ROSCTRAck)
	binary.BigEndian.PutUint16(h[6:8], 0)  // param len
	binary.BigEndian.PutUint16(h[8:10], 0) // data len
	h[10] = 0x00
	h[11] = 0x00
	parsed, _, err := ParseS7Header(h)
	if err != nil {
		t.Fatalf("ParseS7Header ack: %v", err)
	}
	if parsed.ROSCTR != ROSCTRAck || parsed.ErrorClass != 0 || parsed.ErrorCode != 0 {
		t.Fatalf("unexpected parsed ack: %+v", parsed)
	}
}

func TestParseS7HeaderAckTooShort(t *testing.T) {
	h := make([]byte, 11)
	h[0] = 0x32
	h[1] = byte(ROSCTRAck)
	_, _, err := ParseS7Header(h)
	if err == nil {
		t.Fatal("expected short ack header error")
	}
}

func TestParseS7HeaderPayloadLengthError(t *testing.T) {
	h := EncodeS7Header(ROSCTRJob, 0, 20, 10) // claims 30 bytes payload
	h = append(h, make([]byte, 5)...)         // but only 5 bytes
	_, _, err := ParseS7Header(h)
	if err == nil {
		t.Fatal("expected payload length error")
	}
}

func TestParseS7HeaderROSCTR_AckVsAckData(t *testing.T) {
	// ACK (no data) vs ACK_DATA (with param/data) are distinct
	hAck := make([]byte, 12)
	hAck[0], hAck[1] = 0x32, byte(ROSCTRAck)
	parsed, rest, err := ParseS7Header(hAck)
	if err != nil {
		t.Fatalf("ParseS7Header(ACK): %v", err)
	}
	if !parsed.ROSCTR.IsAck() || parsed.ROSCTR.IsAckData() {
		t.Errorf("expected ACK not ACK_DATA, got ROSCTR=0x%02X", parsed.ROSCTR)
	}
	if len(rest) != 0 {
		t.Errorf("ACK has no payload, rest len=%d", len(rest))
	}
	hAckData := make([]byte, 12+6) // 12-byte ack header + 6 bytes param+data
	hAckData[0], hAckData[1] = 0x32, byte(ROSCTRAckData)
	binary.BigEndian.PutUint16(hAckData[4:6], 1)
	binary.BigEndian.PutUint16(hAckData[6:8], 2)
	binary.BigEndian.PutUint16(hAckData[8:10], 4)
	hAckData[10], hAckData[11] = 0, 0
	parsed2, rest2, err := ParseS7Header(hAckData)
	if err != nil {
		t.Fatalf("ParseS7Header(ACK_DATA): %v", err)
	}
	if !parsed2.ROSCTR.IsAckData() || parsed2.ROSCTR.IsAck() {
		t.Errorf("expected ACK_DATA not ACK, got ROSCTR=0x%02X", parsed2.ROSCTR)
	}
	if len(rest2) != 6 {
		t.Errorf("expected rest 6 (param+data), got %d", len(rest2))
	}
}

func TestParseS7HeaderInvalidROSCTR(t *testing.T) {
	h := make([]byte, 10)
	h[0], h[1] = 0x32, 0xFF // invalid/unknown ROSCTR
	binary.BigEndian.PutUint16(h[6:8], 0)
	binary.BigEndian.PutUint16(h[8:10], 0)
	parsed, rest, err := ParseS7Header(h)
	if err != nil {
		t.Fatalf("ParseS7Header(0xFF): unexpected error %v", err)
	}
	// Parser accepts unknown ROSCTR; raw value preserved
	if parsed.ROSCTR != ROSCTR(0xFF) {
		t.Errorf("expected ROSCTR 0xFF preserved, got 0x%02X", parsed.ROSCTR)
	}
	if len(rest) != 0 {
		t.Errorf("expected rest 0 for param=0 data=0, got %d", len(rest))
	}
}

func TestFunctionCodeString(t *testing.T) {
	if got := FunctionCodeString(FuncReadVar); got != "Read Var" {
		t.Errorf("FunctionCodeString(ReadVar): got %q", got)
	}
	if got := FunctionCodeString(FuncUpload); got != "Upload" {
		t.Errorf("FunctionCodeString(Upload): got %q", got)
	}
	if got := FunctionCodeString(FuncSetupComm); got != "Setup communication" {
		t.Errorf("FunctionCodeString(SetupComm): got %q", got)
	}
	if got := FunctionCodeString(0x99); got != "0x99" {
		t.Errorf("FunctionCodeString(unknown): got %q", got)
	}
}

func BenchmarkParseS7Header(b *testing.B) {
	h := EncodeS7Header(ROSCTRJob, 7, 12, 4)
	h = append(h, make([]byte, 16)...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = ParseS7Header(h)
	}
}

func FuzzParseS7Header(f *testing.F) {
	f.Add([]byte{0x32, 0x01, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = ParseS7Header(data)
	})
}
