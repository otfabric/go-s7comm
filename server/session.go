// SPDX-License-Identifier: MIT

package server

import (
	"encoding/binary"
	"log/slog"
	"math"

	"github.com/otfabric/go-s7comm/wire"
)

// Header error answers (class, code), with their meaning in the Siemens
// parameter error list as Wireshark's s7comm dissector names it.
const (
	errClassApp      = 0x81 // with 0x04: "This service is not implemented on the module or a frame error was reported"
	errCodeNotImpl   = 0x04
	errClassSupplies = 0x85 // with 0x00: "S7 protocol error: Wrong frames"; the answer would not fit the PDU
	errCodeFrames    = 0x00
)

// maxAmQ is the number of jobs the server processes in parallel per
// connection: one, as it answers each job before reading the next.
const maxAmQ = 1

// session is the per-connection state: the negotiated PDU size.
type session struct {
	srv     *Server
	pduSize int
	log     *slog.Logger
}

// handle answers one S7 PDU. keep is false when the header itself cannot
// be read (protocol ID, lengths), so no answer can be addressed and the
// connection is dropped. A readable header always gets an answer: an
// unsupported or malformed job gets header error 0x8104.
func (s *session) handle(req []byte) (out []byte, keep bool) {
	if len(req) < wire.S7HeaderSize || req[0] != 0x32 {
		return nil, false
	}
	rosctr := wire.ROSCTR(req[1])
	ref := binary.BigEndian.Uint16(req[4:6])
	paramLen := int(binary.BigEndian.Uint16(req[6:8]))
	dataLen := int(binary.BigEndian.Uint16(req[8:10]))
	if wire.S7HeaderSize+paramLen+dataLen != len(req) {
		return nil, false
	}
	param := req[wire.S7HeaderSize : wire.S7HeaderSize+paramLen]
	data := req[wire.S7HeaderSize+paramLen:]
	if rosctr.IsUserdata() {
		s.log.Debug("s7 user data not implemented", "pduRef", ref)
		return s.userdataNotImplemented(ref, param), true
	}
	if !rosctr.IsJob() || paramLen == 0 {
		s.log.Debug("s7 PDU is not a job", "pduRef", ref, "rosctr", byte(rosctr))
		return s.notImplemented(ref, param), true
	}
	var err error
	switch param[0] {
	case wire.FuncSetupComm:
		out, err = s.setup(ref, param)
	case wire.FuncReadVar:
		out, err = s.read(ref, param)
	case wire.FuncWriteVar:
		out, err = s.write(ref, param, data)
	default:
		s.log.Debug("s7 job not implemented", "pduRef", ref, "function", wire.FunctionCodeString(param[0]))
		return s.notImplemented(ref, param), true
	}
	if err != nil {
		s.log.Debug("s7 job malformed", "pduRef", ref, "error", err)
		return s.notImplemented(ref, param), true
	}
	return out, true
}

// notImplemented answers with header error 0x8104 and the job's function
// code as parameter, when there is one.
func (s *session) notImplemented(ref uint16, param []byte) []byte {
	var fn []byte
	if len(param) > 0 {
		fn = param[:1]
	}
	out, _ := wire.EncodeAckData(ref, errClassApp, errCodeNotImpl, fn, nil)
	return out
}

// userdataNotImplemented answers a user data request with a user data
// response carrying parameter error 0x8104 and item code 0x0A (object does
// not exist), keeping the request's function group, subfunction and
// sequence number. A parameter too short to be a user data request gets
// header error 0x8104 instead.
func (s *session) userdataNotImplemented(ref uint16, param []byte) []byte {
	if len(param) < 8 || param[0] != 0x00 || param[1] != 0x01 || param[2] != 0x12 {
		return s.notImplemented(ref, param)
	}
	group, subfunc, seq := param[5]&0x3F, param[6], param[7]
	resp := []byte{0x00, 0x01, 0x12, 0x08, 0x12, 0x80 | group, subfunc, seq, 0x00, 0x00, errClassApp, errCodeNotImpl}
	data := []byte{wire.RetCodeNotAvailable, 0x00, 0x00, 0x00}
	hdr, _ := wire.EncodeS7Header(wire.ROSCTRUserdata, ref, len(resp), len(data))
	return append(append(hdr, resp...), data...)
}

