// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// startScriptedPeer runs a TP0 peer on loopback that serves any number of connections.
// setup builds the reply to the S7 setup request (nil means an accepted setup with PDU size 480).
// op builds the reply to each later request; a nil op leaves requests unanswered and a nil
// reply closes the connection. The peer stops when the test ends.
func startScriptedPeer(t *testing.T, setup func(pduRef uint16) []byte, op func(pduRef uint16, req []byte) []byte) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		wg.Wait()
	})

	serve := func(raw net.Conn) {
		defer wg.Done()
		srv, err := cotp.Accept(ctx, raw, cotp.ServerConfig{MaxTPDULength: 1024})
		if err != nil {
			_ = raw.Close()
			return
		}
		defer func() { _ = srv.Close() }()

		req, err := srv.ReadTSDU(ctx)
		if err != nil || len(req) < 10 {
			return
		}
		ref := binary.BigEndian.Uint16(req[4:6])
		reply := buildS7SetupResponse(ref, 480)
		if setup != nil {
			reply = setup(ref)
		}
		if reply == nil {
			<-ctx.Done()
			return
		}
		if err := srv.WriteTSDU(ctx, reply); err != nil {
			return
		}
		for {
			req, err := srv.ReadTSDU(ctx)
			if err != nil || len(req) < 10 {
				return
			}
			if op == nil {
				continue
			}
			reply := op(binary.BigEndian.Uint16(req[4:6]), req)
			if reply == nil {
				return
			}
			if err := srv.WriteTSDU(ctx, reply); err != nil {
				return
			}
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go serve(raw)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// buildAckData builds an S7 ack-data TSDU with the given header error and sections.
func buildAckData(pduRef uint16, errClass, errCode byte, param, data []byte) []byte {
	resp := make([]byte, 12, 12+len(param)+len(data))
	resp[0] = 0x32
	resp[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(resp[4:6], pduRef)
	binary.BigEndian.PutUint16(resp[6:8], uint16(len(param)))
	binary.BigEndian.PutUint16(resp[8:10], uint16(len(data)))
	resp[10] = errClass
	resp[11] = errCode
	return append(append(resp, param...), data...)
}

// buildSZLReply builds an SZL response whose parsed Data is exactly payload.
func buildSZLReply(pduRef, szlID uint16, payload []byte) []byte {
	return buildSZLResponse(pduRef, szlID, len(payload)+4, append(append([]byte{}, payload...), 0, 0, 0, 0))
}

// buildSZLItemError builds an SZL response carrying a non-success item return code.
func buildSZLItemError(pduRef uint16, retCode byte) []byte {
	resp := buildSZLResponse(pduRef, wire.SZLModuleID, 8, make([]byte, 8))
	resp[14] = retCode
	return resp
}

func connectScripted(t *testing.T, port int) *Client {
	t.Helper()
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestSendReceive_MalformedResponses(t *testing.T) {
	cases := []struct {
		name  string
		reply func(ref uint16) []byte
		want  error
	}{
		{
			name:  "shorter than S7 header",
			reply: func(uint16) []byte { return []byte{0x32, 0x03, 0x00} },
			want:  wire.ErrShortS7Header,
		},
		{
			name: "wrong protocol ID",
			reply: func(ref uint16) []byte {
				resp := buildAckData(ref, 0, 0, nil, nil)
				resp[0] = 0x33
				return resp
			},
			want: wire.ErrInvalidS7ProtocolID,
		},
		{
			name: "ack without error bytes",
			reply: func(ref uint16) []byte {
				return buildAckData(ref, 0, 0, nil, nil)[:10]
			},
			want: wire.ErrShortS7AckHeader,
		},
		{
			name: "declared sections overrun TSDU",
			reply: func(ref uint16) []byte {
				resp := buildAckData(ref, 0, 0, nil, nil)
				binary.BigEndian.PutUint16(resp[6:8], 100)
				return resp
			},
			want: wire.ErrS7PayloadLength,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte { return tc.reply(ref) })
			c := connectScripted(t, port)

			state, err := c.GetCPUState(testContext(t))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !errors.Is(err, ErrProtocolFailure) {
				t.Fatalf("err = %v, want ErrProtocolFailure classification", err)
			}
			if state != model.CPUStateUnknown {
				t.Fatalf("state = %v, want unknown", state)
			}
		})
	}
}

func TestSendReceive_HeaderErrorUsesParamCode(t *testing.T) {
	param := []byte{wire.FuncReadVar, 0x00, 0x01, 0x14} // standard parameter error slot
	port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
		return buildAckData(ref, wire.ErrClassApp, 0x04, param, nil)
	})
	c := connectScripted(t, port)

	_, err := c.GetProtectionLevel(testContext(t))
	var s7Err *wire.S7Error
	if !errors.As(err, &s7Err) {
		t.Fatalf("err = %T %v, want *wire.S7Error", err, err)
	}
	if s7Err.Class != wire.ErrClassApp || s7Err.Code != 0x04 {
		t.Fatalf("raw class/code not preserved: %+v", s7Err)
	}
	if want := wire.ParamErrorCodeString(0x0114); s7Err.Message != want {
		t.Fatalf("Message = %q, want parameter error %q", s7Err.Message, want)
	}
}

