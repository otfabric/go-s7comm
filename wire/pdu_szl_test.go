// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"testing"
)

func TestEncodeSZLRequest(t *testing.T) {
	got := EncodeSZLRequest(1, SZLModuleID, 0)
	if len(got) < 10+4+8 {
		t.Fatalf("expected header+param+data, got %d bytes", len(got))
	}
	if got[0] != 0x32 || got[1] != byte(ROSCTRUserdata) {
		t.Fatalf("expected S7 userdata header")
	}
	// Data block starts after header (10) + param (variable)
	paramLen := int(binary.BigEndian.Uint16(got[6:8]))
	dataOff := 10 + paramLen
	if dataOff+8 > len(got) {
		t.Fatalf("expected 8-byte data block")
	}
	data := got[dataOff:]
	if data[0] != RetCodeSuccess {
		t.Fatalf("expected RetCodeSuccess in data[0], got 0x%02X", data[0])
	}
	if binary.BigEndian.Uint16(data[4:6]) != SZLModuleID {
		t.Fatalf("expected SZLModuleID in data, got 0x%04X", binary.BigEndian.Uint16(data[4:6]))
	}
}

func TestParseSZLResponse(t *testing.T) {
	// Minimal valid: 8 bytes, success, dataLen=4, szlID=0x0011, szlIndex=0
	data := make([]byte, 8)
	data[0] = RetCodeSuccess
	data[1] = 0x09
	binary.BigEndian.PutUint16(data[2:4], 4)
	binary.BigEndian.PutUint16(data[4:6], 0x0011)
	binary.BigEndian.PutUint16(data[6:8], 0)

	resp, err := ParseSZLResponse(data)
	if err != nil {
		t.Fatalf("ParseSZLResponse: %v", err)
	}
	if resp.SZLID != 0x0011 || resp.SZLIndex != 0 || resp.DataLength != 4 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestParseSZLResponseWithData(t *testing.T) {
	data := make([]byte, 12)
	data[0] = RetCodeSuccess
	data[1] = 0x09
	binary.BigEndian.PutUint16(data[2:4], 8)
	binary.BigEndian.PutUint16(data[4:6], 0x0424)
	binary.BigEndian.PutUint16(data[6:8], 0)
	copy(data[8:], []byte{0x01, 0x02, 0x03, 0x04})

	resp, err := ParseSZLResponse(data)
	if err != nil {
		t.Fatalf("ParseSZLResponse: %v", err)
	}
	if len(resp.Data) != 4 || resp.Data[0] != 0x01 {
		t.Fatalf("unexpected data: %v", resp.Data)
	}
}

func TestParseSZLResponseTooShort(t *testing.T) {
	_, err := ParseSZLResponse([]byte{0xFF, 0x09, 0, 4})
	if err == nil {
		t.Fatal("expected error for short buffer")
	}
}

func TestParseSZLResponseBadReturnCode(t *testing.T) {
	data := make([]byte, 8)
	data[0] = RetCodeAccessFault
	_, err := ParseSZLResponse(data)
	if err == nil {
		t.Fatal("expected error for non-success return code")
	}
}

func TestParseSZLResponseDataLengthMismatch(t *testing.T) {
	data := make([]byte, 8)
	data[0] = RetCodeSuccess
	data[1] = 0x09
	binary.BigEndian.PutUint16(data[2:4], 100) // claims 100 bytes of data
	_, err := ParseSZLResponse(data)
	if err == nil {
		t.Fatal("expected error for data length mismatch")
	}
}

func FuzzParseSZLResponse(f *testing.F) {
	data := make([]byte, 12)
	data[0] = RetCodeSuccess
	data[1] = 0x09
	binary.BigEndian.PutUint16(data[2:4], 8)
	binary.BigEndian.PutUint16(data[4:6], 0x0424)
	binary.BigEndian.PutUint16(data[6:8], 0)
	f.Add(data)
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseSZLResponse(data)
	})
}

func TestSZLIDString(t *testing.T) {
	if got := SZLIDString(SZLModuleID); got != "Module identification" {
		t.Errorf("SZLIDString(0x0011): got %q", got)
	}
	if got := SZLIDString(SZLCPUState); got != "Current mode transition" {
		t.Errorf("SZLIDString(0x0424): got %q", got)
	}
	if got := SZLIDString(0x9999); got != "SZL 0x9999" {
		t.Errorf("SZLIDString(unknown): got %q", got)
	}
}
