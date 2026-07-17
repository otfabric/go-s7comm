package wire

import (
	"fmt"

	"github.com/otfabric/go-cotp"
)

// FrameSummary captures key protocol fields from one COTP TPDU that may carry an S7 PDU.
type FrameSummary struct {
	TPDULength  int
	COTPType    byte
	ROSCTR      byte
	Function    byte
	ParamLength int
	DataLength  int
	ErrorClass  byte
	ErrorCode   byte
}

// InspectTPDU decodes a COTP TPDU payload (no TPKT header) and extracts high-level S7 metadata when present.
// This is a diagnostic helper for offline captures; the live client path uses cotp.Conn TSDUs.
// For full TPKT captures, peel the TPKT header with go-tpkt first, then pass the TPDU payload here.
func InspectTPDU(tpdu []byte) (*FrameSummary, error) {
	dec, err := cotp.Decode(tpdu)
	if err != nil {
		return nil, fmt.Errorf("parse cotp: %w", err)
	}

	s := &FrameSummary{
		TPDULength: len(tpdu),
		COTPType:   byte(dec.Type),
	}

	var s7Payload []byte
	if dec.DT != nil {
		s7Payload = dec.DT.UserData
	}
	if len(s7Payload) == 0 || s7Payload[0] != 0x32 {
		return s, nil
	}

	h, rest, err := ParseS7Header(s7Payload)
	if err != nil {
		return nil, fmt.Errorf("parse s7 header: %w", err)
	}

	s.ROSCTR = byte(h.ROSCTR)
	s.ParamLength = int(h.ParamLength)
	s.DataLength = int(h.DataLength)
	s.ErrorClass = h.ErrorClass
	s.ErrorCode = h.ErrorCode
	if len(rest) > 0 {
		s.Function = rest[0]
	}

	return s, nil
}
