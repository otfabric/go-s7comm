package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

func TestReadAreaInvalidAddress(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	_, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: -1, Start: 0, Size: 4})
	if err == nil {
		t.Fatal("expected error for negative DBNumber")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T", err)
	}
	_, err = c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: -1, Size: 4})
	if err == nil {
		t.Fatal("expected error for negative Start")
	}
	_, err = c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: -1})
	if err == nil {
		t.Fatal("expected error for negative Size")
	}
}

func TestReadArea_RequestExceedsNegotiatedPDU(t *testing.T) {
	// Server negotiates PDU size 20; minimum read request is 26 bytes → sendReceive must reject before send.
	port, cleanup := startFakeSetupServer(t, 20)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	defer func() { _ = c.Close() }()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	res, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 1})
	if err != nil {
		t.Fatalf("ReadArea: %v", err)
	}
	if res.OK() {
		t.Error("expected read to fail when request exceeds PDU size")
	}
	if res.Status != ReadStatusProtocolErr {
		t.Errorf("expected protocol error (request exceeds PDU), got %s", res.Status)
	}
	if !errors.Is(res.Err(), ErrRequestExceedsPDU) {
		t.Errorf("expected ErrRequestExceedsPDU in chain, got %q", res.Message)
	}
}

func TestReadAreaNotConnected(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	res, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 4})
	if err != nil {
		t.Fatalf("ReadArea should return (result, nil), got err: %v", err)
	}
	if res.Status != ReadStatusTransportErr {
		t.Errorf("expected Status TransportErr, got %s", res.Status)
	}
	if !errors.Is(res.Err(), ErrNotConnected) && res.Message != "not connected" {
		t.Errorf("expected not connected error, got %q", res.Message)
	}
}

func TestWriteAreaInvalidAddress(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	err := c.WriteArea(ctx, model.Address{Area: model.AreaDB, DBNumber: -1, Start: 0, Size: 2}, []byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for negative DBNumber")
	}
	err = c.WriteArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: -1, Size: 2}, []byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for negative Start")
	}
}

func TestWriteAreaNotConnected(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	err := c.WriteArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 2}, []byte{0x01, 0x02})
	if err == nil {
		t.Fatal("WriteArea without connection should return error")
	}
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %q", err.Error())
	}
}

func TestReadAreaContextCancelled(t *testing.T) {
	c := New("host")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 4})
	if err != nil {
		t.Fatalf("ReadArea should return (result, nil), got err: %v", err)
	}
	if res.Status != ReadStatusTimeout && res.Status != ReadStatusTransportErr {
		t.Errorf("expected Timeout or TransportErr, got %s", res.Status)
	}
}

func TestPlanReadChunks(t *testing.T) {
	got := planReadChunks(20, 6)
	want := []int{6, 6, 6, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected chunks: got=%v want=%v", got, want)
	}
}

func TestPlanReadChunksEdgeCases(t *testing.T) {
	if got := planReadChunks(0, 8); len(got) != 0 {
		t.Fatalf("expected no chunks, got %v", got)
	}
	if got := planReadChunks(5, 0); !reflect.DeepEqual(got, []int{5}) {
		t.Fatalf("unexpected fallback chunking: %v", got)
	}
}

func TestClassifyOpError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ReadStatus
	}{
		{"nil", nil, ReadStatusSuccess},
		{"deadline exceeded", context.DeadlineExceeded, ReadStatusTimeout},
		{"not connected", ErrNotConnected, ReadStatusTransportErr},
		{"S7 error", wire.NewS7Error(wire.ErrClassAccess, 0x05), ReadStatusRejected},
		{"short S7 header", wire.ErrShortS7Header, ReadStatusProtocolErr},
		{"protocol failure sentinel", ErrProtocolFailure, ReadStatusProtocolErr},
		{"wrapped protocol failure", fmt.Errorf("decode COTP: %w", ErrProtocolFailure), ReadStatusProtocolErr},
		{"request exceeds PDU", ErrRequestExceedsPDU, ReadStatusProtocolErr},
		{"wrapped request exceeds PDU", fmt.Errorf("request size 100 exceeds negotiated PDU size 50: %w", ErrRequestExceedsPDU), ReadStatusProtocolErr},
		{"generic", errors.New("network failure"), ReadStatusTransportErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyOpError(tt.err)
			if got != tt.want {
				t.Errorf("classifyOpError() = %q, want %q", got, tt.want)
			}
		})
	}
	// net.Error timeout (best-effort: DialTimeout may not always return timeout on all systems)
	_, err := net.DialTimeout("tcp", "127.0.0.1:1", time.Nanosecond)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			got := classifyOpError(err)
			if got != ReadStatusTimeout {
				t.Errorf("net timeout error: got %q, want Timeout", got)
			}
		}
	}
}

func TestNewFailedReadResult(t *testing.T) {
	r := newFailedReadResult(10, ErrNotConnected)
	if r.RequestedLength != 10 || r.ReturnedLength != 0 || r.Status != ReadStatusTransportErr {
		t.Errorf("newFailedReadResult: got %+v", r)
	}
	r2 := newFailedReadResult(0, context.DeadlineExceeded)
	if r2.Status != ReadStatusTimeout {
		t.Errorf("newFailedReadResult(deadline): got Status %q", r2.Status)
	}
}

func BenchmarkPlanReadChunks(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = planReadChunks(4096, 200)
	}
}
