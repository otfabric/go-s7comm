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
	for i := 0; i < concurrency; i++ {
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

// TestCloseRacingRead ensures Close() racing with ReadArea does not panic and ReadArea either succeeds or returns an error.
func TestCloseRacingRead(t *testing.T) {
	port, cleanup := startFakeSetupAndReadServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port), WithTimeout(500*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			default:
				_, _ = c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 2})
			}
		}
	}()

	time.Sleep(20 * time.Millisecond)
	_ = c.Close()
	close(done)
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

// TestConnectRacingClose ensures Connect() and Close() can be called concurrently without panic.
func TestConnectRacingClose(t *testing.T) {
	port, cleanup := startFakeSetupServer(t, 480)
	defer cleanup()

	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port), WithTimeout(100*time.Millisecond))
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = c.Connect(ctx)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		_ = c.Close()
	}()
	wg.Wait()
}
