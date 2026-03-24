package client

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/otfabric/go-s7comm/model"
)

// RackSlot is a rack/slot pair for compare or probe operations.
type RackSlot struct {
	Rack int
	Slot int
}

// CompareReadRequest is the same read across multiple rack/slot candidates.
type CompareReadRequest struct {
	Address    string
	Port       int
	Candidates []RackSlot
	Area       model.Area
	DBNumber   int
	Offset     int
	Size       int
	Timeout    time.Duration
	// Parallelism limits concurrent connections. Zero or negative is treated as 1 (sequential).
	// Results remain in candidate order. Aligns with probe/discovery parallelism options.
	Parallelism int
}

// CompareReadCandidate holds one candidate's read result.
type CompareReadCandidate struct {
	Rack   int
	Slot   int
	Result ReadResult
}

// CompareReadResult holds one ReadResult per candidate and whether all candidates agreed.
// RackSlotInsensitive is true only when every candidate succeeded and all returned identical data.
type CompareReadResult struct {
	Request             CompareReadRequest
	ByCandidate         []CompareReadCandidate
	RackSlotInsensitive bool
}

// compareCandidateFailure builds a CompareReadCandidate for a failed read.
func compareCandidateFailure(c RackSlot, size int, err error) CompareReadCandidate {
	return CompareReadCandidate{
		Rack:   c.Rack,
		Slot:   c.Slot,
		Result: *newFailedReadResult(size, err),
	}
}

// CompareRead performs the same read for each rack/slot candidate and reports whether results are identical.
// Use it to test whether a target responds identically across multiple rack/slot candidates.
// Each candidate uses a new connection (connect, read, close). If Parallelism > 1, up to that many
// connections run concurrently; results are still in candidate order. If all candidates return
// ReadStatusSuccess and the returned data is identical, RackSlotInsensitive is set true.
// Invalid request (negative size/offset/DBNumber for DB) returns an error.
func CompareRead(ctx context.Context, req CompareReadRequest) (*CompareReadResult, error) {
	if err := validateCompareReadRequest(req); err != nil {
		return nil, err
	}
	out := &CompareReadResult{Request: req}
	if len(req.Candidates) == 0 {
		return out, nil
	}

	port := req.Port
	if port == 0 {
		port = 102
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	results := make([]CompareReadCandidate, len(req.Candidates))
	var wg sync.WaitGroup
	parallelism := req.Parallelism
	if parallelism <= 0 {
		parallelism = 1
	}
	sem := make(chan struct{}, parallelism)
	for i, cand := range req.Candidates {
		select {
		case <-ctx.Done():
			for j := i; j < len(req.Candidates); j++ {
				results[j] = compareCandidateFailure(req.Candidates[j], req.Size, ctx.Err())
			}
			goto wait
		case sem <- struct{}{}:
		}
		idx := i
		cand := cand
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c := New(req.Address, WithPort(port), WithRackSlot(cand.Rack, cand.Slot), WithTimeout(timeout))
			if err := c.Connect(ctx); err != nil {
				results[idx] = compareCandidateFailure(cand, req.Size, err)
				_ = c.Close()
				return
			}
			res, err := c.ReadArea(ctx, model.Address{Area: req.Area, DBNumber: req.DBNumber, Start: req.Offset, Size: req.Size})
			_ = c.Close()
			if err != nil {
				results[idx] = compareCandidateFailure(cand, req.Size, err)
				return
			}
			results[idx] = CompareReadCandidate{Rack: cand.Rack, Slot: cand.Slot, Result: *res}
		}()
	}
wait:
	wg.Wait()
	out.ByCandidate = results

	// Set RackSlotInsensitive if all succeeded and all Data is identical
	if len(out.ByCandidate) < 2 {
		return out, nil
	}
	firstOK := -1
	for i := range out.ByCandidate {
		if out.ByCandidate[i].Result.Status == ReadStatusSuccess {
			firstOK = i
			break
		}
	}
	if firstOK < 0 {
		return out, nil
	}
	ref := out.ByCandidate[firstOK].Result.Data
	allSame := true
	for i := range out.ByCandidate {
		if out.ByCandidate[i].Result.Status != ReadStatusSuccess {
			allSame = false
			break
		}
		if !bytes.Equal(out.ByCandidate[i].Result.Data, ref) {
			allSame = false
			break
		}
	}
	out.RackSlotInsensitive = allSame
	return out, nil
}
