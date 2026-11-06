// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"fmt"
	"math"
)

// S7 Area codes (classic subset). C and T are timer/counter, not generic byte/bit addresses.
const (
	AreaDataRecord    = 0x01 // Data record (optional; not all targets)
	AreaSysInfo       = 0x03
	AreaSysFlags      = 0x05
	AreaS7200AN       = 0x06 // Analog in 200
	AreaS7200AO       = 0x07 // Analog out 200 (S7-200 style)
	AreaInputs        = 0x81 // I
	AreaOutputs       = 0x82 // Q
	AreaMerkers       = 0x83 // M
	AreaDB            = 0x84 // DB
	AreaDI            = 0x85 // DI (instance DB)
	AreaLocal         = 0x86
	AreaV             = 0x87
	AreaCounter       = 0x1C // C (counter)
	AreaTimer         = 0x1D // T (timer)
	AreaIECCounter200 = 30   // IEC counters (S7-200 family)
	AreaIECTimer200   = 31   // IEC timers (S7-200 family)
	AreaPeripheral    = 0x80 // P
)

// Syntax IDs for variable specification. Only S7Any is supported for encoding.
const (
	SyntaxIDS7Any        = 0x10 // Supported
	SyntaxIDDBRead       = 0xB0 // Recognized, rejected
	SyntaxID1200Symbolic = 0xB2
	SyntaxIDNCK          = 0x82 // 0x82/0x83/0x84
	SyntaxIDDriveES      = 0xA2
)

// UnsupportedSyntaxError indicates a request syntax ID that is recognized but not supported.
type UnsupportedSyntaxError struct {
	RawSyntaxID byte
}

func (e *UnsupportedSyntaxError) Error() string {
	return "unsupported syntax ID 0x" + hexByte(e.RawSyntaxID)
}

// ValidateRequestSyntax returns nil for SyntaxIDS7Any, UnsupportedSyntaxError for known-unsupported syntaxes.
func ValidateRequestSyntax(syntax byte) error {
	switch syntax {
	case SyntaxIDS7Any:
		return nil
	case SyntaxIDDBRead, SyntaxID1200Symbolic, SyntaxIDDriveES, 0x82, 0x83, 0x84:
		return &UnsupportedSyntaxError{RawSyntaxID: syntax}
	default:
		return &UnsupportedSyntaxError{RawSyntaxID: syntax}
	}
}

// ValidateArea returns nil for supported classic areas, S7Error for unsupported.
func ValidateArea(area byte) error {
	switch area {
	case AreaDataRecord, AreaInputs, AreaOutputs, AreaMerkers, AreaDB, AreaDI, AreaLocal, AreaV,
		AreaCounter, AreaTimer, AreaIECCounter200, AreaIECTimer200, AreaPeripheral, AreaSysInfo, AreaSysFlags, AreaS7200AN, AreaS7200AO:
		return nil
	default:
		return &S7Error{Message: "unsupported area code 0x" + hexByte(area)}
	}
}

// AreaString returns a short display name for the area code (e.g. "I", "DB", "M"). Unknown codes return hex.
func AreaString(area byte) string {
	if s, ok := areaNames[area]; ok {
		return s
	}
	return fmt.Sprintf("0x%02X", area)
}

var areaNames = map[byte]string{
	AreaDataRecord: "RECORD", AreaSysInfo: "SI200", AreaSysFlags: "SF200",
	AreaS7200AN: "AI200", AreaS7200AO: "AO200",
	AreaPeripheral: "P", AreaInputs: "I", AreaOutputs: "Q", AreaMerkers: "M",
	AreaDB: "DB", AreaDI: "DI", AreaLocal: "L", AreaV: "V",
	AreaCounter: "C", AreaTimer: "T",
	30: "C200", 31: "T200", // IEC counter/timer 200
}

// SyntaxIDString returns a display name for the variable specification syntax ID. Unknown IDs return hex.
func SyntaxIDString(syntax byte) string {
	if s, ok := syntaxIDNames[syntax]; ok {
		return s
	}
	return fmt.Sprintf("0x%02X", syntax)
}

