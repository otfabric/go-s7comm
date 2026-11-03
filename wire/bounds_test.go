// SPDX-License-Identifier: MIT

package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

// Encoders refuse a value that does not fit its field instead of wrapping it
// to another address.
func TestEncodersRejectOutOfRange(t *testing.T) {
	tooMany := make([]S7AnyAddress, MaxReadVarItems+1)
	cases := map[string]func() error{
		"DB number 65536":      func() error { _, err := EncodeS7Any(S7AnyAddress{DBNumber: 65536, Size: 1}); return err },
		"DB number -1":         func() error { _, err := EncodeS7Any(S7AnyAddress{DBNumber: -1, Size: 1}); return err },
		"start past 24-bit":    func() error { _, err := EncodeS7Any(S7AnyAddress{Start: MaxS7AnyByteOffset + 1, Size: 1}); return err },
		"start negative":       func() error { _, err := EncodeS7Any(S7AnyAddress{Start: -1, Size: 1}); return err },
		"start overflowing *8": func() error { _, err := EncodeS7Any(S7AnyAddress{Start: math.MaxInt, Size: 1}); return err },
		"size 65536":           func() error { _, err := EncodeS7Any(S7AnyAddress{Size: 65536}); return err },
		"size negative":        func() error { _, err := EncodeS7Any(S7AnyAddress{Size: -1}); return err },
		"bit DB 65536":         func() error { _, err := EncodeS7AnyBit(S7AnyBitAddress{DBNumber: 65536}); return err },
		"bit byte past 24-bit": func() error {
			_, err := EncodeS7AnyBit(S7AnyBitAddress{ByteOffset: MaxS7AnyByteOffset + 1})
			return err
		},
		"bit offset 8":   func() error { _, err := EncodeS7AnyBit(S7AnyBitAddress{BitOffset: 8}); return err },
		"bit offset -1":  func() error { _, err := EncodeS7AnyBit(S7AnyBitAddress{BitOffset: -1}); return err },
		"read 256 items": func() error { _, err := EncodeReadVarRequest(1, tooMany); return err },
		"read bad item":  func() error { _, err := EncodeReadVarRequest(1, []S7AnyAddress{{DBNumber: 70000}}); return err },
		"read bit bad":   func() error { _, err := EncodeReadVarBitRequest(1, S7AnyBitAddress{BitOffset: 9}); return err },
		"write bit bad":  func() error { _, err := EncodeWriteVarBitRequest(1, S7AnyBitAddress{DBNumber: -2}, true); return err },
		"write 8192 bytes": func() error {
			_, err := EncodeWriteVarRequest(1, S7AnyAddress{Size: 8192}, make([]byte, MaxWriteVarBytes+1))
			return err
		},
		"write bad address":    func() error { _, err := EncodeWriteVarRequest(1, S7AnyAddress{Start: -5}, []byte{1}); return err },
		"setup PDU 65536":      func() error { _, err := EncodeSetupCommRequest(1, 1, 1, 65536); return err },
		"setup calling -1":     func() error { _, err := EncodeSetupCommRequest(1, -1, 1, 480); return err },
		"setup called 65536":   func() error { _, err := EncodeSetupCommRequest(1, 1, 65536, 480); return err },
		"header param 65536":   func() error { _, err := EncodeS7Header(ROSCTRJob, 1, 65536, 0); return err },
		"header data -1":       func() error { _, err := EncodeS7Header(ROSCTRJob, 1, 0, -1); return err },
		"upload block -1":      func() error { _, err := EncodeStartUploadRequest(1, BlockTypeDB, -1); return err },
		"upload block 100000":  func() error { _, err := EncodeStartUploadRequest(1, BlockTypeDB, 100000); return err },
		"validate address":     func() error { return ValidateS7AnyAddress(S7AnyAddress{DBNumber: 1 << 20}) },
		"validate bit address": func() error { return ValidateS7AnyBitAddress(S7AnyBitAddress{ByteOffset: -1}) },
	}
	for name, f := range cases {
		if err := f(); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("%s: err = %v, want ErrOutOfRange", name, err)
		}
	}
}

