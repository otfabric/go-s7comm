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

// ResponseTransportSize is the data transport size in a Read Var response item.
// Distinct type to prevent using request transport size in response length normalization.
type ResponseTransportSize byte

const (
	RespTransportSizeBit       ResponseTransportSize = 0x01
	RespTransportSizeByte      ResponseTransportSize = 0x02
	RespTransportSizeChar      ResponseTransportSize = 0x03
	RespTransportSizeWord      ResponseTransportSize = 0x04
	RespTransportSizeInt       ResponseTransportSize = 0x05
	RespTransportSizeDWord     ResponseTransportSize = 0x06
	RespTransportSizeDInt      ResponseTransportSize = 0x07
	RespTransportSizeReal      ResponseTransportSize = 0x08
	RespTransportSizeDATE      ResponseTransportSize = 0x09 // 2 bytes
	RespTransportSizeTOD       ResponseTransportSize = 0x0A // 4 bytes (time of day)
	RespTransportSizeTIME      ResponseTransportSize = 0x0B // 4 bytes
	RespTransportSizeS5TIME    ResponseTransportSize = 0x0C // 2 bytes
	RespTransportSizeDT        ResponseTransportSize = 0x0F // 8 bytes (date and time)
	RespTransportSizeCount     ResponseTransportSize = 0x1C // COUNTER (2 bytes)
	RespTransportSizeTimer     ResponseTransportSize = 0x1D // TIMER (2 bytes)
	RespTransportSizeIECCount  ResponseTransportSize = 30   // IEC counter (S7-200)
	RespTransportSizeIECTimer  ResponseTransportSize = 31   // IEC timer (S7-200)
	RespTransportSizeHSCounter ResponseTransportSize = 32   // High-speed counter
)

// String returns the transport size name for logging/diagnostics (e.g. "BYTE", "DATE", "COUNTER").
func (r ResponseTransportSize) String() string {
	if s, ok := responseTransportSizeNames[r]; ok {
		return s
	}
	return fmt.Sprintf("0x%02X", byte(r))
}

var responseTransportSizeNames = map[ResponseTransportSize]string{
	RespTransportSizeBit: "BIT", RespTransportSizeByte: "BYTE", RespTransportSizeChar: "CHAR",
	RespTransportSizeWord: "WORD", RespTransportSizeInt: "INT", RespTransportSizeDWord: "DWORD",
	RespTransportSizeDInt: "DINT", RespTransportSizeReal: "REAL",
	RespTransportSizeDATE: "DATE", RespTransportSizeTOD: "TOD", RespTransportSizeTIME: "TIME",
	RespTransportSizeS5TIME: "S5TIME", RespTransportSizeDT: "DATE_AND_TIME",
	RespTransportSizeCount: "COUNTER", RespTransportSizeTimer: "TIMER",
	RespTransportSizeIECCount: "IEC_COUNTER", RespTransportSizeIECTimer: "IEC_TIMER",
	RespTransportSizeHSCounter: "HS_COUNTER",
}

// S7AnyAddress is a specification of an S7 variable address
type S7AnyAddress struct {
	Area     byte
	DBNumber int
	Start    int // Byte offset
	Size     int // Number of bytes
}

// EncodeS7Any encodes an S7Any address specification (syntax SyntaxIDS7Any only).
// Call ValidateArea(addr.Area) before encoding if area comes from untrusted input.
func EncodeS7Any(addr S7AnyAddress) []byte {
	buf := make([]byte, 12)
	buf[0] = 0x12 // Var spec
	buf[1] = 0x0A // Length of following
	buf[2] = SyntaxIDS7Any
	buf[3] = TransportSizeByte

	binary.BigEndian.PutUint16(buf[4:6], uint16(addr.Size))
	binary.BigEndian.PutUint16(buf[6:8], uint16(addr.DBNumber))
	buf[8] = addr.Area

	// Start address in bits
	startBit := addr.Start * 8
	buf[9] = byte(startBit >> 16)
	buf[10] = byte(startBit >> 8)
	buf[11] = byte(startBit)

	return buf
}

// ReadVarRequestOverhead is the minimum PDU bytes for one read-var item: S7 header (10) + param (2) + S7Any (12).
const ReadVarRequestOverhead = S7HeaderSize + 2 + 12

