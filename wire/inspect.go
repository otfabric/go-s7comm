package wire

import (
	"fmt"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-tpkt"
)

// FrameSummary captures key protocol fields from one S7-over-TPKT frame.
type FrameSummary struct {
	TPKTLength  int
	COTPType    byte
	ROSCTR      byte
	Function    byte
	ParamLength int
	DataLength  int
	ErrorClass  byte
	ErrorCode   byte
}

// InspectFrame decodes a full TPKT frame (go-tpkt) and COTP (go-cotp) and extracts high-level protocol metadata.
// This is a diagnostic helper for offline captures; the live client path uses cotp.Conn TSDUs.
func InspectFrame(frame []byte) (*FrameSummary, error) {
	payload, err := tpkt.DecodePacket(frame)
	if err != nil {
		return nil, fmt.Errorf("parse tpkt: %w", err)
	}

	dec, err := cotp.Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("parse cotp: %w", err)
	}

	s := &FrameSummary{
		TPKTLength: len(frame),
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