// setup grants the smaller of the requested and the configured PDU size
// and of the requested and maxAmQ parallel jobs: negotiation can only
// lower what the client asked for.
func (s *session) setup(ref uint16, param []byte) ([]byte, error) {
	req, err := wire.ParseSetupCommRequest(param)
	if err != nil {
		return nil, err
	}
	size := min(req.PDUSize, s.srv.cfg.MaxPDUSize)
	s.pduSize = size
	s.log.Debug("s7 setup communication", "requested", req.PDUSize, "granted", size)
	return wire.EncodeSetupCommResponse(ref, max(1, min(req.MaxAmqCalling, maxAmQ)), max(1, min(req.MaxAmqCalled, maxAmQ)), size)
}

// elementSize is the bytes per element of a request transport size, 0 for
// one the server does not take (timers and counters are not held).
func elementSize(transport byte) int {
	switch transport {
	case wire.TransportSizeBit, wire.TransportSizeByte, wire.TransportSizeChar:
		return 1
	case wire.TransportSizeWord, wire.TransportSizeInt:
		return 2
	case wire.TransportSizeDWord, wire.TransportSizeDInt, wire.TransportSizeReal:
		return 4
	}
	return 0
}

// target is a decoded item address.
type target struct {
	reg    *region
	start  int // byte offset
	bit    int // bit index for a BIT item
	size   int // bytes
	isBit  bool
	transp byte
}

// resolve checks one item against memory and returns the item return code
// that refuses it, or RetCodeSuccess and its target. Codes as the Siemens
// item return values name them: 0x0A "object does not exist" for an area or
// DB the server does not hold; 0x06 "data type not supported" for a
// transport size it does not take; 0x05 "invalid address" for an item that
// is not S7ANY, a zero count, more than one bit, a byte item that does not
// start on a byte, or a span past the end of the area.
func (s *session) resolve(spec wire.VarSpec) (target, byte) {
	if spec.Syntax != wire.SyntaxIDS7Any {
		return target{}, wire.RetCodeAddressFault
	}
	area := Area(spec.Area)
	key, err := keyOf(area, int(spec.DBNumber))
	if err != nil {
		return target{}, wire.RetCodeNotAvailable
	}
	reg, ok := s.srv.mem.regions[key]
	if !ok {
		return target{}, wire.RetCodeNotAvailable
	}
	per := elementSize(spec.TransportSize)
	if per == 0 {
		return target{}, wire.RetCodeDataTypeFault
	}
	t := target{reg: reg, size: per * int(spec.Count), transp: spec.TransportSize}
	t.isBit = spec.TransportSize == wire.TransportSizeBit
	if t.size == 0 || (t.isBit && t.size > 1) {
		return target{}, wire.RetCodeAddressFault
	}
	if !t.isBit && spec.StartBit%8 != 0 {
		return target{}, wire.RetCodeAddressFault
	}
	t.start = int(spec.StartBit >> 3)
	t.bit = int(spec.StartBit & 0x07)
	if t.start+t.size > len(reg.data) {
		return target{}, wire.RetCodeAddressFault
	}
	return t, wire.RetCodeSuccess
}

// lengthField converts an item length for its 16-bit wire field. Callers
// pass lengths already bounded by the PDU size (at most MaxPDUSize bytes,
// so at most 32768 bits).
func lengthField(n int) uint16 {
	if n < 0 || n > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(n)
}

