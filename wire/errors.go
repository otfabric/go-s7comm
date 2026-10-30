// SPDX-License-Identifier: MIT

package wire

import (
	"errors"
	"fmt"
)

var (
	ErrShortS7Header        = errors.New("data too short for S7 header")
	ErrInvalidS7ProtocolID  = errors.New("invalid S7 protocol ID")
	ErrShortS7AckHeader     = errors.New("data too short for S7 ack header")
	ErrS7PayloadLength      = errors.New("S7 payload shorter than parameter/data lengths")
	ErrTruncatedItemHeader  = errors.New("read response item header truncated")
	ErrTruncatedItemPayload = errors.New("read response item payload truncated or overrun")
	ErrInvalidParamLength   = errors.New("invalid parameter section length")
	ErrInvalidDataLength    = errors.New("invalid data section length")
	ErrUnknownBlockType     = errors.New("unknown block type")
)

// S7 Error classes
const (
	ErrClassNoError  = 0x00
	ErrClassApp      = 0x81
	ErrClassObject   = 0x82
	ErrClassResource = 0x83
	ErrClassService  = 0x84
	ErrClassSupplies = 0x85
	ErrClassAccess   = 0x87
)

// ErrClassString returns a short display name for the S7 header error class only (e.g. "Access error").
func ErrClassString(class byte) string {
	if s, ok := errClassNames[class]; ok {
		return s
	}
	return fmt.Sprintf("class 0x%02X", class)
}

var errClassNames = map[byte]string{
	ErrClassNoError: "No error", ErrClassApp: "Application relationship",
	ErrClassObject: "Object definition", ErrClassResource: "No resources available",
	ErrClassService: "Error on service processing", ErrClassSupplies: "Error on supplies",
	ErrClassAccess: "Access error",
}

// Return codes for Read/Write operations
const (
	RetCodeSuccess       = 0xFF // Success
	RetCodeHWFault       = 0x01 // Hardware fault
	RetCodeAccessFault   = 0x03 // Illegal object access
	RetCodeAddressFault  = 0x05 // Invalid address
	RetCodeDataTypeFault = 0x06 // Data type not supported
	RetCodeDataSizeFault = 0x07 // Data size mismatch
	RetCodeBusy          = 0x09 // Object is busy
	RetCodeNotAvailable  = 0x0A // Object not available
)

// S7Error represents an S7 protocol error
type S7Error struct {
	Class   byte
	Code    byte
	Message string
}

func (e *S7Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("S7 error: class=0x%02X, code=0x%02X", e.Class, e.Code)
}

// NewS7Error creates a new S7 error preserving raw Class and Code, with a string mapping when known.
// Header-level errors use Class and Code; item-level return codes use Code only (Class zero).
func NewS7Error(class, code byte) *S7Error {
	msg := HeaderErrorString(class, code)
	return &S7Error{Class: class, Code: code, Message: msg}
}

// NewS7ErrorWithParam is like NewS7Error but when param contains a 16-bit parameter error code at the
// standard offset (bytes 2-3), uses ParamErrorCodeString for the message when that code is non-zero.
// This yields clearer errors (e.g. "block not found", "invalid request length") for block/upload/diagnostic responses.
func NewS7ErrorWithParam(class, code byte, param []byte) *S7Error {
	e := NewS7Error(class, code)
	if paramCode, ok := ParamErrorFromParam(param); ok && paramCode != 0 {
		e.Message = ParamErrorCodeString(paramCode)
	}
	return e
}

// HeaderErrorString returns a human-readable string for S7 header error class/code.
// Preserves raw values in S7Error; this is for display only.
func HeaderErrorString(class, code byte) string {
	switch class {
	case ErrClassNoError:
		if code == 0 {
			return ""
		}
		return fmt.Sprintf("header error class=0x%02X code=0x%02X", class, code)
	case ErrClassAccess:
		switch code {
		case 0x00:
			return "no access rights"
		case 0x01:
			return "invalid address"
		case 0x04:
			return "invalid data type"
		default:
			return fmt.Sprintf("access error code=0x%02X", code)
		}
	case ErrClassObject:
		return "object does not exist"
	case ErrClassResource:
		return "resource busy"
	case ErrClassApp, ErrClassService, ErrClassSupplies:
		return fmt.Sprintf("S7 error class=0x%02X code=0x%02X", class, code)
	default:
		return fmt.Sprintf("S7 error class=0x%02X code=0x%02X", class, code)
	}
}

// ItemReturnCodeString returns a human-readable string for Read/Write item return code.
func ItemReturnCodeString(code byte) string {
	switch code {
	case RetCodeSuccess:
		return "success"
	case RetCodeHWFault:
		return "hardware fault"
	case RetCodeAccessFault:
		return "access denied"
	case RetCodeAddressFault:
		return "invalid address"
	case RetCodeDataTypeFault:
		return "data type not supported"
	case RetCodeDataSizeFault:
		return "data size mismatch"
	case RetCodeBusy:
		return "object busy"
	case RetCodeNotAvailable:
		return "object not available"
	default:
		return fmt.Sprintf("return code 0x%02X", code)
	}
}

// ReturnCodeError returns an error for an item return code. Preserves raw Code in S7Error (Class zero).
func ReturnCodeError(code byte) error {
	if code == RetCodeSuccess {
		return nil
	}
	return &S7Error{Code: code, Message: ItemReturnCodeString(code)}
}
