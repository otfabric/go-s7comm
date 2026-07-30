// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"fmt"
)

// ROSCTR is the S7 message type (Remote Operating Service Control).
type ROSCTR byte

// S7 ROSCTR constants. ACK and ACK_DATA are distinct: ACK has no payload, ACK_DATA carries param+data.
const (
	ROSCTRJob      ROSCTR = 0x01 // Job request
	ROSCTRAck      ROSCTR = 0x02 // Acknowledgement without data
	ROSCTRAckData  ROSCTR = 0x03 // Acknowledgement with data
	ROSCTRUserdata ROSCTR = 0x07 // Userdata (for SZL, etc.)
)

// IsAck returns true for ROSCTRAck (ack without data).
func (r ROSCTR) IsAck() bool { return r == ROSCTRAck }

// IsAckData returns true for ROSCTRAckData (ack with param/data payload).
func (r ROSCTR) IsAckData() bool { return r == ROSCTRAckData }

// IsJob returns true for ROSCTRJob.
func (r ROSCTR) IsJob() bool { return r == ROSCTRJob }

// IsUserdata returns true for ROSCTRUserdata.
func (r ROSCTR) IsUserdata() bool { return r == ROSCTRUserdata }

// S7 function codes
const (
	FuncSetupComm     = 0xF0 // Setup communication
	FuncReadVar       = 0x04 // Read variable
	FuncWriteVar      = 0x05 // Write variable
	FuncDownloadStart = 0x1A
	FuncDownload      = 0x1B
	FuncDownloadEnd   = 0x1C
	FuncUploadStart   = 0x1D
	FuncUpload        = 0x1E
	FuncUploadEnd     = 0x1F
	FuncPIService     = 0x28 // Program invocation
	FuncPLCStop       = 0x29
	FuncPLCControl    = 0x28
)

// FunctionCodeString returns a display name for the S7 parameter function code (e.g. "Read Var", "Upload").
func FunctionCodeString(code byte) string {
	if s, ok := functionCodeNames[code]; ok {
		return s
	}
	return fmt.Sprintf("0x%02X", code)
}

var functionCodeNames = map[byte]string{
	0x00: "CPU services", 0x01: "Mode transition",
	FuncSetupComm: "Setup communication", FuncReadVar: "Read Var", FuncWriteVar: "Write Var",
	FuncDownloadStart: "Request download", FuncDownload: "Download block", FuncDownloadEnd: "Download ended",
	FuncUploadStart: "Start upload", FuncUpload: "Upload", FuncUploadEnd: "End upload",
	FuncPIService: "PI-Service", FuncPLCStop: "PLC Stop",
}

// S7Header represents an S7 protocol header. All fields are raw wire values.
type S7Header struct {
	ProtocolID   byte   // Always 0x32
	ROSCTR       ROSCTR // Message type (Job, Ack, AckData, Userdata)
	RedundancyID uint16
	PDURef       uint16 // PDU reference for request/response correlation
	ParamLength  uint16 // Declared parameter section length
	DataLength   uint16 // Declared data section length
	ErrorClass   byte   // Error class (for Ack/AckData)
	ErrorCode    byte   // Error code (for Ack/AckData)
}

const S7HeaderSize = 10

// EncodeS7Header creates an S7 protocol header
func EncodeS7Header(rosctr ROSCTR, pduRef uint16, paramLen, dataLen int) []byte {
	buf := make([]byte, S7HeaderSize)
	buf[0] = 0x32 // S7 Protocol ID
	buf[1] = byte(rosctr)
	binary.BigEndian.PutUint16(buf[2:4], 0)      // Redundancy ID
	binary.BigEndian.PutUint16(buf[4:6], pduRef) // PDU Ref
	binary.BigEndian.PutUint16(buf[6:8], uint16(paramLen))
	binary.BigEndian.PutUint16(buf[8:10], uint16(dataLen))
	return buf
}

// ParseS7Header parses an S7 header from data
func ParseS7Header(data []byte) (*S7Header, []byte, error) {
	if len(data) < S7HeaderSize {
		return nil, nil, ErrShortS7Header
	}
	if data[0] != 0x32 {
		return nil, nil, fmt.Errorf("%w: got 0x%02X", ErrInvalidS7ProtocolID, data[0])
	}

	h := &S7Header{
		ProtocolID:   data[0],
		ROSCTR:       ROSCTR(data[1]),
		RedundancyID: binary.BigEndian.Uint16(data[2:4]),
		PDURef:       binary.BigEndian.Uint16(data[4:6]),
		ParamLength:  binary.BigEndian.Uint16(data[6:8]),
		DataLength:   binary.BigEndian.Uint16(data[8:10]),
	}

	offset := S7HeaderSize
	if h.ROSCTR.IsAckData() || h.ROSCTR.IsAck() {
		if len(data) < S7HeaderSize+2 {
			return nil, nil, ErrShortS7AckHeader
		}
		h.ErrorClass = data[10]
		h.ErrorCode = data[11]
		offset = 12
	}

	need := int(h.ParamLength) + int(h.DataLength)
	if len(data[offset:]) < need {
		return nil, nil, fmt.Errorf("%w: need %d, got %d", ErrS7PayloadLength, need, len(data[offset:]))
	}

	return h, data[offset:], nil
}