var syntaxIDNames = map[byte]string{
	SyntaxIDS7Any: "S7ANY", 0x11: "ParameterShort", 0x12: "ParameterExtended",
	0x13: "PBC-R_ID", 0x15: "ALARM_LOCKFREE", 0x16: "ALARM_IND", 0x19: "ALARM_ACK",
	0x1a: "ALARM_QUERYREQ", 0x1c: "NOTIFY_IND",
	0x82: "NCK", 0x83: "NCK_M", 0x84: "NCK_I",
	SyntaxIDDriveES: "DRIVEESANY", SyntaxID1200Symbolic: "1200SYM", SyntaxIDDBRead: "DBREAD",
}

// Transport size codes for request (address specification in S7Any).
// Do not use in response payload length normalization; use ResponseTransportSize there.
const (
	TransportSizeBit   = 0x01
	TransportSizeByte  = 0x02
	TransportSizeChar  = 0x03
	TransportSizeWord  = 0x04
	TransportSizeInt   = 0x05
	TransportSizeDWord = 0x06
	TransportSizeDInt  = 0x07
	TransportSizeReal  = 0x08
)

// ResponseTransportSize is the data transport size of a Read Var answer
// item (or a Write Var data item). It is not the request transport size of
// an S7ANY address: use the DataTransportSize* constants for its values.
type ResponseTransportSize byte

// Deprecated: these constants copy request transport sizes (and S7-200
// types) and are not data transport sizes; only 0x03 (BIT), 0x04, 0x05,
// 0x06, 0x07 and 0x09 occur in an answer. Use DataTransportSizeBit,
// DataTransportSizeByte, DataTransportSizeInt, DataTransportSizeDInt,
// DataTransportSizeReal and DataTransportSizeOctet. Kept so that existing
// code compiles; NormalizeResponseDataLength refuses the values that are
// not data transport sizes.
const (
	RespTransportSizeBit       ResponseTransportSize = 0x01
	RespTransportSizeByte      ResponseTransportSize = 0x02
	RespTransportSizeChar      ResponseTransportSize = 0x03
	RespTransportSizeWord      ResponseTransportSize = 0x04
	RespTransportSizeInt       ResponseTransportSize = 0x05
	RespTransportSizeDWord     ResponseTransportSize = 0x06
	RespTransportSizeDInt      ResponseTransportSize = 0x07
	RespTransportSizeReal      ResponseTransportSize = 0x08
	RespTransportSizeDATE      ResponseTransportSize = 0x09
	RespTransportSizeTOD       ResponseTransportSize = 0x0A
	RespTransportSizeTIME      ResponseTransportSize = 0x0B
	RespTransportSizeS5TIME    ResponseTransportSize = 0x0C
	RespTransportSizeDT        ResponseTransportSize = 0x0F
	RespTransportSizeCount     ResponseTransportSize = 0x1C
	RespTransportSizeTimer     ResponseTransportSize = 0x1D
	RespTransportSizeIECCount  ResponseTransportSize = 30
	RespTransportSizeIECTimer  ResponseTransportSize = 31
	RespTransportSizeHSCounter ResponseTransportSize = 32
)

// DataTransportSizeNull is the data transport size of an item without data
// (a refused item).
const DataTransportSizeNull = 0x00

// String returns the data transport size name as Wireshark's s7comm
// dissector gives it (e.g. "BYTE/WORD/DWORD", "REAL"); other values in hex.
func (r ResponseTransportSize) String() string {
	if s, ok := responseTransportSizeNames[r]; ok {
		return s
	}
	return fmt.Sprintf("0x%02X", byte(r))
}

var responseTransportSizeNames = map[ResponseTransportSize]string{
	DataTransportSizeNull:  "NULL",
	DataTransportSizeBit:   "BIT",
	DataTransportSizeByte:  "BYTE/WORD/DWORD",
	DataTransportSizeInt:   "INTEGER",
	DataTransportSizeDInt:  "DINTEGER",
	DataTransportSizeReal:  "REAL",
	DataTransportSizeOctet: "OCTET STRING",
}

// S7AnyAddress is a specification of an S7 variable address
type S7AnyAddress struct {
	Area     byte
	DBNumber int
	Start    int // Byte offset
	Size     int // Number of bytes
}

