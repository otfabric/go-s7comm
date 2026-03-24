package client

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-s7comm/model"
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
