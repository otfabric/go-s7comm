// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"testing"
)

func TestParamErrorFromParam(t *testing.T) {
	if code, ok := ParamErrorFromParam(nil); ok || code != 0 {
		t.Errorf("ParamErrorFromParam(nil): want (0, false), got (0x%04X, %v)", code, ok)
	}
	if code, ok := ParamErrorFromParam([]byte{0, 0}); ok || code != 0 {
		t.Errorf("ParamErrorFromParam(2 bytes): want (0, false), got (0x%04X, %v)", code, ok)
	}
	param := make([]byte, 6)
	binary.BigEndian.PutUint16(param[2:4], 0x0114)
	code, ok := ParamErrorFromParam(param)
	if !ok || code != 0x0114 {
		t.Errorf("ParamErrorFromParam(0x0114): want (0x0114, true), got (0x%04X, %v)", code, ok)
	}
}

func TestParamErrorCodeString(t *testing.T) {
	tests := []struct {
		code uint16
		want string
	}{
		{0x0000, "no error"},
		{0x0110, "invalid block number"},
		{0x0114, "block not found"},
		{0x8702, "requested service not supported by module"},
		{0x8500, "S7 protocol error: wrong frames"},
		{0x9999, "parameter error 0x9999"},
	}
	for _, tt := range tests {
		got := ParamErrorCodeString(tt.code)
		if got != tt.want {
			t.Errorf("ParamErrorCodeString(0x%04X): got %q, want %q", tt.code, got, tt.want)
		}
	}
}
