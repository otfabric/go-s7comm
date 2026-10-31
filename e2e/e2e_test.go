// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func connect(t *testing.T, p *plc) *client.Client {
	t.Helper()
	c := client.New("127.0.0.1", client.WithPort(p.port()), client.WithRackSlot(0, 1), client.WithTimeout(2*time.Second))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestConnectNegotiatesThePDUSize(t *testing.T) {
	p := startPLC(t, 240)
	c := connect(t, p)
	if got := c.PDUSize(); got != 240 {
		t.Errorf("PDUSize = %d, want the PLC's 240", got)
	}
	info := c.ConnectionInfo()
	if info.Host != "127.0.0.1" || info.Port != p.port() || info.Rack != 0 || info.Slot != 1 || info.PDUSize != 240 {
		t.Errorf("ConnectionInfo = %+v", info)
	}
}

func TestWriteThenReadBack(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	ctx := context.Background()
	want := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01}
	if err := c.WriteDB(ctx, 1, 10, want); err != nil {
		t.Fatalf("WriteDB: %v", err)
	}
	res, err := c.ReadDB(ctx, 1, 10, len(want))
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if err := res.Err(); err != nil {
		t.Fatalf("ReadDB outcome: %v", err)
	}
	if !bytes.Equal(res.Data, want) || res.ReturnedLength != len(want) {
		t.Errorf("read back %x (%d octets), want %x", res.Data, res.ReturnedLength, want)
	}
	// The octets around the write are untouched.
	res, err = c.ReadDB(ctx, 1, 9, 7)
	if err != nil || !res.OK() {
		t.Fatalf("ReadDB around: %v, %+v", err, res)
	}
	if !bytes.Equal(res.Data, []byte{0, 0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0}) {
		t.Errorf("around the write: %x", res.Data)
	}
}

func TestOtherAreas(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	ctx := context.Background()
	addr := model.Address{Area: model.AreaMerkers, Start: 4, Size: 2}
	if err := c.WriteArea(ctx, addr, []byte{0x12, 0x34}); err != nil {
		t.Fatalf("WriteArea: %v", err)
	}
	res, err := c.ReadMerkers(ctx, 4, 2)
	if err != nil || !res.OK() || !bytes.Equal(res.Data, []byte{0x12, 0x34}) {
		t.Fatalf("ReadMerkers: %v, %+v", err, res)
	}
	for name, read := range map[string]func(context.Context, int, int) (*client.ReadResult, error){
		"inputs": c.ReadInputs, "outputs": c.ReadOutputs,
	} {
		res, err := read(ctx, 0, 4)
		if err != nil || !res.OK() || len(res.Data) != 4 {
			t.Errorf("%s: %v, %+v", name, err, res)
		}
	}
}

func TestBits(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	ctx := context.Background()
	if err := c.WriteDBBit(ctx, 1, 3, 5, true); err != nil {
		t.Fatalf("WriteDBBit: %v", err)
	}
	if v, err := c.ReadDBBit(ctx, 1, 3, 5); err != nil || !v {
		t.Fatalf("ReadDBBit(5) = %v, %v", v, err)
	}
	if v, err := c.ReadDBBit(ctx, 1, 3, 4); err != nil || v {
		t.Fatalf("ReadDBBit(4) = %v, %v", v, err)
	}
	res, err := c.ReadDB(ctx, 1, 3, 2)
	if err != nil || !res.OK() || !bytes.Equal(res.Data, []byte{0x20, 0x00}) {
		t.Fatalf("the octet: %v, %+v", err, res)
	}
	if err := c.WriteDBBit(ctx, 1, 3, 5, false); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if v, err := c.ReadDBBit(ctx, 1, 3, 5); err != nil || v {
		t.Fatalf("after clear = %v, %v", v, err)
	}
}

func TestCPUState(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	state, err := c.GetCPUState(context.Background())
	if err != nil {
		t.Fatalf("GetCPUState: %v", err)
	}
	if state != model.CPUStateRun {
		t.Errorf("state = %v, want RUN", state)
	}
}

func TestRefusedAddresses(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	ctx := context.Background()

	res, err := c.ReadDB(ctx, 99, 0, 4)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if res.Status != client.ReadStatusRejected || res.ReturnCode != wire.RetCodeNotAvailable {
		t.Errorf("a DB that does not exist: %+v", res)
	}
	res, err = c.ReadDB(ctx, 1, 1022, 8)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if res.Status != client.ReadStatusRejected || res.ReturnCode != wire.RetCodeAddressFault {
		t.Errorf("beyond the end of the DB: %+v", res)
	}

	err = c.WriteDB(ctx, 99, 0, []byte{1, 2})
	var s7Err *wire.S7Error
	if !errors.As(err, &s7Err) {
		t.Errorf("WriteDB to a DB that does not exist: %v", err)
	}

	// The connection survives a refusal.
	if res, err := c.ReadDB(ctx, 1, 0, 2); err != nil || !res.OK() {
		t.Errorf("after the refusals: %v, %+v", err, res)
	}
}

