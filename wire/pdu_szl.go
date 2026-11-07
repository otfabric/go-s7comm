// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"fmt"
)

// SZL ID constants
const (
	SZLModuleID       = 0x0011 // Module identification
	SZLComponentID    = 0x001C // Component identification
	SZLCPUState       = 0x0424 // CPU state
	SZLProtectionInfo = 0x0232 // Protection level
	SZLDiagBuffer     = 0x00A0 // Diagnostic buffer
	SZLBlockList      = 0x0111 // Block list
	SZLBlockInfo      = 0x0113 // Block info
)

// SZLIDString returns a human-readable name for the SZL ID when known (S7 SZL catalog).
// Use in logging and diagnostics. Unknown IDs return a formatted hex string.
func SZLIDString(id uint16) string {
	if s, ok := szlIDNames[id]; ok {
		return s
	}
	return fmt.Sprintf("SZL 0x%04X", id)
}

// szlIDNames maps SZL IDs to descriptive names (S7 SZL ID catalog).
var szlIDNames = map[uint16]string{
	0x0011: "Module identification",
	0x0012: "All characteristics",
	0x0013: "Data records of all memory areas",
	0x0014: "All system areas of a module",
	0x0015: "Data records of all block types",
	0x0016: "Data records of all priority classes",
	0x0017: "All SDBs of a module",
	0x0018: "All data records",
	0x0019: "Status of all LEDs",
	0x001C: "Component identification",
	0x00A0: "Diagnostic buffer",
	0x0111: "Block list (single identification data record)",
	0x0112: "Characteristics of a group",
	0x0113: "Data record for one memory area / Block info",
	0x0114: "One system area",
	0x0115: "Data record of a block type",
	0x0116: "Data record of priority class",
	0x0117: "One single SDB",
	0x0124: "Information about the last mode transition",
	0x0131: "Information about a communication unit",
	0x0132: "Status data for one communication section",
	0x0137: "Details of one Ethernet interface",
	0x0174: "Status of an LED",
	0x0181: "Startup information of all synchronous error OBs",
	0x0182: "Startup events of all synchronous error OBs",
	0x0190: "Information of one DP master system",
	0x0191: "Status information of all modules/racks with wrong type",
	0x01A0: "Most recent diagnostic buffer entries",
	0x0200: "Partial list extract",
	0x021C: "Identification of all components (H system)",
	0x0221: "Data records for specified interrupt",
	0x0222: "Data records for specified interrupt",
	0x0223: "Data records of priority classes being processed",
	0x0224: "Processed mode transition",
	0x0232: "Protection level",
	0x0281: "Startup information of synchronous error OBs (one priority class)",
	0x0282: "Startup events of synchronous error OBs (one priority class)",
	0x0291: "Status information of all faulty modules",
	0x0292: "Actual status of central racks/stations (DP)",
	0x0294: "Actual status of rack/stations (DP/PN)",
	0x0300: "Possible indexes of a partial list extract",
	0x031C: "Identification of one component (redundant CPUs, H system)",
	0x0381: "Startup information of all OBs of one priority class",
	0x0382: "Startup events of all OBs of a priority class",
	0x0391: "Status information of all modules not available",
	0x0392: "State of battery backup of racks",
	0x0424: "Current mode transition",
	0x0492: "State of total backup of racks",
	0x04A0: "Start information of all standard OBs",
	0x0524: "Specified mode transition",
}

// User data parameter fields. A request parameter is
// 00 01 12 04 | method 0x11 | type and function group | subfunction | sequence;
// a response or follow-up parameter is 00 01 12 08 | method 0x12 | type and
// group | subfunction | sequence | data unit reference | last data unit |
// error code (2 bytes). This is the layout Snap7, STEP 7 5.5, TIA Portal V13,
// WinCC Advanced and libnodave send and S7-300/S7-400 CPUs answer
// (testdata/captures).
const (
	UserdataReq = 0x11 // method: request
	UserdataRes = 0x12 // method: response, and a client's follow-up request
	// UserdataSubParam is the type and function group byte of an SZL read
	// request: type 4 (request) and group 4 (CPU functions).
	UserdataSubParam = 0x44
	UserdataSZL      = 0x01 // subfunction: read SZL

	UserdataTypeRequest  = 0x40 // type nibble of a request (high bits of the type/group byte)
	UserdataTypeResponse = 0x80 // type nibble of a response
	UserdataGroupBlock   = 0x03 // function group: block functions
	UserdataGroupCPU     = 0x04 // function group: CPU functions (SZL)

	UserdataBlockCount = 0x01 // block functions: count blocks of each type
	UserdataBlockList  = 0x02 // block functions: list blocks of one type
	UserdataBlockInfo  = 0x03 // block functions: get block info
)

