// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"testing"
)

func TestEncodeSetupCommRequest(t *testing.T) {
	got := EncodeSetupCommRequest(1, 2, 2, 480)
	if len(got) < 10+8 {
		t.Fatalf("expected at least 18 bytes (header+param), got %d", len(got))
	}
	if got[0] != 0x32 {
		t.Fatalf("expected S7 protocol ID 0x32, got 0x%02X", got[0])
	}
	param := got[10:]
	if param[0] != FuncSetupComm {
		t.Fatalf("expected FuncSetupComm 0xF0, got 0x%02X", param[0])
	}
	if binary.BigEndian.Uint16(param[2:4]) != 2 {
		t.Fatalf("expected maxAmqCalling 2, got %d", binary.BigEndian.Uint16(param[2:4]))
	}
	if binary.BigEndian.Uint16(param[6:8]) != 480 {
		t.Fatalf("expected pduSize 480, got %d", binary.BigEndian.Uint16(param[6:8]))
	}
}

func TestParseSetupCommResponse(t *testing.T) {
	// Valid response: FuncSetupComm, reserved, maxAmqCalling=2, maxAmqCalled=2, pduSize=480
	data := make([]byte, 8)
	data[0] = FuncSetupComm
	data[1] = 0
	binary.BigEndian.PutUint16(data[2:4], 2)
	binary.BigEndian.PutUint16(data[4:6], 2)
	binary.BigEndian.PutUint16(data[6:8], 480)

	resp, err := ParseSetupCommResponse(data)
	if err != nil {
		t.Fatalf("ParseSetupCommResponse: %v", err)
	}
	if resp.MaxAmqCalling != 2 || resp.MaxAmqCalled != 2 || resp.PDUSize != 480 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestParseSetupCommResponseTooShort(t *testing.T) {
	_, err := ParseSetupCommResponse([]byte{0xF0, 0, 0, 0, 0, 0, 0})
	if err == nil {
		t.Fatal("expected error for short buffer")
	}
	if se, ok := err.(*S7Error); ok && se.Message != "setup comm response too short" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseSetupCommResponseWrongFunc(t *testing.T) {
	data := make([]byte, 8)
	data[0] = 0x04 // not FuncSetupComm
	_, err := ParseSetupCommResponse(data)
	if err == nil {
		t.Fatal("expected error for wrong function code")
	}
}