// readResult builds the answer item for a resolved target. Data transport
// sizes as Wireshark's s7comm dissector names them: BIT 0x03 (length in
// bits), BYTE/WORD/DWORD 0x04 (bits), INTEGER 0x05 (bits), DINTEGER 0x06
// (bytes), REAL 0x07 (bytes), OCTET STRING 0x09 (bytes) for CHAR.
func readResult(t target) wire.ReadVarResult {
	src := t.reg.data[t.start : t.start+t.size]
	r := wire.ReadVarResult{ReturnCode: wire.RetCodeSuccess, Data: append([]byte(nil), src...)}
	switch t.transp {
	case wire.TransportSizeBit:
		r.TransportSize, r.RawLength, r.Data = wire.DataTransportSizeBit, 1, []byte{(src[0] >> t.bit) & 0x01}
	case wire.TransportSizeInt:
		r.TransportSize, r.RawLength = wire.DataTransportSizeInt, lengthField(t.size*8)
	case wire.TransportSizeDInt:
		r.TransportSize, r.RawLength = wire.DataTransportSizeDInt, lengthField(t.size)
	case wire.TransportSizeReal:
		r.TransportSize, r.RawLength = wire.DataTransportSizeReal, lengthField(t.size)
	case wire.TransportSizeChar:
		r.TransportSize, r.RawLength = wire.DataTransportSizeOctet, lengthField(t.size)
	default:
		r.TransportSize, r.RawLength = wire.DataTransportSizeByte, lengthField(t.size*8)
	}
	return r
}

// read answers Read Var, one result per item. When the answer would exceed
// the negotiated PDU size the whole job is refused with header error
// 0x8500, so the client can split it.
func (s *session) read(ref uint16, param []byte) ([]byte, error) {
	specs, err := wire.ParseReadVarRequest(param)
	if err != nil {
		return nil, err
	}
	s.srv.mem.mu.RLock()
	defer s.srv.mem.mu.RUnlock()
	used := wire.AckDataHeaderSize + 2
	results := make([]wire.ReadVarResult, len(specs))
	for i, spec := range specs {
		t, code := s.resolve(spec)
		if code != wire.RetCodeSuccess {
			results[i] = wire.ReadVarResult{ReturnCode: code}
			used += 4
			continue
		}
		need := 4 + t.size
		if t.isBit {
			need = 5
		}
		if i < len(specs)-1 && need%2 != 0 {
			need++
		}
		used += need
		if used > s.pduSize {
			return wire.EncodeAckData(ref, errClassSupplies, errCodeFrames, param[:1], nil)
		}
		results[i] = readResult(t)
	}
	if used > s.pduSize {
		return wire.EncodeAckData(ref, errClassSupplies, errCodeFrames, param[:1], nil)
	}
	return wire.EncodeReadVarResponse(ref, results)
}

// write answers Write Var with one return code per item: refusals as in
// resolve, 0x07 "data type inconsistent" for data whose length is not the
// addressed size, 0x03 "accessing the object not allowed" for a read-only
// area.
func (s *session) write(ref uint16, param, data []byte) ([]byte, error) {
	specs, items, err := wire.ParseWriteVarRequest(param, data)
	if err != nil {
		return nil, err
	}
	s.srv.mem.mu.Lock()
	defer s.srv.mem.mu.Unlock()
	codes := make([]byte, len(specs))
	for i, spec := range specs {
		codes[i] = s.writeItem(spec, items[i])
	}
	return wire.EncodeWriteVarResponse(ref, codes)
}

func (s *session) writeItem(spec wire.VarSpec, item wire.WriteVarData) byte {
	t, code := s.resolve(spec)
	if code != wire.RetCodeSuccess {
		return code
	}
	if len(item.Data) != t.size {
		return wire.RetCodeDataSizeFault
	}
	if t.reg.readOnly {
		return wire.RetCodeAccessFault
	}
	if t.isBit {
		mask := byte(1) << t.bit
		if item.Data[0]&0x01 != 0 {
			t.reg.data[t.start] |= mask
		} else {
			t.reg.data[t.start] &^= mask
		}
		return wire.RetCodeSuccess
	}
	copy(t.reg.data[t.start:], item.Data)
	return wire.RetCodeSuccess
}