func TestSendReceive_RequestExceedsNegotiatedPDU(t *testing.T) {
	var requests atomic.Int32
	port := startScriptedPeer(t,
		func(ref uint16) []byte { return buildS7SetupResponse(ref, 16) },
		func(uint16, []byte) []byte {
			requests.Add(1)
			return nil
		})
	c := connectScripted(t, port)

	if _, err := c.GetCPUState(testContext(t)); !errors.Is(err, ErrRequestExceedsPDU) {
		t.Fatalf("err = %v, want ErrRequestExceedsPDU", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("peer received %d requests; an oversized request must not reach the wire", n)
	}
}

func TestSendReceive_PeerClosesIsNotProtocolFailure(t *testing.T) {
	port := startScriptedPeer(t, nil, func(uint16, []byte) []byte { return nil })
	c := connectScripted(t, port)

	_, err := c.GetCPUState(testContext(t))
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if errors.Is(err, ErrProtocolFailure) {
		t.Fatalf("transport failure classified as protocol failure: %v", err)
	}
}

func TestSendReceive_CanceledContext(t *testing.T) {
	port := startScriptedPeer(t, nil, nil)
	c := connectScripted(t, port)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := c.GetCPUState(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestConnect_SetupFailures(t *testing.T) {
	cases := []struct {
		name  string
		reply func(ref uint16) []byte
		check func(t *testing.T, err error)
	}{
		{
			name:  "malformed header",
			reply: func(uint16) []byte { return []byte{0x32} },
			check: func(t *testing.T, err error) {
				if !errors.Is(err, wire.ErrShortS7Header) || !errors.Is(err, ErrProtocolFailure) {
					t.Fatalf("err = %v, want short header protocol failure", err)
				}
			},
		},
		{
			name: "header error",
			reply: func(ref uint16) []byte {
				return buildAckData(ref, wire.ErrClassResource, 0x02, nil, nil)
			},
			check: func(t *testing.T, err error) {
				var s7Err *wire.S7Error
				if !errors.As(err, &s7Err) || s7Err.Class != wire.ErrClassResource {
					t.Fatalf("err = %v, want S7Error class 0x83", err)
				}
			},
		},
		{
			name: "wrong function code",
			reply: func(ref uint16) []byte {
				return buildAckData(ref, 0, 0, []byte{wire.FuncReadVar, 0, 0, 1, 0, 1, 1, 0xE0}, nil)
			},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrProtocolFailure) || !strings.Contains(err.Error(), "expected function") {
					t.Fatalf("err = %v, want function mismatch", err)
				}
			},
		},
		{
			name: "truncated parameters",
			reply: func(ref uint16) []byte {
				return buildAckData(ref, 0, 0, []byte{wire.FuncSetupComm, 0}, nil)
			},
			check: func(t *testing.T, err error) {
				var s7Err *wire.S7Error
				if !errors.Is(err, ErrProtocolFailure) || !errors.As(err, &s7Err) {
					t.Fatalf("err = %v, want parse failure", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, tc.reply, nil)
			c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
			err := c.Connect(testContext(t))
			if err == nil {
				t.Fatal("expected Connect to fail")
			}
			tc.check(t, err)
			if info := c.ConnectionInfo(); info.PDUSize != 0 || info.RemoteTSAP != 0 {
				t.Fatalf("failed Connect left session state: %+v", info)
			}
			if _, err := c.GetCPUState(testContext(t)); !errors.Is(err, ErrNotConnected) {
				t.Fatalf("after failed Connect: err = %v, want ErrNotConnected", err)
			}
		})
	}
}

func TestPerformS7Setup_CanceledBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A nil conn proves the context is checked before any I/O.
	if _, err := performS7Setup(ctx, nil, 1, 1, 1, 480); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestConnect_FailedReconnectKeepsSession pins the ERRORS.md contract that a failed
// reconnect does not drop a healthy session.
func TestConnect_FailedReconnectKeepsSession(t *testing.T) {
	var setups atomic.Int32
	port := startScriptedPeer(t,
		func(ref uint16) []byte {
			if setups.Add(1) == 2 {
				return buildAckData(ref, wire.ErrClassResource, 0x02, nil, nil)
			}
			return buildS7SetupResponse(ref, 480)
		},
		func(ref uint16, _ []byte) []byte {
			return buildSZLReply(ref, wire.SZLCPUState, []byte{0, 0, 0x08, 0})
		})
	c := connectScripted(t, port)

	if err := c.Connect(testContext(t)); err == nil {
		t.Fatal("expected the second Connect to fail")
	}
	if c.PDUSize() != 480 {
		t.Fatalf("PDUSize = %d after failed reconnect, want 480", c.PDUSize())
	}
	state, err := c.GetCPUState(testContext(t))
	if err != nil || state != model.CPUStateRun {
		t.Fatalf("old session unusable: state=%v err=%v", state, err)
	}
}

func TestClose_IsIdempotentAndEndsSession(t *testing.T) {
	port := startScriptedPeer(t, nil, nil)
	c := connectScripted(t, port)

	for i := 0; i < 2; i++ {
		if err := c.Close(); err != nil {
			t.Fatalf("Close %d: %v", i+1, err)
		}
	}
	if info := c.ConnectionInfo(); info.PDUSize != 0 || info.Rack != 0 || info.Slot != 0 || info.RemoteTSAP != 0 {
		t.Fatalf("Close left session state: %+v", info)
	}
	if _, err := c.GetCPUState(testContext(t)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestIsProbeTimeout(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline", context.DeadlineExceeded, true},
		{"wrapped net timeout", errors.Join(errors.New("dial"), &net.DNSError{IsTimeout: true}), true},
		{"net error without timeout", &net.DNSError{}, false},
		{"canceled", context.Canceled, false},
		{"plain", errors.New("refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isProbeTimeout(tc.err); got != tc.want {
				t.Fatalf("isProbeTimeout(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestGetProtectionLevel(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		want    model.ProtectionLevel
	}{
		{"none", []byte{0, 0, 0, 0}, model.ProtectionNone},
		{"write protected", []byte{0, 0, 1, 0}, model.ProtectionRead},
		{"read/write protected", []byte{0, 0, 2, 0}, model.ProtectionReadWrite},
		{"full", []byte{0, 0, 3, 0}, model.ProtectionFull},
		{"upper bits masked", []byte{0, 0, 0xFE, 0}, model.ProtectionReadWrite},
		{"payload too short", []byte{0, 0, 3}, model.ProtectionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
				return buildSZLReply(ref, wire.SZLProtectionInfo, tc.payload)
			})
			c := connectScripted(t, port)
			got, err := c.GetProtectionLevel(testContext(t))
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestGetCPUState_UnrecognisedPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"unknown state byte", []byte{0, 0, 0x02, 0}},
		{"payload too short", []byte{0, 0, 0x08}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
				return buildSZLReply(ref, wire.SZLCPUState, tc.payload)
			})
			c := connectScripted(t, port)
			got, err := c.GetCPUState(testContext(t))
			if err != nil || got != model.CPUStateUnknown {
				t.Fatalf("got %v, %v; want unknown, nil", got, err)
			}
		})
	}
}

func TestSZLOperations_ItemReturnCode(t *testing.T) {
	port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
		return buildSZLItemError(ref, wire.RetCodeAccessFault)
	})
	c := connectScripted(t, port)

	ops := map[string]func(ctx context.Context) error{
		"GetCPUState":        func(ctx context.Context) error { _, err := c.GetCPUState(ctx); return err },
		"GetProtectionLevel": func(ctx context.Context) error { _, err := c.GetProtectionLevel(ctx); return err },
		"ReadDiagBufferRaw":  func(ctx context.Context) error { _, err := c.ReadDiagBufferRaw(ctx); return err },
		"ReadDiagBuffer":     func(ctx context.Context) error { _, err := c.ReadDiagBuffer(ctx); return err },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			var s7Err *wire.S7Error
			if err := op(testContext(t)); !errors.As(err, &s7Err) || s7Err.Code != wire.RetCodeAccessFault {
				t.Fatalf("err = %v, want item return code 0x03", err)
			}
		})
	}
}