// S7ANY field limits. The start address is a 24-bit bit offset, so the last
// addressable byte is MaxS7AnyByteOffset; DB number and element count are 16-bit.
const (
	MaxS7AnyDBNumber   = math.MaxUint16
	MaxS7AnyByteOffset = 0xFFFFFF >> 3
	MaxS7AnyCount      = math.MaxUint16
	// MaxReadVarItems is the item count limit of one Read Var request (one byte).
	MaxReadVarItems = math.MaxUint8
	// MaxWriteVarBytes is the largest Write Var payload: its length is 16-bit, in bits.
	MaxWriteVarBytes = math.MaxUint16 >> 3
)

// ValidateS7AnyAddress returns an ErrOutOfRange error when a field does not fit
// its S7ANY field. It does not check the area: see ValidateArea.
func ValidateS7AnyAddress(addr S7AnyAddress) error {
	_, err := EncodeS7Any(addr)
	return err
}

// ValidateS7AnyBitAddress is ValidateS7AnyAddress for a one-bit address; BitOffset must be 0..7.
func ValidateS7AnyBitAddress(addr S7AnyBitAddress) error {
	_, err := EncodeS7AnyBit(addr)
	return err
}

// putS7Any writes the 12-byte S7ANY item for fields ValidateS7Any* accepted.
func putS7Any(transport byte, count, dbNumber uint16, area byte, startBit uint32) []byte {
	buf := make([]byte, 12)
	buf[0] = 0x12 // Var spec
	buf[1] = 0x0A // Length of following
	buf[2] = SyntaxIDS7Any
	buf[3] = transport
	binary.BigEndian.PutUint16(buf[4:6], count)
	binary.BigEndian.PutUint16(buf[6:8], dbNumber)
	buf[8] = area
	buf[9] = byte(startBit >> 16 & 0xFF)
	buf[10] = byte(startBit >> 8 & 0xFF)
	buf[11] = byte(startBit & 0xFF)
	return buf
}

// EncodeS7Any encodes an S7Any address specification (syntax SyntaxIDS7Any only).
// Call ValidateArea(addr.Area) before encoding if area comes from untrusted input.
// Byte-oriented: Start is a byte offset; Size is a byte count; transport size is BYTE.
// A field that does not fit returns ErrOutOfRange (ValidateS7AnyAddress).
func EncodeS7Any(addr S7AnyAddress) ([]byte, error) {
	if addr.DBNumber < 0 || addr.DBNumber > MaxS7AnyDBNumber {
		return nil, fmt.Errorf("%w: DB number %d not in 0..%d", ErrOutOfRange, addr.DBNumber, MaxS7AnyDBNumber)
	}
	if addr.Start < 0 || addr.Start > MaxS7AnyByteOffset {
		return nil, fmt.Errorf("%w: start %d not in 0..%d", ErrOutOfRange, addr.Start, MaxS7AnyByteOffset)
	}
	if addr.Size < 0 || addr.Size > MaxS7AnyCount {
		return nil, fmt.Errorf("%w: size %d not in 0..%d", ErrOutOfRange, addr.Size, MaxS7AnyCount)
	}
	return putS7Any(TransportSizeByte, uint16(addr.Size), uint16(addr.DBNumber), addr.Area, uint32(addr.Start)<<3), nil
}

// S7AnyBitAddress is a single-bit S7ANY address. BitOffset must be 0..7.
type S7AnyBitAddress struct {
	Area       byte
	DBNumber   int
	ByteOffset int
	BitOffset  int
}

// EncodeS7AnyBit encodes a one-bit S7ANY address (transport BIT, element count 1).
// Start is ByteOffset*8+BitOffset (already in bits; not multiplied again).
// A field that does not fit returns ErrOutOfRange (ValidateS7AnyBitAddress).
func EncodeS7AnyBit(addr S7AnyBitAddress) ([]byte, error) {
	if addr.DBNumber < 0 || addr.DBNumber > MaxS7AnyDBNumber {
		return nil, fmt.Errorf("%w: DB number %d not in 0..%d", ErrOutOfRange, addr.DBNumber, MaxS7AnyDBNumber)
	}
	if addr.ByteOffset < 0 || addr.ByteOffset > MaxS7AnyByteOffset {
		return nil, fmt.Errorf("%w: byte offset %d not in 0..%d", ErrOutOfRange, addr.ByteOffset, MaxS7AnyByteOffset)
	}
	if addr.BitOffset < 0 || addr.BitOffset > 7 {
		return nil, fmt.Errorf("%w: bit offset %d not in 0..7", ErrOutOfRange, addr.BitOffset)
	}
	startBit := uint32(addr.ByteOffset)<<3 | uint32(addr.BitOffset)
	return putS7Any(TransportSizeBit, 1, uint16(addr.DBNumber), addr.Area, startBit), nil
}

