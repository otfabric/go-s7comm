//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

const opTimeout = 10 * time.Second

// knownDifferences are steps where a server answers differently from the
// fixture on purpose, keyed by server, fixture and step: the outcome that
// server must give instead. Only the otfabric image (go-s7comm's own server,
// not a reference) has one: it answers a write to a read-only area with the
// Siemens code 0x03 (write-protected) where both references send 0x0A,
// which the fixture normalises to address-out-of-range. snap7-interop lists
// the same step in own/go-s7comm/known-differences.tsv.
var knownDifferences = map[string]Outcome{
	"otfabric/read-only-reject/1": OutcomeWriteProtected,
}

func runFixtureAgainst(t *testing.T, server, host string, port, rack, slot int, fx *CompiledFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := newClient(host, port, rack, slot)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	for i, exp := range fx.Expectations {
		if want, ok := knownDifferences[fmt.Sprintf("%s/%s/%d", server, fx.ID, i)]; ok {
			t.Logf("known difference: %s step %d of %s expects %s, not %s", server, i, fx.ID, want, exp.WantOutcome)
			exp.WantOutcome = want
		}
		name := fmt.Sprintf("step%d_%s", i, exp.Operation)
		ok := t.Run(name, func(t *testing.T) {
			stepCtx, stepCancel := context.WithTimeout(ctx, opTimeout)
			defer stepCancel()
			if err := executeExpectation(stepCtx, &c, host, port, rack, slot, exp); err != nil {
				t.Fatalf("%s/%s expectation[%d] %s %s#%d@%d: %v",
					t.Name(), fx.ID, i, exp.Operation, exp.AreaType, exp.AreaNum, exp.Offset, err)
			}
		})
		if !ok {
			return
		}
	}
}

func newClient(host string, port, rack, slot int) *client.Client {
	return client.New(host,
		client.WithPort(port),
		client.WithRackSlot(rack, slot),
		client.WithTimeout(opTimeout),
		client.WithMaxPDU(480),
	)
}

func executeExpectation(ctx context.Context, c **client.Client, host string, port, rack, slot int, exp Expectation) error {
	switch exp.Operation {
	case "reconnect":
		_ = (*c).Close()
		next := newClient(host, port, rack, slot)
		if err := next.Connect(ctx); err != nil {
			return fmt.Errorf("reconnect: %w (want %s)", err, exp.WantOutcome)
		}
		*c = next
		if exp.WantOutcome != OutcomeSuccess {
			return fmt.Errorf("reconnect succeeded, want %s", exp.WantOutcome)
		}
		return nil

	case "concurrent-read":
		return executeConcurrentRead(ctx, host, port, rack, slot, exp)

	case "read", "final-state":
		data, outcome, err := doRead(ctx, *c, exp)
		if outcome != exp.WantOutcome {
			return fmt.Errorf("outcome %s (err=%v), want %s", outcome, err, exp.WantOutcome)
		}
		if exp.HasBytes && outcome == OutcomeSuccess {
			if !bytes.Equal(data, exp.WantBytes) {
				return fmt.Errorf("bytes %s, want %s", hex.EncodeToString(data), hex.EncodeToString(exp.WantBytes))
			}
		}
		return nil

	case "write":
		outcome, err := doWrite(ctx, *c, exp)
		if outcome != exp.WantOutcome {
			return fmt.Errorf("outcome %s (err=%v), want %s", outcome, err, exp.WantOutcome)
		}
		return nil

	case "rejected-read":
		_, outcome, err := doRead(ctx, *c, exp)
		if outcome != exp.WantOutcome || outcome == OutcomeSuccess {
			return fmt.Errorf("outcome %s (err=%v), want rejected %s", outcome, err, exp.WantOutcome)
		}
		return nil

	case "rejected-write":
		outcome, err := doWrite(ctx, *c, exp)
		if outcome != exp.WantOutcome || outcome == OutcomeSuccess {
			return fmt.Errorf("outcome %s (err=%v), want rejected %s", outcome, err, exp.WantOutcome)
		}
		return nil

	default:
		return fmt.Errorf("unsupported operation %q", exp.Operation)
	}
}

func executeConcurrentRead(ctx context.Context, host string, port, rack, slot int, exp Expectation) error {
	n := max(exp.Clients, 2)
	length := exp.Length
	if length == 0 && exp.HasBytes {
		length = len(exp.WantBytes)
	}
	type result struct {
		data    []byte
		outcome Outcome
		err     error
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			c := newClient(host, port, rack, slot)
			if err := c.Connect(ctx); err != nil {
				results[i] = result{outcome: OutcomeTransportFailure, err: err}
				return
			}
			defer func() { _ = c.Close() }()
			data, outcome, err := doRead(ctx, c, Expectation{
				Operation: "read",
				AreaType:  exp.AreaType,
				AreaNum:   exp.AreaNum,
				Offset:    exp.Offset,
				Length:    length,
			})
			results[i] = result{data: data, outcome: outcome, err: err}
		}(i)
	}
	wg.Wait()

	wantHex := ""
	if exp.HasBytes {
		wantHex = hex.EncodeToString(exp.WantBytes)
	}
	for i, r := range results {
		if r.outcome != exp.WantOutcome {
			return fmt.Errorf("client %d outcome %s (err=%v), want %s", i, r.outcome, r.err, exp.WantOutcome)
		}
		got := hex.EncodeToString(r.data)
		if exp.HasBytes && got != wantHex {
			return fmt.Errorf("client %d bytes %s, want %s", i, got, wantHex)
		}
		if i > 0 && got != hex.EncodeToString(results[0].data) {
			return fmt.Errorf("concurrent clients returned differing bytes")
		}
	}
	return nil
}