// WriteVarRequestOverhead is the minimum PDU bytes for one write-var item: S7 header (10) + param (2) + S7Any (12) + data header (4).
const WriteVarRequestOverhead = S7HeaderSize + 2 + 12 + 4

// EncodeReadVarRequest creates a read variable request
func EncodeReadVarRequest(pduRef uint16, addrs []S7AnyAddress) []byte {
	param := make([]byte, 2)
	param[0] = FuncReadVar
	param[1] = byte(len(addrs))

	for _, addr := range addrs {
		param = append(param, EncodeS7Any(addr)...)
	}

	header := EncodeS7Header(ROSCTRJob, pduRef, len(param), 0)
	return append(header, param...)
}

// ReadVarItem represents a single read result. Raw fields are preserved; Data is normalized payload (success only).
type ReadVarItem struct {
	ReturnCode       byte   // Item return code
	RawTransportSize byte   // Transport size from wire
	RawLength        uint16 // Length from wire (bits or bytes per transport size)
	Data             []byte // Normalized payload bytes (only when ReturnCode == RetCodeSuccess)
}

// NormalizeResponseDataLength converts the raw length field of a Read Var response item
// to payload byte count. Use ResponseTransportSize only; do not pass request transport size.
func NormalizeResponseDataLength(transportSize ResponseTransportSize, rawLength uint16) (payloadBytes int, err error) {
	switch transportSize {
	case RespTransportSizeBit, RespTransportSizeChar, RespTransportSizeWord, RespTransportSizeInt,
		RespTransportSizeDWord, RespTransportSizeDInt, RespTransportSizeReal:
		return (int(rawLength) + 7) / 8, nil
	case RespTransportSizeByte:
		return int(rawLength), nil
	case RespTransportSizeDATE, RespTransportSizeS5TIME, RespTransportSizeCount, RespTransportSizeTimer,
		RespTransportSizeIECCount, RespTransportSizeIECTimer, RespTransportSizeHSCounter:
		// 2-byte types (or same bit-length formula)
		return (int(rawLength) + 7) / 8, nil
	case RespTransportSizeTOD, RespTransportSizeTIME:
		// 4-byte types
		return (int(rawLength) + 7) / 8, nil
	case RespTransportSizeDT:
		// 8-byte date-time
		return (int(rawLength) + 7) / 8, nil
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
		byteLen := 0
		if n, err := NormalizeResponseDataLength(ResponseTransportSize(transportSize), rawLength); err == nil && n > 0 {
			byteLen = n
		}
		next := offset + 4
		if offset+4+byteLen <= len(data) {
			next = offset + 4 + byteLen
			if byteLen%2 != 0 && next < len(data) {
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

	for i := 0; i < itemCount; i++ {
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

// EncodeWriteVarRequest creates a write variable request
func EncodeWriteVarRequest(pduRef uint16, addr S7AnyAddress, value []byte) []byte {
	param := make([]byte, 2)
	param[0] = FuncWriteVar
	param[1] = 1 // Item count
	param = append(param, EncodeS7Any(addr)...)

	// Data
	data := make([]byte, 4)
	data[0] = RetCodeSuccess                                    // Return code (reserved)
	data[1] = 0x04                                              // Transport size (byte/word/dword)
	binary.BigEndian.PutUint16(data[2:4], uint16(len(value)*8)) // Length in bits
	data = append(data, value...)
	// Pad to even length
	if len(data)%2 != 0 {
		data = append(data, 0x00)
	}

	header := EncodeS7Header(ROSCTRJob, pduRef, len(param), len(data))
	result := append(header, param...)
	return append(result, data...)
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
	return int16(binary.BigEndian.Uint16(data)), nil
}

// DecodeAsDInt returns the first 4 bytes as big-endian int32.
func DecodeAsDInt(item ReadVarItem) (int32, error) {
	data, err := decodeSuccessItem(item, 4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(data)), nil
}

// DecodeAsReal returns the first 4 bytes as big-endian float32.
func DecodeAsReal(item ReadVarItem) (float32, error) {
	data, err := decodeSuccessItem(item, 4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(data)), nil
}
