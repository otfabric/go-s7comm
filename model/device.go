// SPDX-License-Identifier: MIT

package model

// DeviceInfo contains identification information about an S7 device
type DeviceInfo struct {
	OrderNumber  string
	SerialNumber string
	ModuleName   string
	PlantID      string
	Copyright    string
	ModuleType   string
	FWVersion    string
	HWVersion    string
	CPUType      string
	CPUFamily    string
}

// ConnectionInfo contains a snapshot of the established connection and negotiated limits.
// Obtained from client.ConnectionInfo(); safe to read when connected.
// PDUSize is the negotiated maximum S7 PDU payload length in bytes (excluding TPKT/COTP framing).
type ConnectionInfo struct {
	Host          string
	Port          int
	LocalTSAP     uint16
	RemoteTSAP    uint16
	Rack          int
	Slot          int
	PDUSize       int // negotiated max S7 PDU payload (bytes), excluding TPKT/COTP
	MaxAmqCalling int
	MaxAmqCalled  int
}

// CPUState represents the current state of the PLC CPU
type CPUState uint8

const (
	CPUStateUnknown CPUState = 0
	CPUStateRun     CPUState = 1
	CPUStateStop    CPUState = 2
	CPUStateStartup CPUState = 3
	CPUStateHold    CPUState = 4
)

func (s CPUState) String() string {
	switch s {
	case CPUStateRun:
		return "RUN"
	case CPUStateStop:
		return "STOP"
	case CPUStateStartup:
		return "STARTUP"
	case CPUStateHold:
		return "HOLD"
	default:
		return "UNKNOWN"
	}
}

// ProtectionLevel indicates the access protection level
type ProtectionLevel uint8

const (
	ProtectionNone      ProtectionLevel = 0
	ProtectionRead      ProtectionLevel = 1
	ProtectionReadWrite ProtectionLevel = 2
	ProtectionFull      ProtectionLevel = 3
)

func (p ProtectionLevel) String() string {
	switch p {
	case ProtectionNone:
		return "No Protection"
	case ProtectionRead:
		return "Write Protected"
	case ProtectionReadWrite:
		return "Read/Write Protected"
	case ProtectionFull:
		return "Full Protection"
	default:
		return "Unknown"
	}
}

// DiagEntry represents a diagnostic buffer entry
type DiagEntry struct {
	Timestamp   string
	EventID     uint16
	EventClass  uint8
	Priority    uint8
	Description string
	Info1       uint32
	Info2       uint32
}

// DiagBuffer represents the diagnostic buffer
type DiagBuffer struct {
	Entries    []DiagEntry
	TotalCount int
}