// EncodeUserdataRequest encodes the first request of a user data exchange:
// group is the function group (UserdataGroup*), subfunc its subfunction,
// data the data section. A data section longer than a 16-bit length field
// holds returns ErrOutOfRange.
func EncodeUserdataRequest(pduRef uint16, group, subfunc byte, data []byte) ([]byte, error) {
	param := []byte{0x00, 0x01, 0x12, 0x04, UserdataReq, UserdataTypeRequest | group&0x0f, subfunc, 0x00}
	return encodeMessage(ROSCTRUserdata, pduRef, param, data)
}

// userdataRequest encodes a request whose data section has a fixed size
// well within the 16-bit length field, so encoding cannot fail.
func userdataRequest(pduRef uint16, group, subfunc byte, data []byte) []byte {
	msg, _ := EncodeUserdataRequest(pduRef, group, subfunc, data)
	return msg
}

// EncodeUserdataFollowUp asks for the next packet of a response the CPU
// marked "more data follows", with the sequence number of that response.
func EncodeUserdataFollowUp(pduRef uint16, group, subfunc, seq byte) []byte {
	param := []byte{0x00, 0x01, 0x12, 0x08, UserdataRes, UserdataTypeRequest | group&0x0f, subfunc, seq, 0x00, 0x00, 0x00, 0x00}
	msg, _ := encodeMessage(ROSCTRUserdata, pduRef, param, []byte{RetCodeNotAvailable, 0x00, 0x00, 0x00}) // fixed sizes
	return msg
}

// UserdataParam is the parameter section of a user data response.
type UserdataParam struct {
	Group       byte   // function group
	Subfunction byte   //
	Sequence    byte   // sequence number, echoed in a follow-up request
	More        bool   // last data unit 0x01: more data follows
	ErrorCode   uint16 // 0 when no error
}

// ParseUserdataParam decodes the parameter section of a user data response.
func ParseUserdataParam(param []byte) (*UserdataParam, error) {
	if len(param) < 12 || param[0] != 0x00 || param[1] != 0x01 || param[2] != 0x12 || param[3] != 0x08 {
		return nil, &S7Error{Message: fmt.Sprintf("user data response parameter of %d bytes is not 00 01 12 08", len(param))}
	}
	return &UserdataParam{
		Group:       param[5] & 0x0f,
		Subfunction: param[6],
		Sequence:    param[7],
		More:        param[9] != 0x00,
		ErrorCode:   binary.BigEndian.Uint16(param[10:12]),
	}, nil
}

// ParseUserdataData returns the payload of a user data data section (after
// return code, transport size and length). A return code other than 0xFF is
// returned as an error.
func ParseUserdataData(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, &S7Error{Message: "user data section too short"}
	}
	if data[0] != RetCodeSuccess {
		return nil, ReturnCodeError(data[0])
	}
	n := int(binary.BigEndian.Uint16(data[2:4]))
	if 4+n > len(data) {
		return nil, &S7Error{Message: fmt.Sprintf("user data length %d past the %d bytes received", n, len(data)-4)}
	}
	return data[4 : 4+n], nil
}

// EncodeSZLRequest creates a SZL read request: user data, CPU functions,
// subfunction read SZL, data ff 09 00 04 <id> <index>.
func EncodeSZLRequest(pduRef, szlID, szlIndex uint16) []byte {
	data := make([]byte, 8)
	data[0] = RetCodeSuccess                 // Return code
	data[1] = 0x09                           // Transport size: OCTET STRING
	binary.BigEndian.PutUint16(data[2:4], 4) // Length
	binary.BigEndian.PutUint16(data[4:6], szlID)
	binary.BigEndian.PutUint16(data[6:8], szlIndex)
	return userdataRequest(pduRef, UserdataGroupCPU, UserdataSZL, data)
}

// SZLResponse holds a parsed SZL response
type SZLResponse struct {
	SZLID      uint16
	SZLIndex   uint16
	DataLength uint16
	Data       []byte
}

// ParseSZLResponse parses the first packet of a SZL read response from the
// S7 data payload. Strict: validates return code, data length, and minimum
// 8-byte header. When the CPU marks the response "more data follows"
// (ParseUserdataParam), the next packets carry only further records: append
// their ParseUserdataData payloads to Data.
func ParseSZLResponse(data []byte) (*SZLResponse, error) {
	if len(data) > 0 && data[0] != RetCodeSuccess {
		return nil, ReturnCodeError(data[0]) // a refusal carries only 4 bytes
	}
	if len(data) < 8 {
		return nil, &S7Error{Message: "SZL response too short"}
	}

	// SZL data length; payload is data[8:end] with end = 4+dataLen (header is 4 bytes before data[8])
	dataLen := binary.BigEndian.Uint16(data[2:4])
	end := 4 + int(dataLen)
	if end < 8 {
		return nil, &S7Error{Message: "SZL data length too short"}
	}
	if end > len(data) {
		return nil, &S7Error{Message: "SZL data length mismatch"}
	}

	resp := &SZLResponse{
		SZLID:      binary.BigEndian.Uint16(data[4:6]),
		SZLIndex:   binary.BigEndian.Uint16(data[6:8]),
		DataLength: dataLen,
		Data:       data[8:end],
	}

	return resp, nil
}