func TestInvalidAddressIsNotSent(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	var ve *client.ValidationError
	if _, err := c.ReadDB(context.Background(), 1, -1, 2); !errors.As(err, &ve) {
		t.Errorf("negative offset: %v", err)
	}
}

func TestTransferLargerThanThePDUIsSplit(t *testing.T) {
	c := connect(t, startPLC(t, 240))
	ctx := context.Background()
	want := make([]byte, 700)
	for i := range want {
		want[i] = byte(i)
	}
	if err := c.WriteDB(ctx, 1, 100, want); err != nil {
		t.Fatalf("WriteDB: %v", err)
	}
	res, err := c.ReadDB(ctx, 1, 100, len(want))
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if err := res.Err(); err != nil {
		t.Fatalf("ReadDB outcome: %v", err)
	}
	if !bytes.Equal(res.Data, want) {
		t.Error("the data read back differs from the data written")
	}
}

func TestNotConnected(t *testing.T) {
	p := startPLC(t, 480)
	ctx := context.Background()
	c := client.New("127.0.0.1", client.WithPort(p.port()), client.WithRackSlot(0, 1))
	if err := c.WriteDB(ctx, 1, 0, []byte{1}); !errors.Is(err, client.ErrNotConnected) {
		t.Errorf("before Connect: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	res, err := c.ReadDB(ctx, 1, 0, 2)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if !errors.Is(res.Err(), client.ErrNotConnected) {
		t.Errorf("after Close: %v", res.Err())
	}
}

func TestConnectRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	c := client.New("127.0.0.1", client.WithPort(port), client.WithRackSlot(0, 1), client.WithTimeout(time.Second))
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect to a closed port succeeded")
	}
}

func TestSilentPLCTimesOut(t *testing.T) {
	p := startPLC(t, 480)
	c := connect(t, p)
	p.mode.Store(modeSilent)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := c.ReadDB(ctx, 1, 0, 2)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if res.Status != client.ReadStatusTimeout {
		t.Errorf("status = %s, want timeout (%v)", res.Status, res.Cause)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("returned after %v, the context allowed 100ms", d)
	}
}

func TestCanceledContext(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.WriteDB(ctx, 1, 0, []byte{1}); !errors.Is(err, context.Canceled) {
		t.Errorf("WriteDB: %v", err)
	}
}

func TestReconnectAfterConnectionLoss(t *testing.T) {
	p := startPLC(t, 480)
	c := connect(t, p)
	ctx := context.Background()
	if err := c.WriteDB(ctx, 1, 0, []byte{0x42, 0x43}); err != nil {
		t.Fatalf("WriteDB: %v", err)
	}

	p.mode.Store(modeHangUp)
	res, err := c.ReadDB(ctx, 1, 0, 2)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if res.OK() || res.Status != client.ReadStatusTransportErr {
		t.Fatalf("status = %s, want transport-error (%v)", res.Status, res.Cause)
	}

	p.mode.Store(modeAnswer)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect again: %v", err)
	}
	res, err = c.ReadDB(ctx, 1, 0, 2)
	if err != nil || !res.OK() || !bytes.Equal(res.Data, []byte{0x42, 0x43}) {
		t.Fatalf("after reconnecting: %v, %+v", err, res)
	}
}

func TestForeignResponseIsAProtocolError(t *testing.T) {
	p := startPLC(t, 480)
	c := connect(t, p)
	p.mode.Store(modeGarbage)
	res, err := c.ReadDB(context.Background(), 1, 0, 2)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if res.Status != client.ReadStatusProtocolErr {
		t.Errorf("status = %s, want protocol-error (%v)", res.Status, res.Cause)
	}
}

func TestConcurrentUseOfOneClient(t *testing.T) {
	c := connect(t, startPLC(t, 480))
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := []byte{byte(i), byte(i + 1)}
			if err := c.WriteDB(ctx, 1, 2*i, want); err != nil {
				errs <- err
				return
			}
			res, err := c.ReadDB(ctx, 1, 2*i, 2)
			if err == nil {
				err = res.Err()
			}
			if err == nil && !bytes.Equal(res.Data, want) {
				err = errors.New("read back another client's value")
			}
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestProbeRackSlots(t *testing.T) {
	p := startPLC(t, 480)
	res, err := client.ProbeRackSlots(context.Background(), client.RackSlotProbeRequest{
		Address: "127.0.0.1", Port: p.port(),
		RackMin: 0, RackMax: 0, SlotMin: 1, SlotMax: 2,
		Timeout: time.Second, Parallelism: 2, Strict: true,
	})
	if err != nil {
		t.Fatalf("ProbeRackSlots: %v", err)
	}
	if len(res.Candidates) != 2 || res.ConfirmedByQuery != 2 || len(res.Valid) != 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, c := range res.Valid {
		if c.Status != client.StatusValidQuery || c.PDUSize != 480 {
			t.Errorf("candidate %+v", c)
		}
	}
}
