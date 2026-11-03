// SPDX-License-Identifier: MIT

package codec

import (
	"encoding/binary"
	"math"
	"testing"
)

// Signed values round-trip and match the two's complement big-endian layout.
func FuzzSignedRoundTrip(f *testing.F) {
	f.Add(int16(math.MinInt16), int32(math.MinInt32))
	f.Add(int16(-1), int32(-1))
	f.Add(int16(math.MaxInt16), int32(math.MaxInt32))
	f.Fuzz(func(t *testing.T, i int16, d int32) {
		b := EncodeInt(i)
		if DecodeInt(b) != i || binary.BigEndian.Uint16(b) != uint16(i) {
			t.Fatalf("Int %d -> % X", i, b)
		}
		b = EncodeDInt(d)
		if DecodeDInt(b) != d || binary.BigEndian.Uint32(b) != uint32(d) {
			t.Fatalf("DInt %d -> % X", d, b)
		}
	})
}

// EncodeString keeps its header consistent for any input and length.
func FuzzEncodeString(f *testing.F) {
	f.Add("hello", 10)
	f.Add("", -5)
	f.Add(string(make([]byte, 400)), 1000)
	f.Fuzz(func(t *testing.T, s string, maxLen int) {
		b := EncodeString(s, maxLen)
		if len(b) < 2 || len(b) > 256 || int(b[0]) != len(b)-2 || int(b[1]) > int(b[0]) {
			t.Fatalf("EncodeString(%d bytes, %d) = header % X, %d bytes", len(s), maxLen, b[:min(2, len(b))], len(b))
		}
		if got := DecodeString(b); got != s[:b[1]] {
			t.Fatalf("round trip %q, want %q", got, s[:b[1]])
		}
	})
}

func TestShortSignedDecodes(t *testing.T) {
	if DecodeInt([]byte{0xFF}) != 0 || DecodeDInt([]byte{0xFF, 0xFF, 0xFF}) != 0 {
		t.Error("short input must decode to 0")
	}
}
