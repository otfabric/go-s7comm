// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// Block type codes for S7: the ASCII character a CPU uses for the type in
// block functions and upload file names.
const (
	BlockTypeOB  = 0x38 // '8'
	BlockTypeDB  = 0x41 // 'A'
	BlockTypeSDB = 0x42 // 'B'
	BlockTypeFC  = 0x43 // 'C'
	BlockTypeSFC = 0x44 // 'D'
	BlockTypeFB  = 0x45 // 'E'
	BlockTypeSFB = 0x46 // 'F'
)

func validBlockType(t byte) bool {
	switch t {
	case BlockTypeOB, BlockTypeDB, BlockTypeSDB, BlockTypeFC, BlockTypeSFC, BlockTypeFB, BlockTypeSFB:
		return true
	}
	return false
}

// MaxUploadBlockNumber is the largest block number the five-digit block
// address of block functions and upload file names holds.
const MaxUploadBlockNumber = 99999

// blockAddressLen is the length of a block address.
const blockAddressLen = 8

// blockAddress is the block address of block info and upload: '0', the
// type character and the block number in five ASCII digits, then the
// file system ('A', active), e.g. "0A00001A" for DB 1.
func blockAddress(blockType byte, blockNum int) ([]byte, error) {
	if !validBlockType(blockType) {
		return nil, fmt.Errorf("%w: 0x%02X", ErrUnknownBlockType, blockType)
	}
	if blockNum < 0 || blockNum > MaxUploadBlockNumber {
		return nil, fmt.Errorf("%w: block number %d not in 0..%d", ErrOutOfRange, blockNum, MaxUploadBlockNumber)
	}
	return fmt.Appendf(nil, "0%c%05dA", blockType, blockNum), nil
}

// EncodeBlockCountRequest asks for the number of blocks of each type (user
// data, block functions, subfunction 0x01).
func EncodeBlockCountRequest(pduRef uint16) []byte {
	return userdataRequest(pduRef, UserdataGroupBlock, UserdataBlockCount, []byte{RetCodeNotAvailable, 0x00, 0x00, 0x00})
}

// BlockCount is the number of blocks of one type.
type BlockCount struct {
	BlockType byte
	Count     int
}

// ParseBlockCountResponse decodes the payload (ParseUserdataData) of a block
// count response: 4 bytes per type, 0x30, the type character, the count.
func ParseBlockCountResponse(payload []byte) ([]BlockCount, error) {
	if len(payload)%4 != 0 {
		return nil, fmt.Errorf("block count data length %d not multiple of 4", len(payload))
	}
	out := make([]BlockCount, 0, len(payload)/4)
	for off := 0; off < len(payload); off += 4 {
		out = append(out, BlockCount{BlockType: payload[off+1], Count: int(binary.BigEndian.Uint16(payload[off+2:]))})
	}
	return out, nil
}

// EncodeBlockListRequest asks for the blocks of one type (user data, block
// functions, subfunction 0x02, data ff 09 00 02 30 <type>). A long list
// comes in several packets: ask for each further one with
// EncodeUserdataFollowUp(ref, UserdataGroupBlock, UserdataBlockList, seq).
func EncodeBlockListRequest(pduRef uint16, blockType byte) []byte {
	return userdataRequest(pduRef, UserdataGroupBlock, UserdataBlockList, []byte{RetCodeSuccess, 0x09, 0x00, 0x02, 0x30, blockType})
}

// BlockListEntry represents an entry in the block list response. The type
// is not on the wire: it is the type that was asked for.
type BlockListEntry struct {
	BlockType   byte
	BlockNumber uint16
	Language    byte
	Flags       byte
}

// ParseBlockListResponse parses the payload (ParseUserdataData) of a block
// list response. Strict: its length must be a multiple of 4. Each entry is
// the block number (2 bytes), the block flags and the block language.
func ParseBlockListResponse(payload []byte) ([]BlockListEntry, error) {
	if len(payload)%4 != 0 {
		return nil, fmt.Errorf("block list data length %d not multiple of 4", len(payload))
	}
	entries := make([]BlockListEntry, 0, len(payload)/4)
	for off := 0; off < len(payload); off += 4 {
		entries = append(entries, BlockListEntry{
			BlockNumber: binary.BigEndian.Uint16(payload[off : off+2]),
			Flags:       payload[off+2],
			Language:    payload[off+3],
		})
	}
	return entries, nil
}

// EncodeBlockInfoRequest asks for the header of one block (user data,
// block functions, subfunction 0x03, the block address as data).
func EncodeBlockInfoRequest(pduRef uint16, blockType byte, blockNum int) ([]byte, error) {
	addr, err := blockAddress(blockType, blockNum)
	if err != nil {
		return nil, err
	}
	data := append([]byte{RetCodeSuccess, 0x09, 0x00, blockAddressLen}, addr...)
	return userdataRequest(pduRef, UserdataGroupBlock, UserdataBlockInfo, data), nil
}

// BlockInfoData is a block header from a block info response, in the
// layout S7-300 CPUs answer and Wireshark's s7comm dissector decodes.
type BlockInfoData struct {
	Flags        byte
	Language     byte
	SubBlockType byte
	Number       int
	LoadMemory   int // load memory length, bytes
	Security     int // 0 none, 3 know-how protected
	SSBLength    int
	AddLength    int
	LocalData    int
	MC7Size      int
	Author       string
	Family       string
	Name         string
	Version      string // header version, "major.minor"
	Checksum     uint16
}

