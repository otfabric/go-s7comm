// SPDX-License-Identifier: MIT

package wire

import (
	"fmt"
)

// EncodeRackSlotTSAP returns the low byte of a classic S7 TSAP for rack/slot.
// Protocol: bits 0..4 = slot, bits 5..7 = rack. Rack must be 0..7, slot 0..31.
// This is the single canonical TSAP rack/slot encoder; do not duplicate bit-packing elsewhere.
func EncodeRackSlotTSAP(rack, slot byte) byte {
	return ((rack & 0x07) << 5) | (slot & 0x1F)
}

// ValidateRackSlot returns an error if rack or slot are outside classic S7 range (rack 0..7, slot 0..31).
func ValidateRackSlot(rack, slot int) error {
	if rack < 0 || rack > 7 {
		return fmt.Errorf("rack must be 0..7, got %d", rack)
	}
	if slot < 0 || slot > 31 {
		return fmt.Errorf("slot must be 0..31, got %d", slot)
	}
	return nil
}

// BuildTSAP creates a TSAP from connection type, rack, and slot (S7 convention).
// Connection type: 1=PG, 2=OP, 3=S7Basic (0..255). Rack must be 0..7, slot 0..31; returns error if out of range.
func BuildTSAP(connType, rack, slot int) (uint16, error) {
	if err := ValidateRackSlot(rack, slot); err != nil {
		return 0, err
	}
	if connType < 0 || connType > 0xFF {
		return 0, fmt.Errorf("connection type must be 0..255, got %d", connType)
	}
	// ValidateRackSlot bounded rack and slot; the masks state it for the conversion.
	low := EncodeRackSlotTSAP(byte(rack&0x07), byte(slot&0x1F))
	return uint16(connType)<<8 | uint16(low), nil
}
