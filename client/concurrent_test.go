// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
)

// TestReadAreaConcurrent exercises multiple goroutines calling ReadArea on the same client.
func TestReadAreaConcurrent(t *testing.T) {
	port, cleanup := startFakeSetupAndReadServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port), WithTimeout(2*time.Second))
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	const concurrency = 8
	var wg sync.WaitGroup
	results := make([]*ReadResult, concurrency)
	for i := range concurrency {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 2})
			if err != nil {
				t.Errorf("goroutine %d: ReadArea err: %v", idx, err)
				return
			}
			results[idx] = res
		}(i)
	}
	wg.Wait()

	for i, res := range results {
		if res == nil {
			continue
		}
		if !res.OK() {
			t.Errorf("goroutine %d: result not OK: status=%s err=%s", i, res.Status, res.Message)
		}
		if len(res.Data) < 2 {
			t.Errorf("goroutine %d: expected at least 2 bytes, got %d", i, len(res.Data))
		}
	}
}

// TestConnectWhileConnected documents that a second Connect() disconnects and reconnects; client remains usable.
func TestConnectWhileConnected(t *testing.T) {
	port, cleanup := startFakeSetupAndReadServerMultiAccept(t, 480)
	defer cleanup()

	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port), WithTimeout(2*time.Second))
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("second Connect (while connected): %v", err)
	}
	res, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 2})
	if err != nil {
		t.Fatalf("ReadArea after double Connect: %v", err)
	}
	if !res.OK() {
		t.Errorf("ReadArea after double Connect: status=%s", res.Status)
	}
}
