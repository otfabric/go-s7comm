// SPDX-License-Identifier: MIT

package model

import "testing"

func TestNumericDecoders(t *testing.T) {
	if DecodeWord([]byte{0x12, 0x34}) != 0x1234 {
		t.Fatal("DecodeWord mismatch")
	}
	if DecodeDWord([]byte{0x01, 0x02, 0x03, 0x04}) != 0x01020304 {
		t.Fatal("DecodeDWord mismatch")
	}
}

func TestStringEncodingRoundTrip(t *testing.T) {
	enc := EncodeString("HELLO", 12)
	got := DecodeString(enc)
	if got != "HELLO" {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestDecodeBool(t *testing.T) {
	if !DecodeBool([]byte{0x04}, 2) {
		t.Fatal("expected bit 2 set")
	}
}

func TestDecodeBoolNegativeBit(t *testing.T) {
	if DecodeBool([]byte{0xFF}, -1) {
		t.Fatal("expected false for negative bit index")
	}
}

func TestEncodeStringBounds(t *testing.T) {
	small := EncodeString("A", 1)
	if len(small) != 2 {
		t.Fatalf("expected minimum encoded length 2, got %d", len(small))
	}
	if small[0] != 0 || small[1] != 0 {
		t.Fatalf("expected empty metadata for too-small max len, got [%d %d]", small[0], small[1])
	}

	large := EncodeString("HELLO", 300)
	if len(large) != 256 {
		t.Fatalf("expected clamped encoded length 256, got %d", len(large))
	}
	if large[0] != 254 {
		t.Fatalf("expected max string length marker 254, got %d", large[0])
	}
}

func TestEncodeStringTruncatesValue(t *testing.T) {
	encoded := EncodeString("ABCDEFGHIJ", 8)
	if encoded[0] != 6 {
		t.Fatalf("expected max payload marker 6, got %d", encoded[0])
	}
	if encoded[1] != 6 {
		t.Fatalf("expected actual payload length 6, got %d", encoded[1])
	}
	if got := DecodeString(encoded); got != "ABCDEF" {
		t.Fatalf("expected truncated payload ABCDEF, got %q", got)
	}
}

func TestDecodeByte(t *testing.T) {
	if got := DecodeByte(nil); got != 0 {
		t.Fatalf("DecodeByte(nil) want 0, got %d", got)
	}
	if got := DecodeByte([]byte{}); got != 0 {
		t.Fatalf("DecodeByte([]) want 0, got %d", got)
	}
	if got := DecodeByte([]byte{0xAB}); got != 0xAB {
		t.Fatalf("DecodeByte want 0xAB, got 0x%02X", got)
	}
}

func TestDecodeInt(t *testing.T) {
	if got := DecodeInt([]byte{0x80, 0x00}); got != -32768 {
		t.Fatalf("DecodeInt 0x8000 want -32768, got %d", got)
	}
	if got := DecodeInt([]byte{0x12, 0x34}); got != 0x1234 {
		t.Fatalf("DecodeInt want 0x1234, got %d", got)
	}
}

func TestDecodeDInt(t *testing.T) {
	if got := DecodeDInt([]byte{0x80, 0, 0, 0}); got != -2147483648 {
		t.Fatalf("DecodeDInt want min int32, got %d", got)
	}
	if got := DecodeDInt([]byte{0x00, 0x00, 0x12, 0x34}); got != 0x1234 {
		t.Fatalf("DecodeDInt want 0x1234, got %d", got)
	}
}

func TestDecodeReal(t *testing.T) {
	// 1.0 in IEEE 754 float32 big-endian
	one := []byte{0x3f, 0x80, 0x00, 0x00}
	if got := DecodeReal(one); got != 1.0 {
		t.Fatalf("DecodeReal(1.0) want 1.0, got %f", got)
	}
	if got := DecodeReal(nil); got != 0 {
		t.Fatalf("DecodeReal(nil) want 0, got %f", got)
	}
}

func TestDecodeWordShortBuffer(t *testing.T) {
	if got := DecodeWord([]byte{0x12}); got != 0 {
		t.Fatalf("DecodeWord(short) want 0, got 0x%04X", got)
	}
}

func TestDecodeDWordShortBuffer(t *testing.T) {
	if got := DecodeDWord([]byte{0x01, 0x02}); got != 0 {
		t.Fatalf("DecodeDWord(short) want 0, got 0x%08X", got)
	}
}

func TestDecodeStringShortBuffer(t *testing.T) {
	if got := DecodeString([]byte{0x05}); got != "" {
		t.Fatalf("DecodeString(short) want \"\", got %q", got)
	}
}

func TestEncodeBool(t *testing.T) {
	if got := string(EncodeBool(true)); got != "\x01" {
		t.Fatalf("EncodeBool(true) want \\x01, got %q", got)
	}
	if got := string(EncodeBool(false)); got != "\x00" {
		t.Fatalf("EncodeBool(false) want \\x00, got %q", got)
	}
}

func TestEncodeByte(t *testing.T) {
	if got := EncodeByte(0x42); len(got) != 1 || got[0] != 0x42 {
		t.Fatalf("EncodeByte(0x42) want [0x42], got %v", got)
	}
}

func TestEncodeWord(t *testing.T) {
	got := EncodeWord(0x1234)
	if len(got) != 2 || DecodeWord(got) != 0x1234 {
		t.Fatalf("EncodeWord roundtrip failed: %v", got)
	}
}

func TestEncodeInt(t *testing.T) {
	got := EncodeInt(-1)
	if len(got) != 2 || DecodeInt(got) != -1 {
		t.Fatalf("EncodeInt(-1) roundtrip failed: %v", got)
	}
}

func TestEncodeDWord(t *testing.T) {
	got := EncodeDWord(0x12345678)
	if len(got) != 4 || DecodeDWord(got) != 0x12345678 {
		t.Fatalf("EncodeDWord roundtrip failed: %v", got)
	}
}

func TestEncodeDInt(t *testing.T) {
	got := EncodeDInt(-1)
	if len(got) != 4 || DecodeDInt(got) != -1 {
		t.Fatalf("EncodeDInt(-1) roundtrip failed: %v", got)
	}
}

func TestEncodeReal(t *testing.T) {
	got := EncodeReal(3.14)
	if len(got) != 4 || DecodeReal(got) != 3.14 {
		t.Fatalf("EncodeReal(3.14) roundtrip failed: %v", got)
	}
}
