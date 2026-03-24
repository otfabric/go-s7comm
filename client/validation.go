package client

import (
	"fmt"
	"strings"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// ValidationError indicates invalid caller input (e.g. negative start/size, invalid range).
// Use errors.As(err, &ValidationError{}) to distinguish validation failures from transport or protocol errors.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// validateAddress checks Address fields for read/write. Returns an error for invalid input.
// Caller/input validation errors are returned as error; use before ReadArea/WriteArea.
func validateAddress(addr model.Address) error {
	if addr.Start < 0 {
		return &ValidationError{Message: fmt.Sprintf("start offset must be >= 0, got %d", addr.Start)}
	}
	if addr.Size < 0 {
		return &ValidationError{Message: fmt.Sprintf("size must be >= 0, got %d", addr.Size)}
	}
	if addr.Area == model.AreaDB && addr.DBNumber < 0 {
		return &ValidationError{Message: fmt.Sprintf("DB number must be >= 0, got %d", addr.DBNumber)}
	}
	return nil
}

// validateCompareReadRequest checks CompareReadRequest fields. Returns an error for invalid input.
// Zero candidates is allowed and yields an empty result.
func validateCompareReadRequest(req CompareReadRequest) error {
	if strings.TrimSpace(req.Address) == "" {
		return &ValidationError{Message: "address must be non-empty"}
	}
	if req.Port < 0 {
		return &ValidationError{Message: fmt.Sprintf("port must be >= 0, got %d", req.Port)}
	}
	if req.Timeout < 0 {
		return &ValidationError{Message: fmt.Sprintf("timeout must be >= 0, got %v", req.Timeout)}
	}
	if req.Size < 0 {
		return &ValidationError{Message: fmt.Sprintf("size must be >= 0, got %d", req.Size)}
	}
	if req.Offset < 0 {
		return &ValidationError{Message: fmt.Sprintf("offset must be >= 0, got %d", req.Offset)}
	}
	if req.Area == model.AreaDB && req.DBNumber < 0 {
		return &ValidationError{Message: fmt.Sprintf("DB number must be >= 0, got %d", req.DBNumber)}
	}
	for i, cand := range req.Candidates {
		if err := wire.ValidateRackSlot(cand.Rack, cand.Slot); err != nil {
			return &ValidationError{Message: fmt.Sprintf("candidate %d: %s", i, err.Error())}
		}
	}
	return nil
}

// validateRangeProbeRequest checks RangeProbeRequest fields. Returns an error for invalid input.
func validateRangeProbeRequest(req RangeProbeRequest) error {
	if req.Start > req.End {
		return &ValidationError{Message: fmt.Sprintf("start must be <= end, got start=%d end=%d", req.Start, req.End)}
	}
	if req.Area == model.AreaDB && req.DBNumber < 0 {
		return &ValidationError{Message: fmt.Sprintf("DB number must be >= 0, got %d", req.DBNumber)}
	}
	if req.ProbeSize < 0 {
		return &ValidationError{Message: fmt.Sprintf("probe size must be >= 0, got %d", req.ProbeSize)}
	}
	if req.Retries < 0 {
		return &ValidationError{Message: fmt.Sprintf("retries must be >= 0, got %d", req.Retries)}
	}
	if req.Repeat < 0 {
		return &ValidationError{Message: fmt.Sprintf("repeat must be >= 0, got %d", req.Repeat)}
	}
	if req.Parallelism < 0 {
		return &ValidationError{Message: fmt.Sprintf("parallelism must be >= 0, got %d", req.Parallelism)}
	}
	return nil
}

// validateRackSlotProbeRequest checks RackSlotProbeRequest fields. Returns an error for invalid input.
func validateRackSlotProbeRequest(req RackSlotProbeRequest) error {
	if strings.TrimSpace(req.Address) == "" {
		return &ValidationError{Message: "address must be non-empty"}
	}
	if req.Port < 0 || req.Port > 65535 {
		return &ValidationError{Message: fmt.Sprintf("port must be 0..65535, got %d", req.Port)}
	}
	if req.RackMin < 0 || req.RackMin > 7 {
		return &ValidationError{Message: fmt.Sprintf("rack min must be 0..7, got %d", req.RackMin)}
	}
	if req.RackMax < 0 || req.RackMax > 7 {
		return &ValidationError{Message: fmt.Sprintf("rack max must be 0..7, got %d", req.RackMax)}
	}
	if req.SlotMin < 0 || req.SlotMin > 31 {
		return &ValidationError{Message: fmt.Sprintf("slot min must be 0..31, got %d", req.SlotMin)}
	}
	if req.SlotMax < 0 || req.SlotMax > 31 {
		return &ValidationError{Message: fmt.Sprintf("slot max must be 0..31, got %d", req.SlotMax)}
	}
	if req.RackMin > req.RackMax {
		return &ValidationError{Message: fmt.Sprintf("rack min must be <= rack max, got min=%d max=%d", req.RackMin, req.RackMax)}
	}
	if req.SlotMin > req.SlotMax {
		return &ValidationError{Message: fmt.Sprintf("slot min must be <= slot max, got min=%d max=%d", req.SlotMin, req.SlotMax)}
	}
	if req.Timeout < 0 {
		return &ValidationError{Message: fmt.Sprintf("timeout must be >= 0, got %v", req.Timeout)}
	}
	if req.Retries < 0 {
		return &ValidationError{Message: fmt.Sprintf("retries must be >= 0, got %d", req.Retries)}
	}
	if req.DelayMS < 0 {
		return &ValidationError{Message: fmt.Sprintf("delay ms must be >= 0, got %d", req.DelayMS)}
	}
	if req.Parallelism < 0 {
		return &ValidationError{Message: fmt.Sprintf("parallelism must be >= 0, got %d", req.Parallelism)}
	}
	if req.JitterMS < 0 {
		return &ValidationError{Message: fmt.Sprintf("jitter ms must be >= 0, got %d", req.JitterMS)}
	}
	if req.MaxAttemptsPerHost < 0 {
		return &ValidationError{Message: fmt.Sprintf("max attempts per host must be >= 0, got %d", req.MaxAttemptsPerHost)}
	}
	return nil
}
