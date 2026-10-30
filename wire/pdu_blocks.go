// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Block type codes for S7
const (
	BlockTypeOB  = 0x38
	BlockTypeDB  = 0x41
	BlockTypeSDB = 0x42
	BlockTypeFC  = 0x43
	BlockTypeSFC = 0x44
	BlockTypeFB  = 0x45
	BlockTypeSFB = 0x46
)

// EncodeBlockListRequest creates a block list request (via SZL)
func EncodeBlockListRequest(pduRef uint16, blockType byte) []byte {
	// Use SZL 0x0111 with index = block type sublist
	var index uint16
	switch blockType {
	case BlockTypeOB:
		index = 0x0001
	case BlockTypeDB:
		index = 0x0002
	case BlockTypeSDB:
		index = 0x0003
	case BlockTypeFC:
		index = 0x0004
	case BlockTypeSFC:
		index = 0x0005
	case BlockTypeFB:
		index = 0x0006
	case BlockTypeSFB:
		index = 0x0007
	default:
		index = 0x0000 // All blocks
	}
	return EncodeSZLRequest(pduRef, SZLBlockList, index)
}

// BlockListEntry represents an entry in the block list response
type BlockListEntry struct {
	BlockType   byte
	BlockNumber uint16
	Language    byte
	Flags       byte
}

// BlockInfoData holds block info fields parsed from SZL 0x0113 response data.
// Layout is heuristic and may vary by PLC; only LoadMemory, LocalData, MC7Size are parsed confidently.
type BlockInfoData struct {
	LoadMemory int
	LocalData  int
	MC7Size    int
}

// ParseBlockInfoResponse parses block info from SZL 0x0113 response data (resp.Data).
// Permissive/partial: requires at least 6 bytes; only the first three 16-bit fields are interpreted.
// SZL block info layout can vary by device; treat as heuristic for interoperability.
func ParseBlockInfoResponse(szlData []byte) (BlockInfoData, error) {
	out := BlockInfoData{}
	if len(szlData) < 6 {
		return out, fmt.Errorf("block info data too short: need 6 bytes, got %d", len(szlData))
	}
	out.LoadMemory = int(binary.BigEndian.Uint16(szlData[0:2]))
	out.LocalData = int(binary.BigEndian.Uint16(szlData[2:4]))
	out.MC7Size = int(binary.BigEndian.Uint16(szlData[4:6]))
	return out, nil
}

// ParseBlockListResponse parses a block list response. Strict: szlData length must be
// a multiple of 4 (no trailing garbage). Each entry is 4 bytes: block number, type, language/flags.
func ParseBlockListResponse(szlData []byte) ([]BlockListEntry, error) {
	if len(szlData)%4 != 0 {
		return nil, fmt.Errorf("block list data length %d not multiple of 4", len(szlData))
	}
	var entries []BlockListEntry
	offset := 0
	for offset+4 <= len(szlData) {
		entry := BlockListEntry{
			BlockNumber: binary.BigEndian.Uint16(szlData[offset : offset+2]),
			BlockType:   szlData[offset+2],
			Language:    szlData[offset+3] >> 4,
			Flags:       szlData[offset+3] & 0x0F,
		}
		entries = append(entries, entry)
		offset += 4
	}
	return entries, nil
}

// EncodeStartUploadRequest creates a start upload request.
// An unknown block type returns ErrUnknownBlockType, so a bad type cannot request a different block.
func EncodeStartUploadRequest(pduRef uint16, blockType byte, blockNum int) ([]byte, error) {
	// File identifier: _XYYYYY
	// X = block type char, YYYYY = block number
	var typeChar byte
	switch blockType {
	case BlockTypeOB:
		typeChar = '8'
	case BlockTypeDB:
		typeChar = 'A'
	case BlockTypeSDB:
		typeChar = 'B'
	case BlockTypeFC:
		typeChar = 'C'
	case BlockTypeFB:
		typeChar = 'E'
	case BlockTypeSFC:
		typeChar = 'D'
	case BlockTypeSFB:
		typeChar = 'F'
	default:
		return nil, fmt.Errorf("%w: 0x%02X", ErrUnknownBlockType, blockType)
	}

	// Parameter header
	param := []byte{
		FuncUploadStart,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x09, // Filename length
	}

	// Filename: _XNNNNN\x41 (block A area, ASCII)
	filename := make([]byte, 9)
	filename[0] = '_'
	filename[1] = typeChar
	copy(filename[2:7], []byte("00000"))
	// Convert block number to 5 digit ASCII
	numStr := []byte{
		'0' + byte((blockNum/10000)%10),
		'0' + byte((blockNum/1000)%10),
		'0' + byte((blockNum/100)%10),
		'0' + byte((blockNum/10)%10),
		'0' + byte(blockNum%10),
	}
	copy(filename[2:7], numStr)
	filename[7] = 'A' // Block area (passive)
	filename[8] = 0x00

	param = append(param, filename...)

	header := EncodeS7Header(ROSCTRJob, pduRef, len(param), 0)
	return append(header, param...), nil
}

// ParseStartUploadResponse extracts upload session ID from upload start response parameters.
func ParseStartUploadResponse(param []byte) (string, error) {
	if len(param) < 10 || param[0] != FuncUploadStart {
		return "", &S7Error{Message: "invalid start upload response"}
	}
	idLen := int(param[8])
	if idLen <= 0 || 9+idLen > len(param) {
		return "", &S7Error{Message: "invalid upload session id length"}
	}
	return strings.TrimSpace(string(param[9 : 9+idLen])), nil
}

// EncodeUploadRequest requests next upload segment for a started upload session.
func EncodeUploadRequest(pduRef uint16, sessionID string) []byte {
	id := []byte(sessionID)
	if len(id) > 255 {
		id = id[:255]
	}
	param := make([]byte, 9)
	param[0] = FuncUpload
	param[8] = byte(len(id))
	param = append(param, id...)
	header := EncodeS7Header(ROSCTRJob, pduRef, len(param), 0)
	return append(header, param...)
}

// EncodeEndUploadRequest ends an upload session.
func EncodeEndUploadRequest(pduRef uint16, sessionID string) []byte {
	id := []byte(sessionID)
	if len(id) > 255 {
		id = id[:255]
	}
	param := make([]byte, 9)
	param[0] = FuncUploadEnd
	param[8] = byte(len(id))
	param = append(param, id...)
	header := EncodeS7Header(ROSCTRJob, pduRef, len(param), 0)
	return append(header, param...)
}

// UploadChunk is one chunk returned by upload continuation responses.
type UploadChunk struct {
	Done bool
	Data []byte
}

// ParseUploadResponse parses upload continuation response and chunk payload. Strict:
// declared length must not overrun the data buffer; overrun returns an error.
func ParseUploadResponse(param, data []byte) (*UploadChunk, error) {
	if len(param) < 2 || param[0] != FuncUpload {
		return nil, &S7Error{Message: "invalid upload response"}
	}

	status := param[1]
	done := status == 0

	if len(data) < 4 {
		return nil, fmt.Errorf("upload response data too short")
	}

	lengthBits := int(binary.BigEndian.Uint16(data[2:4]))
	lengthBytes := (lengthBits + 7) / 8
	if 4+lengthBytes > len(data) {
		return nil, fmt.Errorf("upload response length overrun: need %d bytes, have %d", 4+lengthBytes, len(data))
	}

	return &UploadChunk{Done: done, Data: data[4 : 4+lengthBytes]}, nil
}
