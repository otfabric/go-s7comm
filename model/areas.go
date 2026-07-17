// Package model defines data types for S7 communications.
package model

// Area represents an S7 memory area
type Area uint8

const (
	AreaInputs  Area = 0x81
	AreaOutputs Area = 0x82
	AreaMerkers Area = 0x83
	AreaDB      Area = 0x84
	AreaCounter Area = 0x1C
	AreaTimer   Area = 0x1D
)

// String returns the area code as a string
func (a Area) String() string {
	switch a {
	case AreaInputs:
		return "I"
	case AreaOutputs:
		return "Q"
	case AreaMerkers:
		return "M"
	case AreaDB:
		return "DB"
	case AreaCounter:
		return "C"
	case AreaTimer:
		return "T"
	default:
		return "?"
	}
}

// Address represents an S7 variable address
type Address struct {
	Area     Area
	DBNumber int // Only used for DB area
	Start    int // Byte offset
	Size     int // Number of bytes
}

// BitAddress identifies a single S7 bit (e.g. DB1.DBX10.3 → DBNumber=1, ByteOffset=10, BitOffset=3).
// BitOffset must be in 0..7; invalid offsets are rejected by the client (no silent wrap).
type BitAddress struct {
	Area       Area
	DBNumber   int
	ByteOffset int
	BitOffset  int
}