// EncodeReadVarBitRequest creates a Read Var request for one bit.
func EncodeReadVarBitRequest(pduRef uint16, addr S7AnyBitAddress) ([]byte, error) {
	item, err := EncodeS7AnyBit(addr)
	if err != nil {
		return nil, err
	}
	param := append([]byte{FuncReadVar, 1}, item...)
	return encodeMessage(ROSCTRJob, pduRef, param, nil)
}

// DataTransportSizeBit is the BIT transport size in Read/Write Var data sections (0x03).
// Distinct from request S7ANY TransportSizeBit (0x01).
const DataTransportSizeBit = 0x03

// EncodeWriteVarBitRequest creates a Write Var request for one bit using native BIT data transport.
// Data section: reserved 0x00, transport BIT (0x03), length 1 bit, payload 0x00/0x01.
func EncodeWriteVarBitRequest(pduRef uint16, addr S7AnyBitAddress, value bool) ([]byte, error) {
	item, err := EncodeS7AnyBit(addr)
	if err != nil {
		return nil, err
	}
	param := append([]byte{FuncWriteVar, 1}, item...)

	payload := byte(0)
	if value {
		payload = 1
	}
	// Five bytes, padded to even length.
	data := []byte{0x00, DataTransportSizeBit, 0x00, 0x01, payload, 0x00}
	return encodeMessage(ROSCTRJob, pduRef, param, data)
}

// ReadVarRequestOverhead is the minimum PDU bytes for one read-var item: S7 header (10) + param (2) + S7Any (12).
const ReadVarRequestOverhead = S7HeaderSize + 2 + 12

// WriteVarRequestOverhead is the minimum PDU bytes for one write-var item: S7 header (10) + param (2) + S7Any (12) + data header (4).
const WriteVarRequestOverhead = S7HeaderSize + 2 + 12 + 4

// EncodeReadVarRequest creates a read variable request. More than
// MaxReadVarItems addresses, or an address ValidateS7AnyAddress rejects,
// returns ErrOutOfRange.
func EncodeReadVarRequest(pduRef uint16, addrs []S7AnyAddress) ([]byte, error) {
	n := len(addrs)
	if n > MaxReadVarItems {
		return nil, fmt.Errorf("%w: %d items, at most %d", ErrOutOfRange, n, MaxReadVarItems)
	}
	param := make([]byte, 2, 2+12*n)
	param[0] = FuncReadVar
	param[1] = byte(n)

	for _, addr := range addrs {
		item, err := EncodeS7Any(addr)
		if err != nil {
			return nil, err
		}
		param = append(param, item...)
	}
	return encodeMessage(ROSCTRJob, pduRef, param, nil)
}

// ReadVarItem represents a single read result. Raw fields are preserved; Data is normalized payload (success only).
type ReadVarItem struct {
	ReturnCode       byte   // Item return code
	RawTransportSize byte   // Transport size from wire
	RawLength        uint16 // Length from wire (bits or bytes per transport size)
	Data             []byte // Normalized payload bytes (only when ReturnCode == RetCodeSuccess)
}

// NormalizeResponseDataLength converts the length field of a Read Var
// answer item to its payload byte count. The field counts bits for
// BYTE/WORD/DWORD (0x04) and INTEGER (0x05), and bytes for DINTEGER (0x06),
// REAL (0x07) and OCTET STRING (0x09), as Wireshark's s7comm dissector
// reads it. A BIT item (0x03) carries one bit in one byte; its length,
// normally 1, is rounded up to whole bytes. Any other transport size is an
// error.
func NormalizeResponseDataLength(transportSize ResponseTransportSize, rawLength uint16) (payloadBytes int, err error) {
	switch transportSize {
	case DataTransportSizeBit, DataTransportSizeByte, DataTransportSizeInt:
		return (int(rawLength) + 7) / 8, nil
	case DataTransportSizeDInt, DataTransportSizeReal, DataTransportSizeOctet:
		return int(rawLength), nil
	default:
		return 0, &S7Error{Message: "unknown response transport size 0x" + hexByte(byte(transportSize))}
	}
}

