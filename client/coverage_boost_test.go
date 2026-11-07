// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

func TestReadResult_Success(t *testing.T) {
	if !(&ReadResult{Status: ReadStatusSuccess}).Success() {
		t.Fatal("Success() should match OK() for success")
	}
	if (&ReadResult{Status: ReadStatusShortRead}).Success() {
		t.Fatal("Success() should be false for short-read")
	}
}

func TestNopLoggerMethods(t *testing.T) {
	l := NewSlogLogger(nil)
	l.Debug("d")
	l.Info("i")
	l.Error("e")
}

func TestIsUploadProtocolError(t *testing.T) {
	if isUploadProtocolError(nil) {
		t.Fatal("nil should not be protocol error")
	}
	if !isUploadProtocolError(ErrProtocolFailure) {
		t.Fatal("ErrProtocolFailure should match")
	}
	if !isUploadProtocolError(&wire.S7Error{Code: 1}) {
		t.Fatal("S7Error should match")
	}
	if isUploadProtocolError(errors.New("other")) {
		t.Fatal("plain error should not match")
	}
}

func TestDiscoverOptionsApplied(t *testing.T) {
	// Invalid CIDR fails after options are applied (covers option setters).
	_, err := Discover(context.Background(), "not-a-cidr",
		WithDiscoverSafetyMode(SafetyConservative),
		WithDiscoverJitter(5),
		WithDiscoverMaxAttemptsPerHost(1),
	)
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestProbeRackSlotsValidationTable(t *testing.T) {
	cases := []RackSlotProbeRequest{
		{Address: "", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0},
		{Address: "10.0.0.1", Port: -1, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0},
		{Address: "10.0.0.1", Port: 70000, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0},
		{Address: "10.0.0.1", Port: 102, RackMin: -1, RackMax: 0, SlotMin: 0, SlotMax: 0},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 8, SlotMin: 0, SlotMax: 0},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: -1, SlotMax: 0},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 32},
		{Address: "10.0.0.1", Port: 102, RackMin: 2, RackMax: 1, SlotMin: 0, SlotMax: 0},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 2, SlotMax: 1},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0, Timeout: -time.Second},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0, Retries: -1},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0, DelayMS: -1},
		// Parallelism < 1 is overwritten by applyProbeDefaults before validation.
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0, JitterMS: -1},
		{Address: "10.0.0.1", Port: 102, RackMin: 0, RackMax: 0, SlotMin: 0, SlotMax: 0, MaxAttemptsPerHost: -1},
	}
	for i, req := range cases {
		_, err := ProbeRackSlots(context.Background(), req)
		if err == nil {
			t.Errorf("case %d: expected validation error", i)
			continue
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Errorf("case %d: want *ValidationError, got %T: %v", i, err, err)
		}
	}
}

func TestConnect_LogsErrorOnDialFailure(t *testing.T) {
	log := &recordingLogger{}
	c := New("127.0.0.1",
		WithPort(1), // nothing listening
		WithRackSlot(0, 1),
		WithTimeout(50*time.Millisecond),
		WithLogger(log),
	)
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected dial failure")
	}
	if log.errors == 0 {
		t.Fatal("expected Error log on Connect failure")
	}
}

func TestAutoConnect_BruteSucceeds(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1",
		WithPort(port),
		WithAutoRackSlot(true),
		WithTimeout(2*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect brute auto rack/slot: %v", err)
	}
	_ = c.Close()
}

// startFakeSZLServer handshakes then answers SZL requests with a fixed payload builder.
func startFakeSZLServer(t *testing.T, reply func(pduRef uint16, req []byte) []byte) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		for {
			tsdu, err := srv.ReadTSDU(ctx)
			if err != nil || len(tsdu) < 12 {
				return
			}
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			if err := srv.WriteTSDU(ctx, reply(pduRef, tsdu)); err != nil {
				return
			}
		}
	}()
	return port, func() { _ = ln.Close() }
}