// The largest values that fit are encoded exactly.
func TestEncodersAtTheLimits(t *testing.T) {
	b := must(EncodeS7Any(S7AnyAddress{Area: AreaDB, DBNumber: MaxS7AnyDBNumber, Start: MaxS7AnyByteOffset, Size: MaxS7AnyCount}))
	if !bytes.Equal(b[4:12], []byte{0xFF, 0xFF, 0xFF, 0xFF, AreaDB, 0xFF, 0xFF, 0xF8}) {
		t.Errorf("S7ANY at limits = % X", b)
	}
	b = must(EncodeS7AnyBit(S7AnyBitAddress{Area: AreaDB, DBNumber: MaxS7AnyDBNumber, ByteOffset: MaxS7AnyByteOffset, BitOffset: 7}))
	if !bytes.Equal(b[6:12], []byte{0xFF, 0xFF, AreaDB, 0xFF, 0xFF, 0xFF}) {
		t.Errorf("S7ANY bit at limits = % X", b)
	}
	if err := ValidateS7AnyAddress(S7AnyAddress{}); err != nil {
		t.Errorf("zero address: %v", err)
	}

	msg := must(EncodeReadVarRequest(1, make([]S7AnyAddress, MaxReadVarItems)))
	h, _, err := ParseS7Header(msg)
	if err != nil || msg[11] != MaxReadVarItems || int(h.ParamLength) != 2+12*MaxReadVarItems {
		t.Errorf("255 items: count %d, param length %v, %v", msg[11], h, err)
	}

	msg = must(EncodeWriteVarRequest(1, S7AnyAddress{Size: MaxWriteVarBytes}, make([]byte, MaxWriteVarBytes)))
	if bits := binary.BigEndian.Uint16(msg[S7HeaderSize+14+2:]); bits != MaxWriteVarBytes*8 {
		t.Errorf("write length = %d bits, want %d", bits, MaxWriteVarBytes*8)
	}

	msg = must(EncodeSetupCommRequest(1, math.MaxUint16, math.MaxUint16, math.MaxUint16))
	if !bytes.Equal(msg[S7HeaderSize+2:], []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Errorf("setup at limits = % X", msg)
	}

	msg = must(EncodeStartUploadRequest(1, BlockTypeDB, MaxUploadBlockNumber))
	if !bytes.Contains(msg, []byte("_A99999A")) {
		t.Errorf("block 99999 file name missing: % X", msg)
	}
}

// An upload session ID over 255 bytes is cut to fit its length byte, and the
// header still states the real parameter length.
func TestUploadSessionIDIsCut(t *testing.T) {
	for _, enc := range []func(uint16, string) []byte{EncodeUploadRequest, EncodeEndUploadRequest} {
		msg := enc(1, strings.Repeat("x", 300))
		h, body, err := ParseS7Header(msg)
		if err != nil {
			t.Fatalf("ParseS7Header: %v", err)
		}
		if body[8] != 255 || int(h.ParamLength) != 9+255 || len(body) != 9+255 {
			t.Errorf("id length %d, param length %d, body %d", body[8], h.ParamLength, len(body))
		}
	}
}

func TestBuildTSAPConnectionType(t *testing.T) {
	for _, ct := range []int{-1, 256} {
		if _, err := BuildTSAP(ct, 0, 1); err == nil {
			t.Errorf("connection type %d accepted", ct)
		}
	}
	got, err := BuildTSAP(255, 7, 31)
	if err != nil || got != 0xFFFF {
		t.Errorf("BuildTSAP(255, 7, 31) = %#04x, %v", got, err)
	}
}

func TestSignedDecodes(t *testing.T) {
	item := ReadVarItem{ReturnCode: RetCodeSuccess, Data: []byte{0x80, 0x00, 0x00, 0x01}}
	if v, err := DecodeAsInt(item); err != nil || v != math.MinInt16 {
		t.Errorf("DecodeAsInt = %d, %v", v, err)
	}
	if v, err := DecodeAsDInt(item); err != nil || v != math.MinInt32+1 {
		t.Errorf("DecodeAsDInt = %d, %v", v, err)
	}
}

// Whatever an S7ANY address holds, the encoder either refuses it or writes
// exactly the fields it was given.
func FuzzEncodeS7Any(f *testing.F) {
	f.Add(0, 0, 0, 0, 0)
	f.Add(MaxS7AnyDBNumber, MaxS7AnyByteOffset, MaxS7AnyCount, 7, 1)
	f.Add(-1, math.MaxInt, math.MinInt, 8, 0)
	f.Fuzz(func(t *testing.T, db, start, size, bitOff int, bit int) {
		if bit%2 == 0 {
			b, err := EncodeS7Any(S7AnyAddress{Area: AreaDB, DBNumber: db, Start: start, Size: size})
			fits := db >= 0 && db <= MaxS7AnyDBNumber && start >= 0 && start <= MaxS7AnyByteOffset && size >= 0 && size <= MaxS7AnyCount
			checkS7Any(t, b, err, fits, size, db, start*8)
			return
		}
		b, err := EncodeS7AnyBit(S7AnyBitAddress{Area: AreaDB, DBNumber: db, ByteOffset: start, BitOffset: bitOff})
		fits := db >= 0 && db <= MaxS7AnyDBNumber && start >= 0 && start <= MaxS7AnyByteOffset && bitOff >= 0 && bitOff <= 7
		checkS7Any(t, b, err, fits, 1, db, start*8+bitOff)
	})
}

