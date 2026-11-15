// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

func TestConsolidateSpans_Empty(t *testing.T) {
	spans, summary := ConsolidateSpans(nil, 8, 8)
	if len(spans) != 0 {
		t.Errorf("expected 0 spans, got %d", len(spans))
	}
	if len(summary.ReadableSpans) != 0 || len(summary.EmptySpans) != 0 {
		t.Errorf("expected empty summary")
	}
}

func TestConsolidateSpans_SingleProbe(t *testing.T) {
	obs := []ReadProbeObservation{
		{Offset: 0, Result: ReadResult{Status: ReadStatusSuccess, RequestedLength: 8, ReturnedLength: 8, Data: make([]byte, 8)}},
	}
	spans, summary := ConsolidateSpans(obs, 8, 8)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Start != 0 || spans[0].End != 8 || spans[0].Status != ReadStatusSuccess {
		t.Errorf("span: Start=%d End=%d Status=%q", spans[0].Start, spans[0].End, spans[0].Status)
	}
	if len(summary.ReadableSpans) != 1 {
		t.Errorf("expected 1 readable span in summary, got %d", len(summary.ReadableSpans))
	}
}

func TestConsolidateSpans_MergesAdjacentReadable(t *testing.T) {
	obs := []ReadProbeObservation{
		{Offset: 0, Result: ReadResult{Status: ReadStatusSuccess, RequestedLength: 8, ReturnedLength: 8}},
		{Offset: 8, Result: ReadResult{Status: ReadStatusSuccess, RequestedLength: 8, ReturnedLength: 8}},
		{Offset: 16, Result: ReadResult{Status: ReadStatusSuccess, RequestedLength: 8, ReturnedLength: 8}},
		{Offset: 24, Result: ReadResult{Status: ReadStatusEmptyRead, RequestedLength: 8, ReturnedLength: 0}},
	}
	spans, summary := ConsolidateSpans(obs, 8, 8)
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (one readable 0..24, one empty 24..32), got %d", len(spans))
	}
	if spans[0].Start != 0 || spans[0].End != 24 || spans[0].Status != ReadStatusSuccess {
		t.Errorf("first span: Start=%d End=%d Status=%q", spans[0].Start, spans[0].End, spans[0].Status)
	}
	if spans[1].Start != 24 || spans[1].End != 32 || spans[1].Status != ReadStatusEmptyRead {
		t.Errorf("second span: Start=%d End=%d Status=%q", spans[1].Start, spans[1].End, spans[1].Status)
	}
	if len(summary.ReadableSpans) != 1 || summary.ReadableSpans[0].End != 24 {
		t.Errorf("summary.ReadableSpans: %+v", summary.ReadableSpans)
	}
	if len(summary.EmptySpans) != 1 {
		t.Errorf("expected 1 empty span in summary, got %d", len(summary.EmptySpans))
	}
}

func TestConsolidateSpans_NonAdjacentNotMerged(t *testing.T) {
	obs := []ReadProbeObservation{
		{Offset: 0, Result: ReadResult{Status: ReadStatusSuccess, RequestedLength: 8, ReturnedLength: 8}},
		{Offset: 16, Result: ReadResult{Status: ReadStatusSuccess, RequestedLength: 8, ReturnedLength: 8}},
	}
	spans, _ := ConsolidateSpans(obs, 8, 8)
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (gap at 8), got %d", len(spans))
	}
	if spans[0].End != 8 || spans[1].Start != 16 {
		t.Errorf("spans should not merge across gap: %+v", spans)
	}
}

func BenchmarkConsolidateSpans(b *testing.B) {
	obs := make([]ReadProbeObservation, 200)
	for i := range obs {
		status := ReadStatusSuccess
		if i%10 == 9 {
			status = ReadStatusEmptyRead
		}
		obs[i] = ReadProbeObservation{
			Offset: i * 8,
			Result: ReadResult{Status: status, RequestedLength: 8, ReturnedLength: 8},
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ConsolidateSpans(obs, 8, 8)
	}
}

func TestProbeReadableRanges_EmptyRange(t *testing.T) {
	c := New("127.0.0.1")
	defer func() { _ = c.Close() }()
	req := RangeProbeRequest{
		Area:      model.AreaInputs,
		Start:     0,
		End:       0,
		Step:      8,
		ProbeSize: 8,
	}
	result, err := c.ProbeReadableRanges(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Probes) != 0 {
		t.Errorf("expected 0 probes, got %d", len(result.Probes))
	}
	if len(result.Spans) != 0 {
		t.Errorf("expected 0 spans, got %d", len(result.Spans))
	}
}

func TestProbeReadableRanges_InvalidRequest(t *testing.T) {
	c := New("127.0.0.1")
	defer func() { _ = c.Close() }()
	_, err := c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaDB, DBNumber: -1, Start: 0, End: 16, ProbeSize: 8,
	})
	if err == nil {
		t.Fatal("expected error for negative DBNumber")
	}
	_, err = c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaInputs, Start: 20, End: 10, ProbeSize: 8,
	})
	if err == nil {
		t.Fatal("expected error for start > end")
	}
	_, err = c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaInputs, Start: 0, End: 16, ProbeSize: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative ProbeSize")
	}
	_, err = c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaInputs, Start: 0, End: 16, ProbeSize: 8, Retries: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative Retries")
	}
	_, err = c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaInputs, Start: 0, End: 16, ProbeSize: 8, Repeat: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative Repeat")
	}
	_, err = c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaInputs, Start: 0, End: 16, ProbeSize: 8, Parallelism: -1,
	})
	if err == nil {
		t.Fatal("expected error for negative Parallelism")
	}
}

