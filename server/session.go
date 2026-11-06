// SPDX-License-Identifier: MIT

package server

import (
	"encoding/binary"
	"log/slog"
	"math"

	"github.com/otfabric/go-s7comm/wire"
)

// maxItems is the item limit of one Read Var or Write Var job. The Snap7
// server answers at most MaxVars (20) items of a read and drops the rest.
const maxItems = 20

// session is the per-connection state: the negotiated PDU size.
type session struct {
	srv     *Server
	pduSize int
	log     *slog.Logger
}

// handle answers one S7 PDU. keep is false when the PDU is malformed and
// the connection must be dropped (Snap7: CheckPDU_in fails). out is nil
// when the job gets no answer.
func (s *session) handle(req []byte) (out []byte, keep bool) {
	if len(req) < wire.S7HeaderSize || req[0] != 0x32 {
		return nil, false
	}
	rosctr := wire.ROSCTR(req[1])
	if !rosctr.IsJob() && !rosctr.IsUserdata() {
		return nil, false
	}
	ref := binary.BigEndian.Uint16(req[4:6])
	paramLen := int(binary.BigEndian.Uint16(req[6:8]))
	dataLen := int(binary.BigEndian.Uint16(req[8:10]))
	if wire.S7HeaderSize+paramLen+dataLen != len(req) || paramLen == 0 {
		return nil, false
	}
	param := req[wire.S7HeaderSize : wire.S7HeaderSize+paramLen]
	data := req[wire.S7HeaderSize+paramLen:]
	if rosctr.IsUserdata() {
		s.log.Debug("s7 user data not answered", "pduRef", ref)
		return nil, true
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
		s.log.Debug("s7 job not answered", "pduRef", ref, "function", wire.FunctionCodeString(param[0]))
		return nil, true
	}
	if err != nil {
		s.log.Debug("s7 job malformed", "pduRef", ref, "error", err)
		return nil, false
	}
	return out, true
}

// setup grants the requested PDU size clamped into MinPDUSize..MaxPDUSize
// and echoes the AmQ values (Snap7 PerformFunctionNegotiate).
func (s *session) setup(ref uint16, param []byte) ([]byte, error) {
	req, err := wire.ParseSetupCommRequest(param)
	if err != nil {
		return nil, err
	}
	size := min(max(req.PDUSize, MinPDUSize), s.srv.cfg.MaxPDUSize)
	s.pduSize = size
	s.log.Debug("s7 setup communication", "requested", req.PDUSize, "granted", size)
	return wire.EncodeSetupCommResponse(ref, req.MaxAmqCalling, req.MaxAmqCalled, size)
}

// elementSize is the bytes per element of a request transport size, 0 for
// one the server does not take (Snap7 DataSizeByte; timers and counters are
// not held).
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
// that refuses it, or RetCodeSuccess and its target. Codes follow the Snap7
// server: unknown area 0x0A; unknown transport size badTransport (0x05 for
// a read, 0x06 for a write); a zero count, more than one bit, unaligned start or span past
// the end 0x05.
func (s *session) resolve(spec wire.VarSpec, badTransport byte) (target, byte) {
	if spec.Syntax != wire.SyntaxIDS7Any {
		return target{}, wire.RetCodeNotAvailable
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
		return target{}, badTransport
	}
	t := target{reg: reg, size: per * int(spec.Count), transp: spec.TransportSize}
	t.isBit = spec.TransportSize == wire.TransportSizeBit
	// A zero count addresses nothing; Snap7 answers it with whatever its
	// buffer holds, so it is refused here as an address fault.
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

// readResult builds the answer item for a resolved target (Snap7 ReadArea):
// BIT 0x03 (length in bits), INT/DINT 0x05 (bits), REAL 0x07 (bytes),
// CHAR 0x09 (bytes), everything else 0x04 (bits). The caller has checked
// that t.size fits the PDU.
func readResult(t target) wire.ReadVarResult {
	src := t.reg.data[t.start : t.start+t.size]
	r := wire.ReadVarResult{ReturnCode: wire.RetCodeSuccess, Data: append([]byte(nil), src...)}
	switch t.transp {
	case wire.TransportSizeBit:
		r.TransportSize, r.RawLength, r.Data = wire.DataTransportSizeBit, 1, []byte{(src[0] >> t.bit) & 0x01}
	case wire.TransportSizeInt, wire.TransportSizeDInt:
		r.TransportSize, r.RawLength = wire.DataTransportSizeInt, lengthField(t.size*8)
	case wire.TransportSizeReal:
		r.TransportSize, r.RawLength = wire.DataTransportSizeReal, lengthField(t.size)
	case wire.TransportSizeChar:
		r.TransportSize, r.RawLength = wire.DataTransportSizeOctet, lengthField(t.size)
	default:
		r.TransportSize, r.RawLength = wire.DataTransportSizeByte, lengthField(t.size*8)
	}
	return r
}

// read answers Read Var. Items past maxItems are dropped. An item whose
// data would make the answer exceed the negotiated PDU size is refused with
// item code 0x85 (Snap7 RA_SizeOverPDU: the low byte of Code7DataOverPDU
// 0x8500 as Snap7 sends it).
func (s *session) read(ref uint16, param []byte) ([]byte, error) {
	specs, err := wire.ParseReadVarRequest(param)
	if err != nil {
		return nil, err
	}
	if len(specs) > maxItems {
		specs = specs[:maxItems]
	}
	s.srv.mem.mu.RLock()
	defer s.srv.mem.mu.RUnlock()
	used := wire.AckDataHeaderSize + 2
	results := make([]wire.ReadVarResult, len(specs))
	for i, spec := range specs {
		t, code := s.resolve(spec, wire.RetCodeAddressFault)
		if code == wire.RetCodeSuccess {
			need := 4 + t.size
			if t.isBit {
				need = 5
			}
			if i < len(specs)-1 && need%2 != 0 {
				need++
			}
			if used+need <= s.pduSize {
				results[i] = readResult(t)
				used += need
				continue
			}
			code = dataOverPDU
		}
		results[i] = wire.ReadVarResult{ReturnCode: code}
		used += 4
	}
	return wire.EncodeReadVarResponse(ref, results)
}

// dataOverPDU is the item code Snap7 sends for a read that does not fit the PDU.
const dataOverPDU = 0x85

// write answers Write Var with one return code per item (Snap7 WriteArea):
// refusals as in resolve, data length unequal to the addressed size 0x07,
// a read-only area 0x0A.
func (s *session) write(ref uint16, param, data []byte) ([]byte, error) {
	specs, items, err := wire.ParseWriteVarRequest(param, data)
	if err != nil {
		return nil, err
	}
	if len(specs) > maxItems {
		return nil, wire.ErrMalformedRequest
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
	t, code := s.resolve(spec, wire.RetCodeDataTypeFault)
	if code != wire.RetCodeSuccess {
		return code
	}
	if len(item.Data) != t.size {
		return wire.RetCodeDataSizeFault
	}
	if t.reg.readOnly {
		return wire.RetCodeNotAvailable
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