func checkS7Any(t *testing.T, b []byte, err error, fits bool, count, db, startBit int) {
	t.Helper()
	if !fits {
		if !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("out of range accepted: % X, %v", b, err)
		}
		return
	}
	if err != nil || len(b) != 12 {
		t.Fatalf("in range refused: %v", err)
	}
	gotStart := int(b[9])<<16 | int(b[10])<<8 | int(b[11])
	if int(binary.BigEndian.Uint16(b[4:6])) != count || int(binary.BigEndian.Uint16(b[6:8])) != db || gotStart != startBit {
		t.Fatalf("fields changed: % X, want count %d db %d start bit %d", b, count, db, startBit)
	}
}

// A Write Var request always states lengths that match its sections.
func FuzzEncodeWriteVarRequest(f *testing.F) {
	f.Add(1, 0, []byte{1, 2, 3})
	f.Add(0, MaxS7AnyByteOffset, []byte{})
	f.Fuzz(func(t *testing.T, db, start int, value []byte) {
		msg, err := EncodeWriteVarRequest(7, S7AnyAddress{Area: AreaDB, DBNumber: db, Start: start, Size: len(value)}, value)
		if err != nil {
			if !errors.Is(err, ErrOutOfRange) {
				t.Fatalf("err = %v, want ErrOutOfRange", err)
			}
			return
		}
		h, body, err := ParseS7Header(msg)
		if err != nil {
			t.Fatalf("ParseS7Header: %v", err)
		}
		if len(body) != int(h.ParamLength)+int(h.DataLength) || h.ParamLength != 14 {
			t.Fatalf("lengths: header %d+%d, body %d", h.ParamLength, h.DataLength, len(body))
		}
		data := body[h.ParamLength:]
		if int(binary.BigEndian.Uint16(data[2:4])) != len(value)*8 || !bytes.Equal(data[4:4+len(value)], value) {
			t.Fatalf("data section % X does not carry % X", data, value)
		}
	})
}

func FuzzParseSetupCommResponse(f *testing.F) {
	f.Add([]byte{FuncSetupComm, 0, 0, 1, 0, 1, 0x01, 0xE0})
	f.Add([]byte{FuncSetupComm})
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := ParseSetupCommResponse(b)
		if err == nil && (r.PDUSize < 0 || r.PDUSize > math.MaxUint16) {
			t.Fatalf("PDU size %d", r.PDUSize)
		}
	})
}

func FuzzParseWriteVarResponse(f *testing.F) {
	f.Add([]byte{FuncWriteVar, 1}, []byte{RetCodeSuccess})
	f.Add([]byte{FuncWriteVar, 3}, []byte{RetCodeSuccess, RetCodeAddressFault})
	f.Fuzz(func(t *testing.T, param, data []byte) { _ = ParseWriteVarResponse(param, data) })
}

func FuzzParseStartUploadResponse(f *testing.F) {
	f.Add([]byte{FuncUploadStart, 0, 0, 0, 0, 0, 0, 0, 2, '4', '2'})
	f.Add([]byte{FuncUploadStart, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0})
	f.Fuzz(func(t *testing.T, param []byte) {
		id, err := ParseStartUploadResponse(param)
		if err == nil && len(id) > len(param) {
			t.Fatalf("id %q longer than the parameter", id)
		}
	})
}

func FuzzParseBlockResponses(f *testing.F) {
	f.Add([]byte{0, 1, 0x22, 0x05, 0, 2, 0x41, 0x01})
	f.Add([]byte{0, 1, 2})
	f.Fuzz(func(t *testing.T, b []byte) {
		if entries, err := ParseBlockListResponse(b); err == nil && len(entries) != len(b)/4 {
			t.Fatalf("%d entries from %d bytes", len(entries), len(b))
		}
		_, _ = ParseBlockInfoResponse(b)
	})
}