func TestListBlocks_Success(t *testing.T) {
	port, cleanup := startFakeSZLServer(t, func(pduRef uint16, _ []byte) []byte {
		// One entry as an S7-300 lists it: block number, flags, language.
		return buildBlockResponse(pduRef, wire.UserdataBlockList, []byte{0x00, 0x01, 0x22, 0x05})
	})
	defer cleanup()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	blocks, err := c.ListBlocks(ctx, model.BlockDB)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Number != 1 || blocks[0].Type != model.BlockDB || blocks[0].Flags != 0x22 || blocks[0].Language != 5 {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestListAllBlocks_AllSucceedEmpty(t *testing.T) {
	port, cleanup := startFakeSZLServer(t, func(pduRef uint16, _ []byte) []byte {
		return buildBlockResponse(pduRef, wire.UserdataBlockList, nil)
	})
	defer cleanup()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	blocks, err := c.ListAllBlocks(ctx)
	if err != nil {
		t.Fatalf("ListAllBlocks: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("want empty list, got %d", len(blocks))
	}
}

func TestGetBlockInfo_Success(t *testing.T) {
	port, cleanup := startFakeSZLServer(t, func(pduRef uint16, _ []byte) []byte {
		return buildBlockResponse(pduRef, wire.UserdataBlockInfo, blockInfoRecord(100, 200, 300))
	})
	defer cleanup()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	info, err := c.GetBlockInfo(ctx, model.BlockDB, 1)
	if err != nil {
		t.Fatalf("GetBlockInfo: %v", err)
	}
	if info.LoadMemory != 100 || info.LocalData != 200 || info.MC7Size != 300 || info.Author != "AUTHOR" || info.Version != "1.2" {
		t.Fatalf("info = %+v", info)
	}
}

func TestGetBlockInfo_PartialOnShortPayload(t *testing.T) {
	port, cleanup := startFakeSZLServer(t, func(pduRef uint16, _ []byte) []byte {
		return buildBlockResponse(pduRef, wire.UserdataBlockInfo, make([]byte, 6))
	})
	defer cleanup()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	info, err := c.GetBlockInfo(ctx, model.BlockFC, 7)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if info == nil || info.Type != model.BlockFC || info.Number != 7 {
		t.Fatalf("want partial BlockInfo FC/7, got %+v", info)
	}
}

func TestListBlocks_BadSZLPayload(t *testing.T) {
	port, cleanup := startFakeSZLServer(t, func(pduRef uint16, _ []byte) []byte {
		// Data length 5 (not multiple of 4) → ParseBlockListResponse error.
		return buildBlockResponse(pduRef, wire.UserdataBlockList, []byte{1, 2, 3, 4, 5})
	})
	defer cleanup()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, err := c.ListBlocks(ctx, model.BlockOB)
	if err == nil {
		t.Fatal("expected list parse error")
	}
}

type recordingLogger struct {
	errors int
}

func (r *recordingLogger) Debug(string, ...any) {}
func (r *recordingLogger) Info(string, ...any)  {}
func (r *recordingLogger) Error(string, ...any) { r.errors++ }

func TestPDURefMismatchError_Error(t *testing.T) {
	err := &PDURefMismatchError{Expected: 1, Got: 9}
	if got := err.Error(); got == "" || !bytes.Contains([]byte(got), []byte("9")) {
		t.Fatalf("Error() = %q", got)
	}
}

func TestSlogLogger_CoversMethods(t *testing.T) {
	var buf bytes.Buffer
	l := NewSlogLogger(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	l.Debug("d%d", 1)
	l.Info("i%d", 2)
	l.Error("e%d", 3)
	if buf.Len() == 0 {
		t.Fatal("expected slog output")
	}
}

func TestProbeS7_SuccessWithJitter(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := probeS7(ctx, "127.0.0.1", port, &discoverOpts{
		timeout:  2000,
		rackMin:  0,
		rackMax:  0,
		slotMin:  1,
		slotMax:  1,
		jitterMS: 5,
	})
	if !r.IsS7 {
		t.Fatalf("expected IsS7, got %+v", r)
	}
	if r.PDUSize != 480 {
		t.Fatalf("PDUSize=%d", r.PDUSize)
	}
}

func TestProbeS7_MaxAttemptsAbandoned(t *testing.T) {
	// Closed port; two pairs but max 1 attempt → abandon.
	r := probeS7(context.Background(), "127.0.0.1", 1, &discoverOpts{
		timeout:            30,
		rackMin:            0,
		rackMax:            0,
		slotMin:            0,
		slotMax:            1,
		maxAttemptsPerHost: 1,
	})
	if r.AbandonedReason != "max_attempts" {
		t.Fatalf("AbandonedReason=%q want max_attempts (result=%+v)", r.AbandonedReason, r)
	}
}

func TestAutoConnect_CommonPairsFailure(t *testing.T) {
	c := New("127.0.0.1",
		WithPort(1),
		WithAutoRackSlot(false),
		WithTimeout(30*time.Millisecond),
	)
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected failure")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("common rack/slot")) {
		t.Fatalf("want common-pairs failure, got %v", err)
	}
}

func TestAutoConnect_BruteCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New("127.0.0.1",
		WithPort(1),
		WithAutoRackSlot(true),
		WithTimeout(30*time.Millisecond),
	)
	if err := c.Connect(ctx); err == nil {
		t.Fatal("expected canceled context error")
	}
}

func TestGetCPUState_StopAndStartup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bzuID byte // SZL 0x0424 record byte 3: previous mode, requested mode
		want  model.CPUState
	}{
		{"run", 0x08, model.CPUStateRun},
		{"stop", 0x04, model.CPUStateStop},
		{"startup", 0x05, model.CPUStateStartup},
		{"hold", 0x0a, model.CPUStateHold},
		{"unknown", 0x0f, model.CPUStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, cleanup := startFakeSZLServer(t, func(pduRef uint16, _ []byte) []byte {
				return buildSZLReply(pduRef, wire.SZLCPUState, szlList(20, []byte{0x51, 0x44, 0xff, tc.bzuID}))
			})
			defer cleanup()

			c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := c.Connect(ctx); err != nil {
				t.Fatalf("Connect: %v", err)
			}
			defer func() { _ = c.Close() }()

			got, err := c.GetCPUState(ctx)
			if err != nil {
				t.Fatalf("GetCPUState: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIdentify_ShortModulePayloadPartial(t *testing.T) {
	n := 0
	port, cleanup := startFakeSZLServer(t, func(pduRef uint16, _ []byte) []byte {
		n++
		if n == 1 {
			// Module ID records shorter than the 28 bytes of the layout.
			return buildSZLReply(pduRef, wire.SZLModuleID, szlList(10, []byte{0x00, 0x01}))
		}
		return buildSZLReply(pduRef, wire.SZLComponentID, szlList(34,
			append([]byte{0x00, 0x02}, "CPU 315-2 DP"...),
			append([]byte{0x00, 0x03}, "PLANT01"...)))
	})
	defer cleanup()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	info, err := c.Identify(ctx)
	if info == nil {
		t.Fatalf("want partial info, err=%v", err)
	}
	if err == nil {
		t.Fatal("expected short module record error")
	}
	if info.ModuleName != "CPU 315-2 DP" || info.PlantID != "PLANT01" {
		t.Fatalf("expected ModuleName and PlantID from component SZL, got %+v", info)
	}
}

func TestProbeRackSlots_WithJitter(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := ProbeRackSlots(ctx, RackSlotProbeRequest{
		Address:     "127.0.0.1",
		Port:        port,
		RackMin:     0,
		RackMax:     0,
		SlotMin:     1,
		SlotMax:     1,
		Timeout:     time.Second,
		Parallelism: 1,
		JitterMS:    3,
	})
	if err != nil {
		t.Fatalf("ProbeRackSlots: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates=%d", len(res.Candidates))
	}
}

func TestSendReceive_PDURefMismatch(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		tsdu, err := srv.ReadTSDU(ctx)
		if err != nil || len(tsdu) < 12 {
			return
		}
		// Echo with wrong PDU ref.
		resp := buildSZLResponse(0xDEAD, wire.SZLModuleID, 8, make([]byte, 8))
		_ = srv.WriteTSDU(ctx, resp)
	}()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, err = c.GetCPUState(ctx)
	if err == nil {
		t.Fatal("expected PDU ref mismatch")
	}
	if _, ok := errors.AsType[*PDURefMismatchError](err); !ok {
		t.Fatalf("want PDURefMismatchError, got %T: %v", err, err)
	}
}

func TestPerformS7Setup_PDURefMismatch(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		ctx := context.Background()
		s7Data, err := srv.ReadTSDU(ctx)
		if err != nil || len(s7Data) < 10 {
			return
		}
		_ = srv.WriteTSDU(ctx, buildS7SetupResponse(0xBEEF, 480))
	}()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	err = c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected setup PDU ref mismatch")
	}
	if _, ok := errors.AsType[*PDURefMismatchError](err); !ok {
		t.Fatalf("want PDURefMismatchError in chain, got %T: %v", err, err)
	}
}

func TestProbeReadableRanges_RetriesAndMixed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		n := 0
		for {
			tsdu, err := srv.ReadTSDU(ctx)
			if err != nil || len(tsdu) < 12 {
				return
			}
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			n++
			if n%2 == 1 {
				_ = srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0xAA, 0xBB}))
			} else {
				_ = srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0x11, 0x22}))
			}
		}
	}()

	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	res, err := c.ProbeReadableRanges(ctx, RangeProbeRequest{
		Area:       model.AreaInputs,
		Start:      0,
		End:        2,
		ProbeSize:  2,
		Step:       2,
		Repeat:     2,
		Interval:   time.Millisecond,
		Retries:    1,
		RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ProbeReadableRanges: %v", err)
	}
	if len(res.Probes) != 1 {
		t.Fatalf("probes=%d", len(res.Probes))
	}
	if res.Probes[0].Result.Status != ReadStatusInconclusive {
		t.Fatalf("status=%q want inconclusive", res.Probes[0].Result.Status)
	}
}