func TestReadDiagBuffer_FixedStrideDropsTrailingPartialEntry(t *testing.T) {
	raw := make([]byte, 45)
	copy(raw[0:4], []byte{0x12, 0x34, 0x05, 0x06})
	copy(raw[20:24], []byte{0xAB, 0xCD, 0x07, 0x08})
	port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte {
		return buildSZLReply(ref, wire.SZLDiagBuffer, raw)
	})
	c := connectScripted(t, port)

	buf, err := c.ReadDiagBuffer(testContext(t))
	if err != nil {
		t.Fatalf("ReadDiagBuffer: %v", err)
	}
	if buf.TotalCount != 2 || len(buf.Entries) != 2 {
		t.Fatalf("entries = %d (total %d), want 2", len(buf.Entries), buf.TotalCount)
	}
	if e := buf.Entries[1]; e.EventID != 0xABCD || e.EventClass != 0x07 || e.Priority != 0x08 {
		t.Fatalf("second entry = %+v", e)
	}
}

func TestIdentify_PartialResults(t *testing.T) {
	module := make([]byte, 22)
	copy(module[2:], "6ES7 315-2EH14-0AB0")
	component := make([]byte, 34)
	copy(component[2:], "CPU 315-2 PN/DP")
	copy(component[26:], "LINE1")

	moduleOK := func(ref uint16) []byte { return buildSZLReply(ref, wire.SZLModuleID, module) }
	componentOK := func(ref uint16) []byte { return buildSZLReply(ref, wire.SZLComponentID, component) }
	failed := func(ref uint16) []byte { return buildSZLItemError(ref, wire.RetCodeNotAvailable) }

	cases := []struct {
		name              string
		moduleReply       func(uint16) []byte
		componentReply    func(uint16) []byte
		wantInfo, wantErr bool
		wantOrder         string
		wantName          string
	}{
		{"both succeed", moduleOK, componentOK, true, false, "6ES7 315-2EH14-0AB0", "CPU 315-2 PN/DP"},
		{"both fail", failed, failed, false, true, "", ""},
		{"module fails", failed, componentOK, true, true, "", "CPU 315-2 PN/DP"},
		{"component fails", moduleOK, failed, true, true, "6ES7 315-2EH14-0AB0", ""},
		{
			"component too short", moduleOK,
			func(ref uint16) []byte { return buildSZLReply(ref, wire.SZLComponentID, make([]byte, 33)) },
			true, true, "6ES7 315-2EH14-0AB0", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
				// SZL ID sits at offset 22 of the encoded request (header 10 + param 8 + data 4).
				if binary.BigEndian.Uint16(req[22:24]) == wire.SZLModuleID {
					return tc.moduleReply(ref)
				}
				return tc.componentReply(ref)
			})
			c := connectScripted(t, port)

			info, err := c.Identify(testContext(t))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if (info != nil) != tc.wantInfo {
				t.Fatalf("info = %+v, wantInfo %v", info, tc.wantInfo)
			}
			if info == nil {
				return
			}
			if info.OrderNumber != tc.wantOrder || info.ModuleName != tc.wantName {
				t.Fatalf("info = %+v, want order %q name %q", info, tc.wantOrder, tc.wantName)
			}
		})
	}
}

