// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"fmt"
	"sync"

	"github.com/otfabric/go-s7comm/wire"
)

// Area is a memory area the server can hold.
type Area byte

// Areas a server can register. Timers, counters and the other S7 areas are
// not held: a request for them is answered "object not available".
const (
	AreaInputs  Area = wire.AreaInputs  // I (process inputs)
	AreaOutputs Area = wire.AreaOutputs // Q (process outputs)
	AreaMerkers Area = wire.AreaMerkers // M (markers)
	AreaDB      Area = wire.AreaDB      // DB (data blocks, by number)
)

// String returns the S7 name of the area ("I", "Q", "M", "DB").
func (a Area) String() string { return wire.AreaString(byte(a)) }

func (a Area) valid() bool {
	switch a {
	case AreaInputs, AreaOutputs, AreaMerkers, AreaDB:
		return true
	}
	return false
}

// Errors of the host-side memory API.
var (
	// ErrUnknownArea: the area (or DB number) is not registered, or not one the server holds.
	ErrUnknownArea = errors.New("s7 server: area not registered")
	// ErrAreaExists: the area (or DB number) is already registered.
	ErrAreaExists = errors.New("s7 server: area already registered")
	// ErrOutOfRange: the span is not inside the area.
	ErrOutOfRange = errors.New("s7 server: span outside the area")
)

// MaxAreaSize is the largest area: the S7ANY start address is 24 bits in bits.
const MaxAreaSize = wire.MaxS7AnyByteOffset + 1

// AreaOptions are the properties of a registered area.
type AreaOptions struct {
	// ReadOnly makes clients' writes fail with item code 0x03 (Siemens:
	// object access is not allowed). Snap7-based servers send 0x0A for the
	// same case; see ERRORS.md.
	// The host-side Write still changes the area.
	ReadOnly bool
}

type region struct {
	data     []byte
	readOnly bool
}

type areaKey struct {
	area Area
	db   uint16
}

// memory holds the registered areas. One lock guards all of them, so a
// multi-item request sees and changes memory as one step.
type memory struct {
	mu      sync.RWMutex
	regions map[areaKey]*region
}

func newMemory() *memory {
	return &memory{regions: make(map[areaKey]*region)}
}

func keyOf(area Area, db int) (areaKey, error) {
	if !area.valid() {
		return areaKey{}, fmt.Errorf("%w: area %s", ErrUnknownArea, area)
	}
	if area != AreaDB {
		return areaKey{area: area}, nil
	}
	if db < 1 || db > wire.MaxS7AnyDBNumber {
		return areaKey{}, fmt.Errorf("%w: DB number %d not in 1..%d", ErrUnknownArea, db, wire.MaxS7AnyDBNumber)
	}
	return areaKey{area: area, db: uint16(db)}, nil
}

func (m *memory) register(area Area, db int, data []byte, opts AreaOptions) error {
	key, err := keyOf(area, db)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxAreaSize {
		return fmt.Errorf("%w: size %d not in 1..%d", ErrOutOfRange, len(data), MaxAreaSize)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.regions[key]; ok {
		return fmt.Errorf("%w: %s", ErrAreaExists, describe(key))
	}
	m.regions[key] = &region{data: append([]byte(nil), data...), readOnly: opts.ReadOnly}
	return nil
}

func (m *memory) span(area Area, db, start, size int) (*region, error) {
	key, err := keyOf(area, db)
	if err != nil {
		return nil, err
	}
	r, ok := m.regions[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownArea, describe(key))
	}
	if start < 0 || size < 0 || start > len(r.data) || size > len(r.data)-start {
		return nil, fmt.Errorf("%w: %s start %d size %d, area size %d", ErrOutOfRange, describe(key), start, size, len(r.data))
	}
	return r, nil
}

func (m *memory) read(area Area, db, start, size int) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, err := m.span(area, db, start, size)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), r.data[start:start+size]...), nil
}

func (m *memory) write(area Area, db, start int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.span(area, db, start, len(data))
	if err != nil {
		return err
	}
	copy(r.data[start:], data)
	return nil
}

func describe(k areaKey) string {
	if k.area == AreaDB {
		return fmt.Sprintf("DB%d", k.db)
	}
	return k.area.String()
}
