// SPDX-License-Identifier: MIT

package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Server-side codec: decoders for the jobs a client sends and encoders for
// the Ack-Data answers. Layouts follow the client encoders in this package;
// response transport sizes and item codes follow the Snap7 server
// (SCADACS/snap7 f6ff903, s7_server.cpp ReadArea/WriteArea).

// ErrMalformedRequest: a job's parameter or data section does not match its
// declared layout. A server cannot answer it item by item.
var ErrMalformedRequest = errors.New("malformed S7 request")

// AckDataHeaderSize is the S7 header of an Ack-Data PDU: the 10-byte header
// plus error class and error code.
const AckDataHeaderSize = S7HeaderSize + 2

// Data section transport sizes (Read Var answers, Write Var data items).
const (
	DataTransportSizeByte  = 0x04 // byte/word/dword; length in bits
	DataTransportSizeInt   = 0x05 // int/dint; length in bits
	DataTransportSizeReal  = 0x07 // real; length in bytes
	DataTransportSizeOctet = 0x09 // octet string; length in bytes
)

// SetupCommRequest holds the parameters of a Setup Communication job.
type SetupCommRequest struct {
	MaxAmqCalling int
	MaxAmqCalled  int
	PDUSize       int
}

// ParseSetupCommRequest decodes the parameter section of a Setup Communication job.
func ParseSetupCommRequest(param []byte) (*SetupCommRequest, error) {
	if len(param) < 8 {
		return nil, fmt.Errorf("%w: setup communication parameter %d bytes, need 8", ErrMalformedRequest, len(param))
	}
	if param[0] != FuncSetupComm {
		return nil, fmt.Errorf("%w: function 0x%02X is not setup communication", ErrMalformedRequest, param[0])
	}
	return &SetupCommRequest{
		MaxAmqCalling: int(binary.BigEndian.Uint16(param[2:4])),
		MaxAmqCalled:  int(binary.BigEndian.Uint16(param[4:6])),
		PDUSize:       int(binary.BigEndian.Uint16(param[6:8])),
	}, nil
}

// EncodeSetupCommResponse builds the Ack-Data answer to Setup Communication.
// Each value must be 0..65535 (ErrOutOfRange otherwise).
func EncodeSetupCommResponse(pduRef uint16, maxAmqCalling, maxAmqCalled, pduSize int) ([]byte, error) {
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
	binary.BigEndian.PutUint16(param[2:4], calling)
	binary.BigEndian.PutUint16(param[4:6], called)
	binary.BigEndian.PutUint16(param[6:8], size)
	return EncodeAckData(pduRef, 0, 0, param, nil)
}

// EncodeAckData builds an Ack-Data PDU (12-byte header with error class and code).
func EncodeAckData(pduRef uint16, errClass, errCode byte, param, data []byte) ([]byte, error) {
	header, err := EncodeS7Header(ROSCTRAckData, pduRef, len(param), len(data))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, AckDataHeaderSize+len(param)+len(data))
	out = append(out, header...)
	out = append(out, errClass, errCode)
	out = append(out, param...)
	return append(out, data...), nil
}

// VarSpec is one item of a Read Var or Write Var job. The raw fields are as
// on the wire; Syntax other than SyntaxIDS7Any leaves the address fields zero.
type VarSpec struct {
	Syntax        byte
	TransportSize byte   // request transport size (TransportSize* constants)
	Count         uint16 // element count
	DBNumber      uint16
	Area          byte
	StartBit      uint32 // 24-bit address in bits
}

// varSpecSize is the length of one S7ANY item specification.
const varSpecSize = 12

// parseVarSpecs decodes the item list of a Read Var or Write Var parameter
// section. An item that is not S7ANY keeps its length so the others still
// decode; its address fields stay zero.
func parseVarSpecs(param []byte, fn byte) ([]VarSpec, error) {
	if len(param) < 2 {
		return nil, fmt.Errorf("%w: parameter %d bytes, need 2", ErrMalformedRequest, len(param))
	}
	if param[0] != fn {
		return nil, fmt.Errorf("%w: function 0x%02X, want 0x%02X", ErrMalformedRequest, param[0], fn)
	}
	n := int(param[1])
	if n == 0 {
		return nil, fmt.Errorf("%w: no items", ErrMalformedRequest)
	}
	specs := make([]VarSpec, 0, n)
	off := 2
	for i := range n {
		if off+2 > len(param) {
			return nil, fmt.Errorf("%w: item %d truncated", ErrMalformedRequest, i)
		}
		if param[off] != 0x12 {
			return nil, fmt.Errorf("%w: item %d specification type 0x%02X", ErrMalformedRequest, i, param[off])
		}
		end := off + 2 + int(param[off+1])
		if end > len(param) {
			return nil, fmt.Errorf("%w: item %d length %d overruns the parameter", ErrMalformedRequest, i, param[off+1])
		}
		spec := VarSpec{}
		if param[off+1] >= 1 {
			spec.Syntax = param[off+2]
		}
		if spec.Syntax == SyntaxIDS7Any {
			if end-off != varSpecSize {
				return nil, fmt.Errorf("%w: item %d S7ANY length %d", ErrMalformedRequest, i, param[off+1])
			}
			b := param[off:end]
			spec.TransportSize = b[3]
			spec.Count = binary.BigEndian.Uint16(b[4:6])
			spec.DBNumber = binary.BigEndian.Uint16(b[6:8])
			spec.Area = b[8]
			spec.StartBit = uint32(b[9])<<16 | uint32(b[10])<<8 | uint32(b[11])
		}
		specs = append(specs, spec)
		off = end
	}
	if off != len(param) {
		return nil, fmt.Errorf("%w: %d bytes after the items", ErrMalformedRequest, len(param)-off)
	}
	return specs, nil
}