func hexByte(b byte) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{hex[b>>4], hex[b&0x0F]})
}

// decodeReadResponseItem parses one Read Var response item at data[offset:].
// Returns the item, the next offset (after payload and any fill byte for 16-bit alignment), and error.
// Fill byte: after an odd-length payload, one padding byte is consumed so the next item is word-aligned.
func decodeReadResponseItem(data []byte, offset int, isLastItem bool) (ReadVarItem, int, error) {
	if offset+4 > len(data) {
		return ReadVarItem{}, 0, fmt.Errorf("read response item at offset %d: %w", offset, ErrTruncatedItemHeader)
	}
	retCode := data[offset]
	transportSize := data[offset+1]
	rawLength := binary.BigEndian.Uint16(data[offset+2 : offset+4])

	item := ReadVarItem{
		ReturnCode:       retCode,
		RawTransportSize: transportSize,
		RawLength:        rawLength,
		Data:             nil,
	}

	// Non-success items (e.g. Snap7 address fault) often use transport size 0x00 and no
	// payload. Surface the return code without requiring a known transport size.
	if retCode != RetCodeSuccess {
		next := offset + 4
		// Snap7-compatible rejection: transport size 0x00 means no payload.
		// Preserve RawLength, but do not interpret it as payload length.
		if transportSize == 0x00 {
			return item, next, nil
		}
		if rawLength == 0 {
			return item, next, nil
		}
		// Known/meaningful transport with nonzero length: strictly validate and skip payload.
		byteLen, err := NormalizeResponseDataLength(ResponseTransportSize(transportSize), rawLength)
		if err != nil {
			return ReadVarItem{}, 0, err
		}
		if next+byteLen > len(data) {
			return ReadVarItem{}, 0, fmt.Errorf("read response item at offset %d: %w", offset, ErrTruncatedItemPayload)
		}
		next += byteLen
		if byteLen%2 != 0 {
			if next >= len(data) && !isLastItem {
				return ReadVarItem{}, 0, fmt.Errorf("read response item at offset %d: %w", offset, ErrTruncatedItemPayload)
			}
			if next < len(data) {
				next++
			}
		}
		return item, next, nil
	}

	byteLen, err := NormalizeResponseDataLength(ResponseTransportSize(transportSize), rawLength)
	if err != nil {
		return ReadVarItem{}, 0, err
	}
	if byteLen < 0 {
		return ReadVarItem{}, 0, &S7Error{Message: "read response item negative length"}
	}
	if offset+4+byteLen > len(data) {
		return ReadVarItem{}, 0, fmt.Errorf("read response item at offset %d: %w", offset, ErrTruncatedItemPayload)
	}

	item.Data = data[offset+4 : offset+4+byteLen]
	next := offset + 4 + byteLen
	// Consume fill byte for 16-bit alignment before next item (classic S7 multi-item response).
	if byteLen%2 != 0 {
		if next >= len(data) && !isLastItem {
			return ReadVarItem{}, 0, fmt.Errorf("read response item at offset %d: %w", offset, ErrTruncatedItemPayload)
		}
		if next < len(data) {
			next++
		}
	}
	return item, next, nil
}

// ParseReadVarResponse parses a read variable response. Strict: requires exactly itemCount
// items; fails on truncated item headers, length overrun, unknown transport size, or count mismatch.
func ParseReadVarResponse(param, data []byte) ([]ReadVarItem, error) {
	if len(param) < 2 {
		return nil, &S7Error{Message: "read response param too short"}
	}
	if param[0] != FuncReadVar {
		return nil, &S7Error{Message: "not a read var response"}
	}

	itemCount := int(param[1])
	items := make([]ReadVarItem, 0, itemCount)
	offset := 0

	for i := range itemCount {
		isLast := i == itemCount-1
		item, next, err := decodeReadResponseItem(data, offset, isLast)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		offset = next
	}

	return items, nil
}

