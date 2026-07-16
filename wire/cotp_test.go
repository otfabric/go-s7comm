package wire

import (
	"testing"
)

func TestEncodeRackSlotTSAP(t *testing.T) {
	// Classic S7: low byte = (rack<<5)|slot; rack 3 bits, slot 5 bits.
	tests := []struct {
		rack, slot byte
		want       byte
	}{
		{0, 0, 0x00},
		{0, 2, 0x02},
		{1, 2, 0x22},
		{7, 31, 0xFF},
	}
	for _, tt := range tests {
		got := EncodeRackSlotTSAP(tt.rack, tt.slot)
		if got != tt.want {
			t.Errorf("EncodeRackSlotTSAP(rack=%d, slot=%d) = 0x%02X, want 0x%02X", tt.rack, tt.slot, got, tt.want)
		}
	}
}

func TestBuildTSAP(t *testing.T) {
	ts, err := BuildTSAP(3, 0, 1)
	if err != nil {
		t.Fatalf("BuildTSAP: %v", err)
	}
	// connType=3, rack=0, slot=1 → 0x0301
	if ts != 0x0301 {
		t.Fatalf("BuildTSAP(3,0,1) = 0x%04X, want 0x0301", ts)
	}
}

func TestBuildTSAP_ValidRanges(t *testing.T) {
	cases := []struct {
		connType, rack, slot int
	}{
		{3, 0, 0}, {3, 0, 2}, {3, 1, 2}, {3, 7, 31},
	}
	for _, c := range cases {
		ts, err := BuildTSAP(c.connType, c.rack, c.slot)
		if err != nil {
			t.Errorf("BuildTSAP(%d,%d,%d): %v", c.connType, c.rack, c.slot, err)
			continue
		}
		low := byte(ts & 0xFF)
		exp := EncodeRackSlotTSAP(byte(c.rack), byte(c.slot))
		if low != exp {
			t.Errorf("BuildTSAP(%d,%d,%d) low byte = 0x%02X, want 0x%02X", c.connType, c.rack, c.slot, low, exp)
		}
	}
}

func TestBuildTSAP_InvalidRackSlot(t *testing.T) {
	_, err := BuildTSAP(3, 8, 0)
	if err == nil {
		t.Fatal("BuildTSAP(3,8,0) expected error for rack 8")
	}
	_, err = BuildTSAP(3, 0, 32)
	if err == nil {
		t.Fatal("BuildTSAP(3,0,32) expected error for slot 32")
	}
	_, err = BuildTSAP(3, -1, 0)
	if err == nil {
		t.Fatal("BuildTSAP(3,-1,0) expected error for negative rack")
	}
}

func TestValidateRackSlot(t *testing.T) {
	if err := ValidateRackSlot(0, 0); err != nil {
		t.Errorf("ValidateRackSlot(0,0): %v", err)
	}
	if err := ValidateRackSlot(7, 31); err != nil {
		t.Errorf("ValidateRackSlot(7,31): %v", err)
	}
	if err := ValidateRackSlot(8, 0); err == nil {
		t.Error("ValidateRackSlot(8,0) expected error")
	}
	if err := ValidateRackSlot(0, 32); err == nil {
		t.Error("ValidateRackSlot(0,32) expected error")
	}
}
