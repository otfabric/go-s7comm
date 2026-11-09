// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
)

var errScanBlocked = errors.New("blocked")

// redirectDialer dials target whatever address it is asked for, the way a
// proxy dialer would, and records each requested address and whether its
// context carried a deadline.
type redirectDialer struct {
	target string

	mu          sync.Mutex
	addrs       []string
	noDeadlines int
}

func (r *redirectDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	r.mu.Lock()
	r.addrs = append(r.addrs, addr)
	if _, ok := ctx.Deadline(); !ok {
		r.noDeadlines++
	}
	r.mu.Unlock()
	var d net.Dialer
	return d.DialContext(ctx, network, r.target)
}

func (r *redirectDialer) calls() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.addrs...), r.noDeadlines
}

func countingErrDialer(calls *atomic.Int32, err error) Dialer {
	return func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, err
	}
}

func TestDiscoverDialerUsed(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()
	rd := &redirectDialer{target: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}

	// The device address is unroutable (TEST-NET-1); only the dialer reaches the fake server.
	results, err := Discover(context.Background(), "192.0.2.1/32",
		WithDiscoverRackSlotRange(0, 0, 1, 1),
		WithDiscoverTimeout(2000),
		WithDiscoverDialer(rd.dial),
	)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 1 || !results[0].IsS7 {
		t.Fatalf("results = %+v, want one S7 device found through the dialer", results)
	}
	addrs, noDeadlines := rd.calls()
	if len(addrs) != 1 || addrs[0] != "192.0.2.1:102" {
		t.Errorf("dialer addrs = %v, want [192.0.2.1:102]", addrs)
	}
	if noDeadlines != 0 {
		t.Error("dialer context has no deadline from the discover timeout")
	}
}

func TestDiscoverDialerErrorPerAttempt(t *testing.T) {
	var calls atomic.Int32
	results, err := Discover(context.Background(), "192.0.2.0/31",
		WithDiscoverRackSlotRange(0, 0, 0, 2),
		WithDiscoverDialer(countingErrDialer(&calls, errScanBlocked)),
	)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, r := range results {
		if r.IsS7 {
			t.Errorf("%s reported as S7 although every dial failed", r.IP)
		}
	}
	// 2 hosts × 3 slots, each attempt dials once.
	if got := calls.Load(); got != 6 {
		t.Errorf("dialer called %d times, want 6", got)
	}
}

