// SPDX-License-Identifier: MIT

package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestEncodeParseS7Header(t *testing.T) {
	h := must(EncodeS7Header(ROSCTRJob, 7, 12, 4))
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
	h := must(EncodeS7Header(ROSCTRJob, 0, 20, 10)) // claims 30 bytes payload
	h = append(h, make([]byte, 5)...)               // but only 5 bytes
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

// BenchmarkParseS7Header decodes the header of a recorded request and of a
// recorded answer (testdata/captures): a job has 10 header octets, an
// Ack-Data 12.
func BenchmarkParseS7Header(b *testing.B) {
	answer := recordedReadAnswer(b, 19)
	request := seedPDUs(b, 1, func(p recordedPDU) bool { return p.fromClient && p.header.ROSCTR.IsJob() })[0]
	for _, tc := range []struct {
		name string
		pdu  []byte
	}{{"job", request.pdu}, {"ack_data", answer.pdu}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			var h *S7Header
			var rest []byte
			for b.Loop() {
				var err error
				if h, rest, err = ParseS7Header(tc.pdu); err != nil {
					b.Fatal(err)
				}
			}
			if int(h.ParamLength)+int(h.DataLength) != len(rest) {
				b.Fatalf("sections of %d octets, %d after the header", int(h.ParamLength)+int(h.DataLength), len(rest))
			}
		})
	}
}

// FuzzParseS7Header: an accepted header holds the input's fields, what
// follows it is a view of the input that covers the declared sections, the
// header encodes back to the same octets, and one octet less than the
// header and its sections is refused. A refusal is one of the four
// documented errors and returns nothing.
func FuzzParseS7Header(f *testing.F) {
	f.Add([]byte{0x32, 0x01, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0x32, 0x03, 0, 0, 0, 1, 0, 2, 0, 1, 0, 0, FuncWriteVar, 1, RetCodeSuccess})
	types := map[ROSCTR]int{}
	for _, p := range seedPDUs(f, 12, func(p recordedPDU) bool { types[p.header.ROSCTR]++; return types[p.header.ROSCTR] <= 3 }) {
		f.Add(p.pdu)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		h, rest, err := ParseS7Header(data)
		if err != nil {
			if h != nil || rest != nil {
				t.Fatalf("a refused header returned %+v and %d octets", h, len(rest))
			}
			if !errors.Is(err, ErrShortS7Header) && !errors.Is(err, ErrInvalidS7ProtocolID) &&
				!errors.Is(err, ErrShortS7AckHeader) && !errors.Is(err, ErrS7PayloadLength) {
				t.Fatalf("undocumented error: %v", err)
			}
			return
		}
		headerLen := S7HeaderSize
		if h.ROSCTR.IsAck() || h.ROSCTR.IsAckData() {
			headerLen = AckDataHeaderSize
			if h.ErrorClass != data[10] || h.ErrorCode != data[11] {
				t.Fatalf("error class and code %#x %#x, input holds %#x %#x", h.ErrorClass, h.ErrorCode, data[10], data[11])
			}
		} else if h.ErrorClass != 0 || h.ErrorCode != 0 {
			t.Fatalf("error class and code set on a PDU of type %#x", byte(h.ROSCTR))
		}
		need := int(h.ParamLength) + int(h.DataLength)
		if off, ok := within(data, rest); !ok || (len(rest) > 0 && off != headerLen) ||
			len(rest) != len(data)-headerLen || cap(rest) != len(rest) || need > len(rest) {
			t.Fatalf("%d octets after a %d-octet header of a %d-octet input, sections of %d", len(rest), headerLen, len(data), need)
		}
		if h.ProtocolID != 0x32 || byte(h.ROSCTR) != data[1] ||
			h.RedundancyID != binary.BigEndian.Uint16(data[2:4]) || h.PDURef != binary.BigEndian.Uint16(data[4:6]) ||
			h.ParamLength != binary.BigEndian.Uint16(data[6:8]) || h.DataLength != binary.BigEndian.Uint16(data[8:10]) {
			t.Fatalf("header %+v does not hold the fields of % x", h, data[:10])
		}
		enc, err := EncodeS7Header(h.ROSCTR, h.PDURef, int(h.ParamLength), int(h.DataLength))
		if err != nil || !bytes.Equal(enc[:2], data[:2]) || !bytes.Equal(enc[4:], data[4:10]) {
			t.Fatalf("header encodes to % x (%v), input % x", enc, err, data[:10])
		}
		if _, _, err := ParseS7Header(data[:headerLen+need-1]); err == nil {
			t.Fatalf("accepted with one octet less than its header and sections (%d)", headerLen+need)
		}
	})
}