// ParseReadVarRequest decodes the parameter section of a Read Var job.
func ParseReadVarRequest(param []byte) ([]VarSpec, error) {
	return parseVarSpecs(param, FuncReadVar)
}

// WriteVarData is one data item of a Write Var job.
type WriteVarData struct {
	ReturnCode    byte // reserved, 0x00 in a request
	TransportSize byte // data transport size (DataTransportSize* constants)
	RawLength     uint16
	Data          []byte // payload, aliases the request
}

// writeDataBytes is the payload length of a Write Var data item: bits for
// byte and integer transports, bytes for bit, real and octet (Snap7 WriteArea).
func writeDataBytes(transport byte, raw uint16) (int, error) {
	switch transport {
	case DataTransportSizeBit, DataTransportSizeReal, DataTransportSizeOctet:
		return int(raw), nil
	case DataTransportSizeByte, DataTransportSizeInt:
		if raw%8 != 0 {
			return 0, fmt.Errorf("%w: length %d bits is not whole bytes", ErrMalformedRequest, raw)
		}
		return int(raw) / 8, nil
	default:
		return 0, fmt.Errorf("%w: data transport size 0x%02X", ErrMalformedRequest, transport)
	}
}

// ParseWriteVarRequest decodes a Write Var job: one data item per item
// specification, each padded to an even length except the last.
func ParseWriteVarRequest(param, data []byte) ([]VarSpec, []WriteVarData, error) {
	specs, err := parseVarSpecs(param, FuncWriteVar)
	if err != nil {
		return nil, nil, err
	}
	items := make([]WriteVarData, 0, len(specs))
	off := 0
	for i := range specs {
		if off+4 > len(data) {
			return nil, nil, fmt.Errorf("%w: data item %d header truncated", ErrMalformedRequest, i)
		}
		item := WriteVarData{
			ReturnCode:    data[off],
			TransportSize: data[off+1],
			RawLength:     binary.BigEndian.Uint16(data[off+2 : off+4]),
		}
		n, err := writeDataBytes(item.TransportSize, item.RawLength)
		if err != nil {
			return nil, nil, fmt.Errorf("data item %d: %w", i, err)
		}
		start := off + 4
		if start+n > len(data) {
			return nil, nil, fmt.Errorf("%w: data item %d payload truncated", ErrMalformedRequest, i)
		}
		item.Data = data[start : start+n]
		items = append(items, item)
		off = start + n
		if n%2 != 0 && i < len(specs)-1 {
			off++ // fill byte before the next item
		}
	}
	if off > len(data) || len(data)-off > 1 {
		return nil, nil, fmt.Errorf("%w: %d bytes after the data items", ErrMalformedRequest, len(data)-off)
	}
	return specs, items, nil
}

// ReadVarResult is one item of a Read Var answer. RawLength is the wire
// length field (bits or bytes, by transport size); a failed item has
// TransportSize 0x00, RawLength 0 and no Data.
type ReadVarResult struct {
	ReturnCode    byte
	TransportSize byte
	RawLength     uint16
	Data          []byte
}

// ReadVarResultSize is the number of bytes item takes in a Read Var answer,
// with the fill byte that precedes a following item when last is false.
func ReadVarResultSize(item ReadVarResult, last bool) int {
	n := 4 + len(item.Data)
	if !last && len(item.Data)%2 != 0 {
		n++
	}
	return n
}

// EncodeReadVarResponse builds the Ack-Data answer to Read Var. At most
// MaxReadVarItems items.
func EncodeReadVarResponse(pduRef uint16, items []ReadVarResult) ([]byte, error) {
	n := len(items)
	if n > math.MaxUint8 {
		return nil, fmt.Errorf("%w: %d items, at most %d", ErrOutOfRange, n, MaxReadVarItems)
	}
	var data []byte
	for i, item := range items {
		data = append(data, item.ReturnCode, item.TransportSize)
		data = binary.BigEndian.AppendUint16(data, item.RawLength)
		data = append(data, item.Data...)
		if i < n-1 && len(item.Data)%2 != 0 {
			data = append(data, 0x00)
		}
	}
	return EncodeAckData(pduRef, 0, 0, []byte{FuncReadVar, uint8(n)}, data)
}

// EncodeWriteVarResponse builds the Ack-Data answer to Write Var: one return
// code per item. At most MaxReadVarItems items.
func EncodeWriteVarResponse(pduRef uint16, codes []byte) ([]byte, error) {
	n := len(codes)
	if n > math.MaxUint8 {
		return nil, fmt.Errorf("%w: %d items, at most %d", ErrOutOfRange, n, MaxReadVarItems)
	}
	return EncodeAckData(pduRef, 0, 0, []byte{FuncWriteVar, uint8(n)}, codes)
}
