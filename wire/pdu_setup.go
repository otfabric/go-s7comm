// SPDX-License-Identifier: MIT

package wire

import "encoding/binary"

// EncodeSetupCommRequest creates a Setup Communication request with the given PDU reference.
// Each value must be 0..65535 (ErrOutOfRange otherwise).
func EncodeSetupCommRequest(pduRef uint16, maxAmqCalling, maxAmqCalled, pduSize int) ([]byte, error) {
	calling, err := checkedUint16("max AmQ calling", maxAmqCalling)
	if err != nil {
		return nil, err
	}
	called, err := checkedUint16("max AmQ called", maxAmqCalled)
	if err != nil {
		return nil, err
	}
	size, err := checkedUint16("PDU size", pduSize)
	if err != nil {
		return nil, err
	}
	param := make([]byte, 8)
	param[0] = FuncSetupComm
	param[1] = 0x00 // Reserved
	binary.BigEndian.PutUint16(param[2:4], calling)
	binary.BigEndian.PutUint16(param[4:6], called)
	binary.BigEndian.PutUint16(param[6:8], size)
	return encodeMessage(ROSCTRJob, pduRef, param, nil)
}

// SetupCommResponse holds the response from Setup Communication
type SetupCommResponse struct {
	MaxAmqCalling int
	MaxAmqCalled  int
	PDUSize       int
}

// ParseSetupCommResponse parses a Setup Communication response
func ParseSetupCommResponse(data []byte) (*SetupCommResponse, error) {
	if len(data) < 8 {
		return nil, &S7Error{Message: "setup comm response too short"}
	}
	if data[0] != FuncSetupComm {
		return nil, &S7Error{Message: "not a setup comm response"}
	}
	return &SetupCommResponse{
		MaxAmqCalling: int(binary.BigEndian.Uint16(data[2:4])),
		MaxAmqCalled:  int(binary.BigEndian.Uint16(data[4:6])),
		PDUSize:       int(binary.BigEndian.Uint16(data[6:8])),
	}, nil
}