// EncodeWriteVarRequest creates a write variable request. A value longer than
// MaxWriteVarBytes, or an address ValidateS7AnyAddress rejects, returns ErrOutOfRange.
func EncodeWriteVarRequest(pduRef uint16, addr S7AnyAddress, value []byte) ([]byte, error) {
	bits := len(value) * 8
	if bits > math.MaxUint16 {
		return nil, fmt.Errorf("%w: write of %d bytes, at most %d", ErrOutOfRange, len(value), MaxWriteVarBytes)
	}
	item, err := EncodeS7Any(addr)
	if err != nil {
		return nil, err
	}
	param := append([]byte{FuncWriteVar, 1}, item...) // one item

	// Data
	data := make([]byte, 4, 4+len(value)+1)
	data[0] = RetCodeSuccess                            // Return code (reserved)
	data[1] = 0x04                                      // Transport size (byte/word/dword)
	binary.BigEndian.PutUint16(data[2:4], uint16(bits)) // Length in bits
	data = append(data, value...)
	// Pad to even length
	if len(data)%2 != 0 {
		data = append(data, 0x00)
	}
	return encodeMessage(ROSCTRJob, pduRef, param, data)
}

// ParseWriteVarResponse parses a write variable response
func ParseWriteVarResponse(param, data []byte) error {
	if len(param) < 2 {
		return &S7Error{Message: "write response param too short"}
	}
	if param[0] != FuncWriteVar {
		return &S7Error{Message: "not a write var response"}
	}
	if len(data) < 1 {
		return &S7Error{Message: "write response data too short"}
	}
	return ReturnCodeError(data[0])
}

// Typed decoding for successful ReadVar items. No decode for non-success; unknown transport size returns error without panic.

func decodeSuccessItem(item ReadVarItem, need int) ([]byte, error) {
	if item.ReturnCode != RetCodeSuccess {
		return nil, &S7Error{Message: "item not success, cannot decode"}
	}
	if len(item.Data) < need {
		return nil, &S7Error{Message: fmt.Sprintf("item payload too short: need %d, got %d", need, len(item.Data))}
	}
	return item.Data, nil
}

// DecodeAsByte returns the first byte. For BIT/BYTE transport size.
func DecodeAsByte(item ReadVarItem) (byte, error) {
	data, err := decodeSuccessItem(item, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// DecodeAsBit interprets a successful one-bit Read Var item as a bool.
// Requires return code success, data transport BIT (0x03), and a one-byte payload.
// Any nonzero low bit is treated as true (Snap7-compatible).
func DecodeAsBit(item ReadVarItem) (bool, error) {
	if item.ReturnCode != RetCodeSuccess {
		return false, &S7Error{Message: "item not success, cannot decode"}
	}
	if item.RawTransportSize != DataTransportSizeBit {
		return false, &S7Error{Message: "expected BIT response transport size, got 0x" + hexByte(item.RawTransportSize)}
	}
	// Data-section BIT length is in bits.
	byteLen := (int(item.RawLength) + 7) / 8
	if byteLen != 1 || len(item.Data) < 1 {
		return false, &S7Error{Message: fmt.Sprintf("bit response payload length invalid: normalized=%d data=%d", byteLen, len(item.Data))}
	}
	return item.Data[0]&0x01 != 0, nil
}

// DecodeAsWord returns the first 2 bytes as big-endian uint16.
func DecodeAsWord(item ReadVarItem) (uint16, error) {
	data, err := decodeSuccessItem(item, 2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

// DecodeAsDWord returns the first 4 bytes as big-endian uint32.
func DecodeAsDWord(item ReadVarItem) (uint32, error) {
	data, err := decodeSuccessItem(item, 4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

// DecodeAsInt returns the first 2 bytes as big-endian int16.
func DecodeAsInt(item ReadVarItem) (int16, error) {
	data, err := decodeSuccessItem(item, 2)
	if err != nil {
		return 0, err
	}
	return int16(data[0])<<8 | int16(data[1]), nil
}

// DecodeAsDInt returns the first 4 bytes as big-endian int32.
func DecodeAsDInt(item ReadVarItem) (int32, error) {
	data, err := decodeSuccessItem(item, 4)
	if err != nil {
		return 0, err
	}
	return int32(data[0])<<24 | int32(data[1])<<16 | int32(data[2])<<8 | int32(data[3]), nil
}

// DecodeAsReal returns the first 4 bytes as big-endian float32.
func DecodeAsReal(item ReadVarItem) (float32, error) {
	data, err := decodeSuccessItem(item, 4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(data)), nil
}