func TestProbeRackSlots_SetupOutcome(t *testing.T) {
	cases := []struct {
		name  string
		setup func(ref uint16) []byte
		want  ProbeStatus
	}{
		{
			name:  "peer rejects setup",
			setup: func(ref uint16) []byte { return buildAckData(ref, wire.ErrClassAccess, 0x01, nil, nil) },
			want:  StatusRejected,
		},
		{
			name:  "peer answers garbage",
			setup: func(uint16) []byte { return []byte("not s7") },
			want:  StatusCOTPOnly,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, tc.setup, nil)
			result, err := ProbeRackSlots(testContext(t), RackSlotProbeRequest{
				Address: "127.0.0.1", Port: port, Timeout: 2 * time.Second, Parallelism: 1,
			})
			if err != nil {
				t.Fatalf("ProbeRackSlots: %v", err)
			}
			got := result.Candidates[0]
			if got.Status != tc.want || got.Stage != ProbeStageSetup || got.Error == "" {
				t.Fatalf("candidate = %+v, want status %s at setup stage", got, tc.want)
			}
			if got.Confidence != ConfidenceNone || len(result.Valid) != 0 || result.SetupAccepted != 0 {
				t.Fatalf("failed setup counted as valid: %+v", result)
			}
		})
	}
}

func TestProbeOne_InvalidTSAPRack(t *testing.T) {
	got := probeOne(context.Background(), RackSlotProbeRequest{Address: "127.0.0.1", Port: 1}, 99, 0)
	if got.Status != StatusRejected || got.Error == "" {
		t.Fatalf("candidate = %+v, want rejected before dialling", got)
	}
}