// blockInfoSize is the length of a block info record up to the checksum.
const blockInfoSize = 70

// ParseBlockInfoResponse parses the payload (ParseUserdataData) of a block
// info response.
func ParseBlockInfoResponse(payload []byte) (BlockInfoData, error) {
	if len(payload) < blockInfoSize {
		return BlockInfoData{}, fmt.Errorf("block info data too short: need %d bytes, got %d", blockInfoSize, len(payload))
	}
	b := payload
	text := func(s []byte) string { return strings.TrimRight(string(s), "\x00 ") }
	return BlockInfoData{
		Flags:        b[9],
		Language:     b[10],
		SubBlockType: b[11],
		Number:       int(binary.BigEndian.Uint16(b[12:14])),
		LoadMemory:   int(binary.BigEndian.Uint32(b[14:18])),
		Security:     int(binary.BigEndian.Uint32(b[18:22])),
		SSBLength:    int(binary.BigEndian.Uint16(b[34:36])),
		AddLength:    int(binary.BigEndian.Uint16(b[36:38])),
		LocalData:    int(binary.BigEndian.Uint16(b[38:40])),
		MC7Size:      int(binary.BigEndian.Uint16(b[40:42])),
		Author:       text(b[42:50]),
		Family:       text(b[50:58]),
		Name:         text(b[58:66]),
		Version:      strconv.Itoa(int(b[66]>>4)) + "." + strconv.Itoa(int(b[66]&0x0f)),
		Checksum:     binary.BigEndian.Uint16(b[68:70]),
	}, nil
}

// EncodeStartUploadRequest starts the upload of one block. The file name is
// '_' and the block address, e.g. "_0B00000A" for SDB 0.
func EncodeStartUploadRequest(pduRef uint16, blockType byte, blockNum int) ([]byte, error) {
	addr, err := blockAddress(blockType, blockNum)
	if err != nil {
		return nil, err
	}
	param := []byte{FuncUploadStart, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 1 + blockAddressLen, '_'}
	param = append(param, addr...)
	return encodeMessage(ROSCTRJob, pduRef, param, nil)
}

// StartUpload is the answer to a start upload request.
type StartUpload struct {
	UploadID uint32 // to pass to EncodeUploadRequest and EncodeEndUploadRequest
	Length   int    // block length the CPU announces, -1 when absent or not a number
}

// ParseStartUploadResponse decodes the parameter section of a start upload
// response: function, status, 2 bytes, the 4-byte upload ID, then the block
// length as a length-prefixed ASCII number.
func ParseStartUploadResponse(param []byte) (StartUpload, error) {
	if len(param) < 8 || param[0] != FuncUploadStart {
		return StartUpload{}, &S7Error{Message: "invalid start upload response"}
	}
	out := StartUpload{UploadID: binary.BigEndian.Uint32(param[4:8]), Length: -1}
	if len(param) > 8 {
		n := int(param[8])
		if 9+n > len(param) {
			return StartUpload{}, &S7Error{Message: "invalid start upload block length"}
		}
		if v, err := strconv.Atoi(string(param[9 : 9+n])); err == nil {
			out.Length = v
		}
	}
	return out, nil
}

// EncodeUploadRequest requests the next segment of a started upload.
func EncodeUploadRequest(pduRef uint16, uploadID uint32) []byte {
	return uploadSessionRequest(pduRef, FuncUpload, uploadID)
}

// EncodeEndUploadRequest ends an upload session.
func EncodeEndUploadRequest(pduRef uint16, uploadID uint32) []byte {
	return uploadSessionRequest(pduRef, FuncUploadEnd, uploadID)
}

// uploadSessionRequest encodes an Upload or End Upload request: function,
// status, 2 bytes, the 4-byte upload ID.
func uploadSessionRequest(pduRef uint16, fn byte, uploadID uint32) []byte {
	const paramLen = 8
	param := make([]byte, paramLen)
	param[0] = fn
	binary.BigEndian.PutUint32(param[4:8], uploadID)
	return append(s7Header(ROSCTRJob, pduRef, paramLen, 0), param...)
}

// UploadChunk is one chunk returned by upload continuation responses.
type UploadChunk struct {
	Done bool
	Data []byte
}

// ParseUploadResponse parses an upload response. The parameter's status
// byte is 0x01 while more data follows, 0x00 for the last segment. The data
// section is the segment length in bytes (2 bytes), 2 further bytes, then
// the segment. Strict: the length must not overrun the data section.
func ParseUploadResponse(param, data []byte) (*UploadChunk, error) {
	if len(param) < 2 || param[0] != FuncUpload {
		return nil, &S7Error{Message: "invalid upload response"}
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("upload response data too short")
	}
	n := int(binary.BigEndian.Uint16(data[0:2]))
	if 4+n > len(data) {
		return nil, fmt.Errorf("upload response length overrun: need %d bytes, have %d", 4+n, len(data))
	}
	return &UploadChunk{Done: param[1]&0x01 == 0, Data: data[4 : 4+n]}, nil
}