func TestDiscoverDialerNilUsesDefault(t *testing.T) {
	// A nil dialer must fall back to net.Dialer, not panic.
	results, err := Discover(context.Background(), "127.0.0.1/32",
		WithDiscoverRackSlotRange(0, 0, 1, 1),
		WithDiscoverTimeout(200),
		WithDiscoverDialer(nil),
	)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestProbeRackSlotsDialerUsed(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()
	rd := &redirectDialer{target: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}

	result, err := ProbeRackSlots(context.Background(), RackSlotProbeRequest{
		Address:     "192.0.2.1",
		Port:        1102,
		RackMin:     0,
		RackMax:     0,
		SlotMin:     1,
		SlotMax:     1,
		Timeout:     2 * time.Second,
		Parallelism: 1,
		Dialer:      rd.dial,
	})
	if err != nil {
		t.Fatalf("ProbeRackSlots: %v", err)
	}
	if result.SetupAccepted != 1 {
		t.Fatalf("candidates = %+v, want setup accepted through the dialer", result.Candidates)
	}
	addrs, noDeadlines := rd.calls()
	if len(addrs) != 1 || addrs[0] != "192.0.2.1:1102" {
		t.Errorf("dialer addrs = %v, want [192.0.2.1:1102]", addrs)
	}
	if noDeadlines != 0 {
		t.Error("dialer context has no deadline from Timeout")
	}
}

func TestProbeRackSlotsDialerError(t *testing.T) {
	var calls atomic.Int32
	result, err := ProbeRackSlots(context.Background(), RackSlotProbeRequest{
		Address:     "192.0.2.1",
		Port:        102,
		RackMin:     0,
		RackMax:     1,
		SlotMin:     0,
		SlotMax:     1,
		Timeout:     time.Second,
		Parallelism: 2,
		Dialer:      countingErrDialer(&calls, errScanBlocked),
	})
	if err != nil {
		t.Fatalf("ProbeRackSlots: %v", err)
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("dialer called %d times, want 4", got)
	}
	for _, c := range result.Candidates {
		if c.Stage != ProbeStageTCP || c.Status != StatusUnreachable {
			t.Errorf("rack %d slot %d: stage %q status %q, want tcp/unreachable", c.Rack, c.Slot, c.Stage, c.Status)
		}
		if c.Error != "TCP connect: blocked" {
			t.Errorf("rack %d slot %d: error %q, want the wrapped dialer error", c.Rack, c.Slot, c.Error)
		}
	}
}

func TestProbeRackSlotsDialerTimeout(t *testing.T) {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	result, err := ProbeRackSlots(context.Background(), RackSlotProbeRequest{
		Address:     "192.0.2.1",
		RackMin:     0,
		RackMax:     0,
		SlotMin:     1,
		SlotMax:     1,
		Timeout:     20 * time.Millisecond,
		Parallelism: 1,
		Dialer:      dial,
	})
	if err != nil {
		t.Fatalf("ProbeRackSlots: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Status != StatusTimeout {
		t.Fatalf("candidates = %+v, want one timeout", result.Candidates)
	}
}

func TestCompareReadDialerUsed(t *testing.T) {
	port, cleanup := startFakeSetupAndReadServerMultiAccept(t, 480)
	defer cleanup()
	rd := &redirectDialer{target: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}

	result, err := CompareRead(context.Background(), CompareReadRequest{
		Address:     "192.0.2.1",
		Candidates:  []RackSlot{{0, 1}, {0, 2}},
		Area:        model.AreaDB,
		DBNumber:    1,
		Size:        2,
		Timeout:     2 * time.Second,
		Parallelism: 2,
		Dialer:      rd.dial,
	})
	if err != nil {
		t.Fatalf("CompareRead: %v", err)
	}
	for _, c := range result.ByCandidate {
		if c.Result.Status != ReadStatusSuccess {
			t.Errorf("rack %d slot %d: status %v (%s), want success through the dialer", c.Rack, c.Slot, c.Result.Status, c.Result.Message)
		}
	}
	if !result.RackSlotInsensitive {
		t.Error("RackSlotInsensitive = false, want true for identical data")
	}
	addrs, noDeadlines := rd.calls()
	if len(addrs) != 2 || addrs[0] != "192.0.2.1:102" || addrs[1] != "192.0.2.1:102" {
		t.Errorf("dialer addrs = %v, want two dials of 192.0.2.1:102", addrs)
	}
	if noDeadlines != 0 {
		t.Error("dialer context has no deadline from Timeout")
	}
}

func TestCompareReadDialerError(t *testing.T) {
	var calls atomic.Int32
	result, err := CompareRead(context.Background(), CompareReadRequest{
		Address:    "192.0.2.1",
		Candidates: []RackSlot{{0, 1}, {0, 2}, {0, 3}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Size:       2,
		Dialer:     countingErrDialer(&calls, errScanBlocked),
	})
	if err != nil {
		t.Fatalf("CompareRead: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("dialer called %d times, want 3", got)
	}
	for _, c := range result.ByCandidate {
		if !errors.Is(c.Result.Cause, errScanBlocked) {
			t.Errorf("rack %d slot %d: cause %v, want it to wrap the dialer error", c.Rack, c.Slot, c.Result.Cause)
		}
		if !strings.Contains(c.Result.Message, "TCP connect: blocked") {
			t.Errorf("rack %d slot %d: message %q, want the TCP connect prefix", c.Rack, c.Slot, c.Result.Message)
		}
	}
	if result.RackSlotInsensitive {
		t.Error("RackSlotInsensitive = true although every candidate failed")
	}
}

// The request structs are echoed in results that callers marshal; a func
// field must not make encoding/json fail.
func TestScanRequestsJSONWithDialer(t *testing.T) {
	dial := countingErrDialer(new(atomic.Int32), errScanBlocked)
	for name, v := range map[string]any{
		"probe":   RackSlotProbeRequest{Address: "192.0.2.1", Dialer: dial},
		"compare": CompareReadResult{Request: CompareReadRequest{Address: "192.0.2.1", Dialer: dial}},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Errorf("%s: json.Marshal: %v", name, err)
			continue
		}
		if strings.Contains(string(b), "Dialer") {
			t.Errorf("%s: JSON %s contains the dialer", name, b)
		}
	}
}