func TestProbeRackSlots_StrictFollowUpFailures(t *testing.T) {
	cases := []struct {
		name    string
		confirm ConfirmationKind
		reply   func(ref uint16) []byte
		wantErr string
	}{
		{
			name: "S7 header error", confirm: ConfirmSZL,
			reply:   func(ref uint16) []byte { return buildAckData(ref, wire.ErrClassAccess, 0x01, nil, nil) },
			wantErr: "S7 error 0x87/0x01",
		},
		{
			name: "PDU ref mismatch", confirm: ConfirmCPUState,
			reply:   func(ref uint16) []byte { return buildSZLReply(ref+1, wire.SZLCPUState, make([]byte, 4)) },
			wantErr: "PDU ref mismatch",
		},
		{
			name: "malformed header", confirm: ConfirmSZL,
			reply:   func(uint16) []byte { return []byte{0x32} },
			wantErr: wire.ErrShortS7Header.Error(),
		},
		{
			name: "item return code", confirm: ConfirmSZL,
			reply:   func(ref uint16) []byte { return buildSZLItemError(ref, wire.RetCodeAccessFault) },
			wantErr: "access denied",
		},
		{
			name: "peer closes", confirm: ConfirmAny,
			reply:   func(uint16) []byte { return nil },
			wantErr: "follow-up",
		},
		{
			name: "unknown strategy", confirm: ConfirmationKind("bogus"),
			reply:   func(uint16) []byte { return nil },
			wantErr: "no confirmation strategy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t, nil, func(ref uint16, _ []byte) []byte { return tc.reply(ref) })
			result, err := ProbeRackSlots(testContext(t), RackSlotProbeRequest{
				Address: "127.0.0.1", Port: port, Timeout: 2 * time.Second, Parallelism: 1,
				Strict: true, Confirm: tc.confirm,
			})
			if err != nil {
				t.Fatalf("ProbeRackSlots: %v", err)
			}
			got := result.Candidates[0]
			if got.Status != StatusValidConnect || got.Stage != ProbeStageQuery || got.Confidence != ConfidenceLow {
				t.Fatalf("candidate = %+v, want valid-connect at query stage", got)
			}
			if !strings.Contains(got.Error, tc.wantErr) || got.ConfirmedBy != "" {
				t.Fatalf("Error = %q, ConfirmedBy = %q; want error containing %q", got.Error, got.ConfirmedBy, tc.wantErr)
			}
			if len(result.Valid) != 0 || result.ConfirmedByQuery != 0 || result.SetupAccepted != 1 {
				t.Fatalf("strict summary wrong: %+v", result)
			}
		})
	}
}

func TestProbeS7_Abandonment(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("canceled before first attempt", func(t *testing.T) {
		got := probeS7(canceled, "127.0.0.1", 1, &discoverOpts{timeout: 50})
		if got.AbandonedReason != "context_canceled" || got.IsS7 {
			t.Fatalf("result = %+v", got)
		}
	})
	for name, opts := range map[string]*discoverOpts{
		"canceled during rate wait":   {timeout: 50, rateMS: 60000},
		"canceled during jitter wait": {timeout: 50, jitterMS: 600000},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			start := time.Now()
			got := probeS7(ctx, "127.0.0.1", 1, opts)
			if got.AbandonedReason != "context_canceled" || got.Error == "" {
				t.Fatalf("result = %+v", got)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("wait ignored context for %v", d)
			}
		})
	}
	t.Run("no candidate answers", func(t *testing.T) {
		got := probeS7(context.Background(), "127.0.0.1", 1, &discoverOpts{timeout: 50, rackMax: 1})
		if got.IsS7 || got.AbandonedReason != "" || !strings.Contains(got.Error, "no valid COTP+S7 setup") {
			t.Fatalf("result = %+v", got)
		}
	})
}

