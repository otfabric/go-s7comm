// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"testing"
	"time"
)

// waitBounded fails the test when fn does not return in time.
func waitBounded[T any](t *testing.T, limit time.Duration, fn func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- fn() }()
	select {
	case v := <-done:
		return v
	case <-time.After(limit):
		t.Fatalf("call did not return within %v", limit)
		panic("unreachable")
	}
}

// A peer that completes COTP and then ignores S7 setup must not hold Connect past WithTimeout.
func TestConnect_WithTimeoutBoundsS7Setup(t *testing.T) {
	port := startScriptedPeer(t, func(uint16) []byte { return nil }, nil)
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(100*time.Millisecond))

	err := waitBounded(t, 3*time.Second, func() error { return c.Connect(context.Background()) })
	if !isProbeTimeout(err) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if c.PDUSize() != 0 {
		t.Fatalf("PDUSize = %d after timed-out setup", c.PDUSize())
	}
}

// Timeout is documented as per attempt, so a silent peer must classify as timeout at the setup stage.
func TestProbeRackSlots_TimeoutBoundsS7Setup(t *testing.T) {
	port := startScriptedPeer(t, func(uint16) []byte { return nil }, nil)
	req := RackSlotProbeRequest{Address: "127.0.0.1", Port: port, Timeout: 100 * time.Millisecond, Parallelism: 1}

	type outcome struct {
		result *RackSlotProbeResult
		err    error
	}
	got := waitBounded(t, 3*time.Second, func() outcome {
		r, err := ProbeRackSlots(context.Background(), req)
		return outcome{r, err}
	})
	if got.err != nil {
		t.Fatalf("ProbeRackSlots: %v", got.err)
	}
	if c := got.result.Candidates[0]; c.Status != StatusTimeout || c.Stage != ProbeStageSetup {
		t.Fatalf("candidate = %+v, want timeout at setup stage", c)
	}
}

// A strict follow-up on a silent peer is part of the same attempt.
func TestProbeRackSlots_TimeoutBoundsFollowUp(t *testing.T) {
	port := startScriptedPeer(t, nil, nil)
	req := RackSlotProbeRequest{
		Address: "127.0.0.1", Port: port, Timeout: 100 * time.Millisecond, Parallelism: 1,
		Strict: true, Confirm: ConfirmSZL,
	}

	type outcome struct {
		result *RackSlotProbeResult
		err    error
	}
	got := waitBounded(t, 3*time.Second, func() outcome {
		r, err := ProbeRackSlots(context.Background(), req)
		return outcome{r, err}
	})
	if got.err != nil {
		t.Fatalf("ProbeRackSlots: %v", got.err)
	}
	if c := got.result.Candidates[0]; c.Status != StatusValidConnect || c.Stage != ProbeStageQuery {
		t.Fatalf("candidate = %+v, want valid-connect at query stage", c)
	}
}