func doRead(ctx context.Context, c *client.Client, exp Expectation) ([]byte, Outcome, error) {
	if exp.HasBit {
		addr, err := modelBitAddress(exp.AreaType, exp.AreaNum, exp.Offset, exp.Bit)
		if err != nil {
			return nil, OutcomeProtocolFailure, err
		}
		v, err := c.ReadBit(ctx, addr)
		if err != nil {
			return nil, classifyWriteError(err), err
		}
		bitVal := byte(0)
		if v {
			bitVal = 1
		}
		return []byte{bitVal}, OutcomeSuccess, nil
	}

	length := exp.Length
	if length == 0 && exp.HasBytes {
		length = len(exp.WantBytes)
	}
	res, err := readArea(ctx, c, exp.AreaType, exp.AreaNum, exp.Offset, length)
	if err != nil {
		return nil, classifyTransportOrProtocol(err), err
	}
	if !res.OK() {
		return res.Data, classifyReadResult(res), res.Err()
	}
	return res.Data, OutcomeSuccess, nil
}

func doWrite(ctx context.Context, c *client.Client, exp Expectation) (Outcome, error) {
	if exp.HasBit {
		addr, err := modelBitAddress(exp.AreaType, exp.AreaNum, exp.Offset, exp.Bit)
		if err != nil {
			return OutcomeProtocolFailure, err
		}
		value := len(exp.Data) > 0 && exp.Data[0] != 0
		if err := c.WriteBit(ctx, addr, value); err != nil {
			return classifyWriteError(err), err
		}
		return OutcomeSuccess, nil
	}
	if err := writeArea(ctx, c, exp.AreaType, exp.AreaNum, exp.Offset, exp.Data); err != nil {
		return classifyWriteError(err), err
	}
	return OutcomeSuccess, nil
}

func readArea(ctx context.Context, c *client.Client, areaType string, number, offset, length int) (*client.ReadResult, error) {
	addr, err := modelAddress(areaType, number, offset, length)
	if err != nil {
		return nil, err
	}
	return c.ReadArea(ctx, addr)
}

func writeArea(ctx context.Context, c *client.Client, areaType string, number, offset int, data []byte) error {
	addr, err := modelAddress(areaType, number, offset, len(data))
	if err != nil {
		return err
	}
	return c.WriteArea(ctx, addr, data)
}

func modelAddress(areaType string, number, offset, size int) (model.Address, error) {
	switch areaType {
	case "db":
		return model.Address{Area: model.AreaDB, DBNumber: number, Start: offset, Size: size}, nil
	case "markers":
		return model.Address{Area: model.AreaMerkers, Start: offset, Size: size}, nil
	case "inputs":
		return model.Address{Area: model.AreaInputs, Start: offset, Size: size}, nil
	case "outputs":
		return model.Address{Area: model.AreaOutputs, Start: offset, Size: size}, nil
	default:
		return model.Address{}, fmt.Errorf("unsupported area type %q", areaType)
	}
}

func modelBitAddress(areaType string, number, byteOffset, bitOffset int) (model.BitAddress, error) {
	addr, err := modelAddress(areaType, number, byteOffset, 1)
	if err != nil {
		return model.BitAddress{}, err
	}
	return model.BitAddress{
		Area:       addr.Area,
		DBNumber:   addr.DBNumber,
		ByteOffset: byteOffset,
		BitOffset:  bitOffset,
	}, nil
}

func classifyReadResult(res *client.ReadResult) Outcome {
	if res == nil {
		return OutcomeProtocolFailure
	}
	switch res.Status {
	case client.ReadStatusRejected:
		return mapItemReturnCode(res.ReturnCode)
	case client.ReadStatusTransportErr, client.ReadStatusTimeout:
		return OutcomeTransportFailure
	case client.ReadStatusProtocolErr:
		return OutcomeProtocolFailure
	default:
		if res.OK() {
			return OutcomeSuccess
		}
		return OutcomeProtocolFailure
	}
}

func classifyWriteError(err error) Outcome {
	if err == nil {
		return OutcomeSuccess
	}
	if s7err, ok := errors.AsType[*wire.S7Error](err); ok {
		return mapItemReturnCode(s7err.Code)
	}
	return classifyTransportOrProtocol(err)
}

func classifyTransportOrProtocol(err error) Outcome {
	if err == nil {
		return OutcomeSuccess
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return OutcomeTransportFailure
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return OutcomeTransportFailure
	}
	return OutcomeProtocolFailure
}

func mapItemReturnCode(code byte) Outcome {
	switch code {
	case wire.RetCodeAddressFault, wire.RetCodeNotAvailable:
		// snap7-interop normalizes OOR and RO-reject (0x0A) to address-out-of-range.
		return OutcomeAddressOutOfRange
	case wire.RetCodeAccessFault: // 0x03; the otfabric image's answer for a read-only area
		return OutcomeWriteProtected
	default:
		return OutcomeProtocolFailure
	}
}
