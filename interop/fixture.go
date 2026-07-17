//go:build interop

package interop

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Outcome mirrors snap-interop normalized probe results.
type Outcome string

const (
	OutcomeSuccess           Outcome = "success"
	OutcomeAddressOutOfRange Outcome = "address-out-of-range"
	OutcomeWriteProtected    Outcome = "write-protected"
	OutcomeUnsupported       Outcome = "unsupported"
	OutcomeTransportFailure  Outcome = "transport-failure"
	OutcomeProtocolFailure   Outcome = "protocol-failure"
)

// CompiledFixture is the subset of a snap-interop compiled fixture used by the runner.
type CompiledFixture struct {
	ID           string
	Revision     int
	Rack         int
	Slot         int
	Filename     string
	Expectations []Expectation
}

// Expectation is one black-box step from a compiled fixture.
type Expectation struct {
	Operation   string
	AreaType    string
	AreaNum     int
	Offset      int
	Length      int
	Bit         int
	HasBit      bool
	Clients     int
	Data        []byte
	WantOutcome Outcome
	WantBytes   []byte
	HasBytes    bool
}

func loadAllFixtures(dir string) ([]*CompiledFixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*CompiledFixture
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".compiled.json") {
			continue
		}
		fx, err := loadCompiledFixture(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		fx.Filename = e.Name()
		out = append(out, fx)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no compiled fixtures in %s", dir)
	}
	return out, nil
}

func loadCompiledFixture(path string) (*CompiledFixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	meta, _ := doc["fixture"].(map[string]any)
	server, _ := doc["server"].(map[string]any)
	fx := &CompiledFixture{
		ID:       asString(meta["id"]),
		Revision: asInt(meta["revision"], 1),
		Rack:     asInt(server["rack"], 0),
		Slot:     asInt(server["slot"], 2),
	}
	exps, _ := doc["expectations"].([]any)
	for i, item := range exps {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expectations[%d]: not an object", i)
		}
		exp, err := parseExpectation(m)
		if err != nil {
			return nil, fmt.Errorf("expectations[%d]: %w", i, err)
		}
		fx.Expectations = append(fx.Expectations, exp)
	}
	return fx, nil
}

func parseExpectation(m map[string]any) (Expectation, error) {
	area, _ := m["area"].(map[string]any)
	exp := Expectation{
		Operation: asString(m["operation"]),
		AreaType:  asString(area["type"]),
		AreaNum:   asInt(area["number"], 0),
		Offset:    asInt(m["offset"], 0),
		Length:    asInt(m["length"], 0),
		Clients:   asInt(m["clients"], 0),
	}
	if _, ok := m["bit"]; ok {
		exp.HasBit = true
		exp.Bit = asInt(m["bit"], 0)
	}
	if data, ok := m["data"].(map[string]any); ok {
		b, err := decodePayload(data)
		if err != nil {
			return exp, err
		}
		exp.Data = b
	}
	switch result := m["result"].(type) {
	case string:
		exp.WantOutcome = normalizeResultString(result)
	case map[string]any:
		b, err := decodePayload(result)
		if err != nil {
			return exp, err
		}
		exp.WantBytes = b
		exp.HasBytes = true
		exp.WantOutcome = OutcomeSuccess
		if exp.Length == 0 {
			exp.Length = len(b)
		}
	default:
		return exp, fmt.Errorf("unsupported result type %T", m["result"])
	}
	return exp, nil
}

func normalizeResultString(s string) Outcome {
	switch s {
	case "accepted", "success":
		return OutcomeSuccess
	case "address-out-of-range":
		return OutcomeAddressOutOfRange
	case "write-protected":
		return OutcomeWriteProtected
	case "unsupported":
		return OutcomeUnsupported
	case "transport-failure":
		return OutcomeTransportFailure
	case "protocol-failure":
		return OutcomeProtocolFailure
	default:
		return Outcome(s)
	}
}

func decodePayload(m map[string]any) ([]byte, error) {
	if h, ok := m["hex"].(string); ok {
		return hex.DecodeString(strings.ReplaceAll(h, " ", ""))
	}
	return nil, fmt.Errorf("payload requires hex")
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func asInt(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return def
		}
		return int(i)
	default:
		return def
	}
}