func TestProbeReadableRanges_RequiresConnectedClient(t *testing.T) {
	c := New("127.0.0.1")
	defer func() { _ = c.Close() }()
	req := RangeProbeRequest{
		Area:      model.AreaInputs,
		Start:     0,
		End:       16,
		Step:      8,
		ProbeSize: 8,
	}
	_, err := c.ProbeReadableRanges(context.Background(), req)
	if err == nil {
		t.Error("expected error when client not connected")
	}
	if err != nil && !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

// The scanned range lies inside the S7 byte address space: a request
// outside it returns a ValidationError before any offset is built or
// anything is sent, whatever its step.
func TestProbeReadableRanges_RangeBounds(t *testing.T) {
	const limit = wire.MaxS7AnyByteOffset + 1 // one past the last byte address
	c := New("127.0.0.1")                     // never connected: validation comes first
	for _, tc := range []struct {
		name             string
		start, end, step int
		valid            bool
	}{
		{"empty at 0", 0, 0, 1, true},
		{"first byte", 0, 1, 1, true},
		{"last byte", limit - 1, limit, 1, true},
		{"empty at the end", limit, limit, 1, true},
		{"whole space, one probe", 0, limit, limit, true},
		{"step larger than any range", 8, 16, math.MaxInt, true},
		{"start -1", -1, 8, 1, false},
		{"start -16", -16, 0, 8, false},
		{"start most negative", math.MinInt, 0, 1, false},
		{"end one past the space", 0, limit + 1, 1, false},
		{"start past the space", limit + 1, limit + 1, 1, false},
		{"end at the 32-bit boundary", 0, math.MaxInt32, math.MaxInt32, false},
		{"end largest int", 0, math.MaxInt, 1, false},
		{"end largest int, large step", 8, math.MaxInt, math.MaxInt - 1, false},
		{"start after end", 16, 8, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := RangeProbeRequest{Area: model.AreaMerkers, Start: tc.start, End: tc.end, Step: tc.step, ProbeSize: 1}
			var res *RangeProbeResult
			var err error
			allocated := allocatedBy(func() { res, err = c.ProbeReadableRanges(context.Background(), req) })
			var ve *ValidationError
			if !tc.valid {
				if !errors.As(err, &ve) || res != nil {
					t.Fatalf("err = %v, want *ValidationError and no result", err)
				}
				if allocated > 4096 {
					t.Errorf("allocated %d bytes for a refused range, budget 4096", allocated)
				}
				return
			}
			if errors.As(err, &ve) {
				t.Fatalf("a range inside the address space was refused: %v", err)
			}
			// Not connected: an empty range returns an empty result, any
			// other one stops at the connection check.
			if tc.start == tc.end {
				if err != nil || res == nil || len(res.Probes) != 0 {
					t.Fatalf("empty range: %+v, %v", res, err)
				}
			} else if !errors.Is(err, ErrNotConnected) {
				t.Fatalf("err = %v, want ErrNotConnected", err)
			}
		})
	}
}

// Offsets run from Start in steps of Step and stay below End, up to the last
// byte address and for a step that no int sum could hold.
func TestProbeReadableRanges_Offsets(t *testing.T) {
	const limit = wire.MaxS7AnyByteOffset + 1
	port, cleanup := startFakeBitServer(t, func(ref uint16, req []byte) []byte {
		return readVarAnswer(ref, []byte{0x5A})
	})
	defer cleanup()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	for _, tc := range []struct {
		start, end, step int
		want             []int
	}{
		{0, 4, 1, []int{0, 1, 2, 3}},
		{1, 10, 4, []int{1, 5, 9}},
		{0, 9, 4, []int{0, 4, 8}},
		{limit - 3, limit, 2, []int{limit - 3, limit - 1}},
		{8, 16, math.MaxInt, []int{8}},
		{8, 16, 0, []int{8, 9, 10, 11, 12, 13, 14, 15}}, // step 0: the probe size
	} {
		res, err := c.ProbeReadableRanges(testContext(t), RangeProbeRequest{
			Area: model.AreaMerkers, Start: tc.start, End: tc.end, Step: tc.step, ProbeSize: 1,
		})
		if err != nil {
			t.Fatalf("[%d,%d) step %d: %v", tc.start, tc.end, tc.step, err)
		}
		var got []int
		for _, p := range res.Probes {
			got = append(got, p.Offset)
			if p.Result.Status != ReadStatusSuccess || p.Request.Start != p.Offset {
				t.Errorf("[%d,%d) step %d: probe at %d: status %s, request %+v", tc.start, tc.end, tc.step, p.Offset, p.Result.Status, p.Request)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("[%d,%d) step %d: offsets %v, want %v", tc.start, tc.end, tc.step, got, tc.want)
		}
	}
}
