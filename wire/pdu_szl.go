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

// Userdata function codes
const (
	UserdataReq      = 0x11
	UserdataRes      = 0x12
	UserdataSubParam = 0x44 // With parameters
	UserdataSZL      = 0x01 // SZL read
)

// EncodeSZLRequest creates a SZL read request
func EncodeSZLRequest(pduRef, szlID, szlIndex uint16) []byte {
	// Parameter block
	param := []byte{
		0x00, 0x01, 0x12, // Parameter header
		0x04, // Parameter length
		UserdataSubParam,
		UserdataSZL,
		0x00, // Sequence number
		0x00, // Data unit reference number
	}

	// Data block
	data := make([]byte, 8)
	data[0] = RetCodeSuccess                 // Return code
	data[1] = 0x09                           // Transport size: OCT
	binary.BigEndian.PutUint16(data[2:4], 4) // Length
	binary.BigEndian.PutUint16(data[4:6], szlID)
	binary.BigEndian.PutUint16(data[6:8], szlIndex)

	header := EncodeS7Header(ROSCTRUserdata, pduRef, len(param), len(data))
	result := append(header, param...)
	return append(result, data...)
}

// SZLResponse holds a parsed SZL response
type SZLResponse struct {
	SZLID      uint16
	SZLIndex   uint16
	DataLength uint16
	Data       []byte
}

// ParseSZLResponse parses a SZL read response from the S7 data payload.
// Strict: validates return code, data length, and minimum 8-byte header.
func ParseSZLResponse(data []byte) (*SZLResponse, error) {
	if len(data) < 8 {
		return nil, &S7Error{Message: "SZL response too short"}
	}

	retCode := data[0]
	if retCode != RetCodeSuccess {
		return nil, ReturnCodeError(retCode)
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
