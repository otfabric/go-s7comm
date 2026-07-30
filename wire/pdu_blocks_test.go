// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"testing"
)

func TestParseBlockInfoResponse(t *testing.T) {
	// Minimum 6 bytes: LoadMemory, LocalData, MC7Size (each 2 bytes)
	data := make([]byte, 8)
	binary.BigEndian.PutUint16(data[0:2], 100)
	binary.BigEndian.PutUint16(data[2:4], 200)
	binary.BigEndian.PutUint16(data[4:6], 300)
	info, err := ParseBlockInfoResponse(data)
	if err != nil {
		t.Fatalf("ParseBlockInfoResponse: %v", err)
	}
	if info.LoadMemory != 100 || info.LocalData != 200 || info.MC7Size != 300 {
		t.Errorf("got LoadMemory=%d LocalData=%d MC7Size=%d", info.LoadMemory, info.LocalData, info.MC7Size)
	}
	_, err = ParseBlockInfoResponse(data[:4])
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestEncodeBlockListRequest(t *testing.T) {
	msg := EncodeBlockListRequest(1, BlockTypeDB)
	if len(msg) < 10 {
		t.Fatalf("expected SZL request length >= 10, got %d", len(msg))
	}
	// Default/unknown block type
	msg0 := EncodeBlockListRequest(1, 0)
	if len(msg0) < 10 {
		t.Fatalf("EncodeBlockListRequest(0): got %d bytes", len(msg0))
	}
	// All block types
	for _, bt := range []byte{BlockTypeOB, BlockTypeDB, BlockTypeSDB, BlockTypeFC, BlockTypeSFC, BlockTypeFB, BlockTypeSFB} {
		m := EncodeBlockListRequest(1, bt)
		if len(m) < 10 {
			t.Errorf("EncodeBlockListRequest(0x%02X): got %d bytes", bt, len(m))
		}
	}
}

func TestParseBlockListResponse(t *testing.T) {
	// Two entries: block 1 type DB, block 2 type FC
	data := make([]byte, 8)
	binary.BigEndian.PutUint16(data[0:2], 1)
	data[2] = BlockTypeDB
	data[3] = 0x51 // lang + flags
	binary.BigEndian.PutUint16(data[4:6], 2)
	data[6] = BlockTypeFC
	data[7] = 0x00
	entries, err := ParseBlockListResponse(data)
	if err != nil {
		t.Fatalf("ParseBlockListResponse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].BlockNumber != 1 || entries[0].BlockType != BlockTypeDB {
		t.Errorf("entry 0: %+v", entries[0])
	}
	if entries[1].BlockNumber != 2 || entries[1].BlockType != BlockTypeFC {
		t.Errorf("entry 1: %+v", entries[1])
	}
	// Empty data
	empty, _ := ParseBlockListResponse(nil)
	if len(empty) != 0 {
		t.Errorf("expected 0 entries for nil, got %d", len(empty))
	}
	// Misaligned length must error
	_, err = ParseBlockListResponse([]byte{1, 2, 3, 4, 5})
	if err == nil {
		t.Fatal("expected error for length not multiple of 4")
	}
}

func TestParseStartUploadResponse(t *testing.T) {
	param := []byte{FuncUploadStart, 0, 0, 0, 0, 0, 0, 0, 4, 'A', 'B', 'C', 'D'}
	session, err := ParseStartUploadResponse(param)
	if err != nil {
		t.Fatalf("ParseStartUploadResponse error: %v", err)
	}
	if session != "ABCD" {
		t.Fatalf("unexpected session: %q", session)
	}
}

func TestParseUploadResponse(t *testing.T) {
	param := []byte{FuncUpload, 0}
	data := []byte{RetCodeSuccess, 0x09, 0x00, 0x10, 0xAA, 0xBB}
	chunk, err := ParseUploadResponse(param, data)
	if err != nil {
		t.Fatalf("ParseUploadResponse error: %v", err)
	}
	if !chunk.Done {
		t.Fatal("expected done=true for status 0")
	}
	if len(chunk.Data) != 2 {
		t.Fatalf("unexpected chunk length: %d", len(chunk.Data))
	}
	// Length overrun must error (declared 100 bytes, only 5 in buffer)
	overrunData := []byte{0, 0, 0x00, 0xC8, 0x01}
	_, err = ParseUploadResponse(param, overrunData)
	if err == nil {
		t.Fatal("expected error for upload response length overrun")
	}
}

func TestEncodeStartUploadRequest(t *testing.T) {
	msg := EncodeStartUploadRequest(1, BlockTypeDB, 42)
	if len(msg) < 20 {
		t.Fatalf("expected request length >= 20, got %d", len(msg))
	}
	if msg[10] != FuncUploadStart {
		t.Fatalf("expected FuncUploadStart at param start")
	}
}

func TestEncodeUploadRequest(t *testing.T) {
	msg := EncodeUploadRequest(1, "sess1")
	if len(msg) < 10 {
		t.Fatalf("expected request length >= 10, got %d", len(msg))
	}
	if msg[10] != FuncUpload {
		t.Fatalf("expected FuncUpload at param start")
	}
}

func TestEncodeEndUploadRequest(t *testing.T) {
	msg := EncodeEndUploadRequest(1, "sess1")
	if len(msg) < 10 {
		t.Fatalf("expected request length >= 10, got %d", len(msg))
	}
	if msg[10] != FuncUploadEnd {
		t.Fatalf("expected FuncUploadEnd at param start")
	}
}

func TestParseStartUploadResponseInvalid(t *testing.T) {
	if _, err := ParseStartUploadResponse([]byte{0x00}); err == nil {
		t.Fatal("expected error for short param")
	}
	if _, err := ParseStartUploadResponse([]byte{FuncUpload, 0, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("expected error for zero id length")
	}
}

func FuzzParseUploadResponse(f *testing.F) {
	param := []byte{FuncUpload, 0}
	data := []byte{0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	f.Add(param, data)
	f.Fuzz(func(t *testing.T, param, data []byte) {
		_, _ = ParseUploadResponse(param, data)
	})
}