func TestDiscover_RejectsNegativeLimits(t *testing.T) {
	cases := map[string]DiscoverOption{
		"jitter":       WithDiscoverJitter(-1),
		"max attempts": WithDiscoverMaxAttemptsPerHost(-1),
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			var verr *ValidationError
			if _, err := Discover(context.Background(), "127.0.0.1/32", opt); !errors.As(err, &verr) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
		})
	}
}

func TestCompareRead_PartialFailureIsNotRackSlotInsensitive(t *testing.T) {
	var setups atomic.Int32
	port := startScriptedPeer(t,
		func(ref uint16) []byte {
			if setups.Add(1) == 2 {
				return buildAckData(ref, wire.ErrClassAccess, 0x01, nil, nil)
			}
			return buildS7SetupResponse(ref, 480)
		},
		func(ref uint16, _ []byte) []byte { return buildReadVarResponse(ref, 16, []byte{1, 2}) })

	out, err := CompareRead(testContext(t), CompareReadRequest{
		Address: "127.0.0.1", Port: port, Area: model.AreaMerkers, Size: 2, Timeout: 2 * time.Second,
		Candidates: []RackSlot{{Rack: 0, Slot: 1}, {Rack: 0, Slot: 2}},
	})
	if err != nil {
		t.Fatalf("CompareRead: %v", err)
	}
	if out.ByCandidate[0].Result.Status != ReadStatusSuccess {
		t.Fatalf("first candidate = %+v", out.ByCandidate[0].Result)
	}
	if out.ByCandidate[1].Result.Status == ReadStatusSuccess {
		t.Fatalf("second candidate should fail: %+v", out.ByCandidate[1].Result)
	}
	if out.RackSlotInsensitive {
		t.Fatal("RackSlotInsensitive must be false when a candidate failed")
	}
}

func TestCompareRead_CanceledMarksEveryCandidate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := CompareRead(ctx, CompareReadRequest{
		Address: "127.0.0.1", Port: 1, Area: model.AreaMerkers, Size: 2, Parallelism: 1,
		Candidates: []RackSlot{{Rack: 0, Slot: 1}, {Rack: 0, Slot: 2}, {Rack: 1, Slot: 3}},
	})
	if err != nil {
		t.Fatalf("CompareRead: %v", err)
	}
	if len(out.ByCandidate) != 3 {
		t.Fatalf("candidates = %d, want 3", len(out.ByCandidate))
	}
	for i, cand := range out.ByCandidate {
		if cand.Result.Status == ReadStatusSuccess || cand.Result.Status == "" {
			t.Fatalf("candidate %d status = %q, want a failure", i, cand.Result.Status)
		}
	}
	if last := out.ByCandidate[2]; last.Rack != 1 || last.Slot != 3 {
		t.Fatalf("candidate identity lost: %+v", last)
	}
	if out.RackSlotInsensitive {
		t.Fatal("RackSlotInsensitive must be false when every candidate failed")
	}
}

func TestUploadBlock_UnknownBlockTypeSendsNothing(t *testing.T) {
	var requests atomic.Int32
	port := startScriptedPeer(t, nil, func(uint16, []byte) []byte {
		requests.Add(1)
		return nil
	})
	c := connectScripted(t, port)

	var verr *ValidationError
	if _, err := c.UploadBlock(testContext(t), model.BlockType(0x99), 1); !errors.As(err, &verr) {
		t.Fatalf("err = %v, want *ValidationError", err)
	}
	if !strings.Contains(verr.Message, "0x99") {
		t.Fatalf("Message = %q, want the raw block type", verr.Message)
	}
	// A follow-up request proves the session is intact and nothing was sent before it.
	if _, err := c.GetCPUState(testContext(t)); err == nil {
		t.Fatal("expected the scripted peer to close on the follow-up request")
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("peer saw %d requests, want only the follow-up", n)
	}
}
