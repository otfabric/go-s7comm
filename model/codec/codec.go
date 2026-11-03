// SPDX-License-Identifier: MIT

// Package codec provides data conversion utilities for S7 types (big-endian words, reals, strings).
// Used by the model package and by wire/client when interpreting PLC data.
package codec

import (
	"encoding/binary"
	"math"
)

// DecodeBool decodes a bit from byte array
func DecodeBool(data []byte, bit int) bool {
	if bit < 0 {
		return false
	}
	if len(data) == 0 {
		return false
	}
	byteOffset := bit / 8
	bitOffset := bit % 8
	if byteOffset >= len(data) {
		return false
	}
	return data[byteOffset]&(1<<uint(bitOffset)) != 0
}

// DecodeByte decodes a single byte
func DecodeByte(data []byte) byte {
	if len(data) < 1 {
		return 0
	}
	return data[0]
}

// DecodeWord decodes an unsigned 16-bit value (big-endian)
func DecodeWord(data []byte) uint16 {
	if len(data) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(data)
}

// DecodeInt decodes a signed 16-bit value (big-endian)
func DecodeInt(data []byte) int16 {
	if len(data) < 2 {
		return 0
	}
	return int16(data[0])<<8 | int16(data[1])
}

// DecodeDWord decodes an unsigned 32-bit value (big-endian)
func DecodeDWord(data []byte) uint32 {
	if len(data) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(data)
}

// DecodeDInt decodes a signed 32-bit value (big-endian)
func DecodeDInt(data []byte) int32 {
	if len(data) < 4 {
		return 0
	}
	return int32(data[0])<<24 | int32(data[1])<<16 | int32(data[2])<<8 | int32(data[3])
}

// DecodeReal decodes a 32-bit float (big-endian IEEE 754)
func DecodeReal(data []byte) float32 {
	bits := DecodeDWord(data)
	return math.Float32frombits(bits)
}

// DecodeString decodes an S7 string (first byte = max len, second = actual len)
func DecodeString(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	actualLen := int(data[1])
	if 2+actualLen > len(data) {
		actualLen = len(data) - 2
	}
	return string(data[2 : 2+actualLen])
}

// EncodeBool encodes a boolean value
func EncodeBool(val bool) []byte {
	if val {
		return []byte{0x01}
	}
	return []byte{0x00}
}

// EncodeByte encodes a byte
func EncodeByte(val byte) []byte {
	return []byte{val}
}

// EncodeWord encodes an unsigned 16-bit value (big-endian)
func EncodeWord(val uint16) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, val)
	return buf
}

// EncodeInt encodes a signed 16-bit value (big-endian)
func EncodeInt(val int16) []byte {
	return []byte{byte(val >> 8 & 0xFF), byte(val & 0xFF)}
}

// EncodeDWord encodes an unsigned 32-bit value (big-endian)
func EncodeDWord(val uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, val)
	return buf
}

// EncodeDInt encodes a signed 32-bit value (big-endian)
func EncodeDInt(val int32) []byte {
	return []byte{byte(val >> 24 & 0xFF), byte(val >> 16 & 0xFF), byte(val >> 8 & 0xFF), byte(val & 0xFF)}
}

// EncodeReal encodes a 32-bit float (big-endian IEEE 754)
func EncodeReal(val float32) []byte {
	return EncodeDWord(math.Float32bits(val))
}

// EncodeString encodes a string as S7 format with a 2-byte header and up to 254 payload chars.
func EncodeString(val string, maxLen int) []byte {
	if maxLen < 2 {
		maxLen = 2
	}
	if maxLen > 256 {
		maxLen = 256
	}

	maxChars := maxLen - 2 // 0..254
	n := min(len(val), maxChars)
	buf := make([]byte, maxLen)
	buf[0] = byte(maxChars) // Max length
	buf[1] = byte(n)        // Actual length
	copy(buf[2:], val[:n])
	return buf
}