func TestRunFollowUp_ConfirmAnyFallsThroughToProtection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		for i := range 3 {
			tsdu, err := srv.ReadTSDU(ctx)
			if err != nil || len(tsdu) < 12 {
				return
			}
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			switch i {
			case 0, 1:
				// Fail ModuleID / CPUState parse with bad return code.
				_ = srv.WriteTSDU(ctx, buildSZLItemError(pduRef, wire.RetCodeNotAvailable))
			default:
				_ = srv.WriteTSDU(ctx, buildSZLReply(pduRef, wire.SZLProtectionInfo, szlList(40, []byte{0, 4, 0, 1, 0, 0, 0, 1})))
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := ProbeRackSlots(ctx, RackSlotProbeRequest{
		Address:     "127.0.0.1",
		Port:        port,
		RackMin:     0,
		RackMax:     0,
		SlotMin:     1,
		SlotMax:     1,
		Timeout:     time.Second,
		Parallelism: 1,
		Strict:      true,
		Confirm:     ConfirmAny,
	})
	if err != nil {
		t.Fatalf("ProbeRackSlots: %v", err)
	}
	if len(res.Valid) != 1 {
		t.Fatalf("want 1 valid via protection confirm, got %+v", res)
	}
	if res.Candidates[0].ConfirmedBy != ConfirmAny {
		t.Fatalf("ConfirmedBy=%v want ConfirmAny", res.Candidates[0].ConfirmedBy)
	}
}
