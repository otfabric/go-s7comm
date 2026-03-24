package client

import (
	"context"
	"encoding/binary"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/transport"
	"github.com/otfabric/go-s7comm/wire"
)

func TestCompareRead_InvalidRequest(t *testing.T) {
	_, err := CompareRead(context.Background(), CompareReadRequest{
		Address:    "   ",
		Candidates: []RackSlot{{0, 1}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     0,
		Size:       8,
	})
	if err == nil {
		t.Fatal("expected error for empty/whitespace address")
	}
	_, err = CompareRead(context.Background(), CompareReadRequest{
		Address:    "127.0.0.1",
		Port:       -1,
		Candidates: []RackSlot{{0, 1}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     0,
		Size:       8,
	})
	if err == nil {
		t.Fatal("expected error for negative port")
	}
	_, err = CompareRead(context.Background(), CompareReadRequest{
		Address:    "127.0.0.1",
		Candidates: []RackSlot{{0, 1}},
		Area:       model.AreaDB,
		DBNumber:   -1,
		Offset:     0,
		Size:       8,
	})
	if err == nil {
		t.Fatal("expected error for negative DBNumber")
	}
	_, err = CompareRead(context.Background(), CompareReadRequest{
		Address:    "127.0.0.1",
		Candidates: []RackSlot{{0, 1}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     -1,
		Size:       8,
	})
	if err == nil {
		t.Fatal("expected error for negative Offset")
	}
	_, err = CompareRead(context.Background(), CompareReadRequest{
		Address:    "127.0.0.1",
		Candidates: []RackSlot{{0, 1}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     0,
		Size:       -1,
	})
	if err == nil {
		t.Fatal("expected error for negative Size")
	}
}

func TestCompareRead_EmptyCandidates(t *testing.T) {
	result, err := CompareRead(context.Background(), CompareReadRequest{
		Address:    "192.168.0.1",
		Candidates: nil,
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     0,
		Size:       8,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.ByCandidate) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(result.ByCandidate))
	}
	if result.RackSlotInsensitive {
		t.Error("RackSlotInsensitive should be false when no candidates")
	}
}

func TestCompareRead_SingleCandidate(t *testing.T) {
	// Use a port unlikely to have a listener so connection fails
	result, err := CompareRead(context.Background(), CompareReadRequest{
		Address:    "127.0.0.1",
		Port:       35555,
		Candidates: []RackSlot{{Rack: 0, Slot: 1}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     0,
		Size:       8,
		Timeout:    100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ByCandidate) != 1 {
		t.Fatalf("expected 1 candidate result, got %d", len(result.ByCandidate))
	}
	// Connection fails (no listener), so we get TransportErr
	if result.ByCandidate[0].Result.Status != ReadStatusTransportErr {
		t.Errorf("expected transport error for unreachable, got %q", result.ByCandidate[0].Result.Status)
	}
	if result.RackSlotInsensitive {
		t.Error("RackSlotInsensitive should be false with single candidate or failed read")
	}
}

func TestRackSlot_ZeroValue(t *testing.T) {
	var r RackSlot
	if r.Rack != 0 || r.Slot != 0 {
		t.Errorf("zero value RackSlot: Rack=%d Slot=%d", r.Rack, r.Slot)
	}
}

// TestCompareRead_TwoCandidatesWithFakeServer runs CompareRead against a server that accepts two connections.
func TestCompareRead_TwoCandidatesWithFakeServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	handleConn := func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		tr := transport.New(conn, 2*time.Second)
		payload, _ := tr.Receive()
		dec, _ := cotp.Decode(payload)
		if dec.CR != nil {
			cc := &cotp.CC{CDT: 0, DestinationRef: 0, SourceRef: 0, ClassOption: 0,
				CallingSelector: dec.CR.CallingSelector, CalledSelector: dec.CR.CalledSelector, TPDUSize: dec.CR.TPDUSize}
			ccBytes, _ := cc.MarshalBinary()
			_ = tr.Send(ccBytes)
		}
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 18 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			resp := make([]byte, 20)
			resp[0] = 0x32
			resp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(resp[4:6], pduRef)
			binary.BigEndian.PutUint16(resp[6:8], 8)
			resp[12] = wire.FuncSetupComm
			binary.BigEndian.PutUint16(resp[14:16], 2)
			binary.BigEndian.PutUint16(resp[16:18], 2)
			binary.BigEndian.PutUint16(resp[18:20], 480)
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			readResp := make([]byte, 20)
			readResp[0] = 0x32
			readResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(readResp[4:6], pduRef)
			binary.BigEndian.PutUint16(readResp[6:8], 2)
			binary.BigEndian.PutUint16(readResp[8:10], 6)
			readResp[12] = wire.FuncReadVar
			readResp[13] = 1
			readResp[14] = wire.RetCodeSuccess
			readResp[15] = 0x04
			binary.BigEndian.PutUint16(readResp[16:18], 16)
			readResp[18] = 0xAB
			readResp[19] = 0xCD
			dtBytes, _ := wire.EncodeCOTPDT(readResp)
			_ = tr.Send(dtBytes)
		}
	}

	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			handleConn(conn)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := CompareRead(ctx, CompareReadRequest{
		Address:    "127.0.0.1",
		Port:       port,
		Candidates: []RackSlot{{Rack: 0, Slot: 1}, {Rack: 0, Slot: 1}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     0,
		Size:       2,
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("CompareRead: %v", err)
	}
	if len(result.ByCandidate) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.ByCandidate))
	}
	if !result.ByCandidate[0].Result.OK() || !result.ByCandidate[1].Result.OK() {
		t.Errorf("both reads should succeed: %s, %s", result.ByCandidate[0].Result.Status, result.ByCandidate[1].Result.Status)
	}
	if !result.RackSlotInsensitive {
		t.Error("expected RackSlotInsensitive true when both return same data")
	}
}

// TestCompareRead_DifferentData verifies RackSlotInsensitive is false when data differs.
func TestCompareRead_DifferentData(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	firstData := []byte{0xAB, 0xCD}
	secondData := []byte{0x11, 0x22}
	var connIndex int32

	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			idx := atomic.AddInt32(&connIndex, 1) - 1
			data := firstData
			if idx == 1 {
				data = secondData
			}
			func(conn net.Conn, data []byte) {
				defer func() { _ = conn.Close() }()
				tr := transport.New(conn, 2*time.Second)
				payload, _ := tr.Receive()
				dec, _ := cotp.Decode(payload)
				if dec.CR != nil {
					cc := &cotp.CC{CDT: 0, DestinationRef: 0, SourceRef: 0, ClassOption: 0,
						CallingSelector: dec.CR.CallingSelector, CalledSelector: dec.CR.CalledSelector, TPDUSize: dec.CR.TPDUSize}
					ccBytes, _ := cc.MarshalBinary()
					_ = tr.Send(ccBytes)
				}
				payload, _ = tr.Receive()
				dec, _ = cotp.Decode(payload)
				if dec.DT != nil && len(dec.DT.UserData) >= 18 {
					s7 := dec.DT.UserData
					pduRef := binary.BigEndian.Uint16(s7[4:6])
					resp := make([]byte, 20)
					resp[0] = 0x32
					resp[1] = byte(wire.ROSCTRAckData)
					binary.BigEndian.PutUint16(resp[4:6], pduRef)
					binary.BigEndian.PutUint16(resp[6:8], 8)
					resp[12] = wire.FuncSetupComm
					binary.BigEndian.PutUint16(resp[14:16], 2)
					binary.BigEndian.PutUint16(resp[16:18], 2)
					binary.BigEndian.PutUint16(resp[18:20], 480)
					dtBytes, _ := wire.EncodeCOTPDT(resp)
					_ = tr.Send(dtBytes)
				}
				payload, _ = tr.Receive()
				dec, _ = cotp.Decode(payload)
				if dec.DT != nil && len(dec.DT.UserData) >= 12 {
					s7 := dec.DT.UserData
					pduRef := binary.BigEndian.Uint16(s7[4:6])
					readResp := make([]byte, 20)
					readResp[0] = 0x32
					readResp[1] = byte(wire.ROSCTRAckData)
					binary.BigEndian.PutUint16(readResp[4:6], pduRef)
					binary.BigEndian.PutUint16(readResp[6:8], 2)
					binary.BigEndian.PutUint16(readResp[8:10], 6)
					readResp[12] = wire.FuncReadVar
					readResp[13] = 1
					readResp[14] = wire.RetCodeSuccess
					readResp[15] = 0x04
					binary.BigEndian.PutUint16(readResp[16:18], 16)
					readResp[18] = data[0]
					readResp[19] = data[1]
					dtBytes, _ := wire.EncodeCOTPDT(readResp)
					_ = tr.Send(dtBytes)
				}
			}(conn, data)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := CompareRead(ctx, CompareReadRequest{
		Address:    "127.0.0.1",
		Port:       port,
		Candidates: []RackSlot{{Rack: 0, Slot: 1}, {Rack: 0, Slot: 2}},
		Area:       model.AreaDB,
		DBNumber:   1,
		Offset:     0,
		Size:       2,
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("CompareRead: %v", err)
	}
	if len(result.ByCandidate) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.ByCandidate))
	}
	if result.RackSlotInsensitive {
		t.Error("expected RackSlotInsensitive false when data differs")
	}
}

func BenchmarkCompareReadManyCandidates(b *testing.B) {
	ctx := context.Background()
	candidates := make([]RackSlot, 20)
	for i := range candidates {
		candidates[i] = RackSlot{Rack: 0, Slot: i % 4}
	}
	req := CompareReadRequest{
		Address:     "127.0.0.1",
		Port:        7,
		Candidates:  candidates,
		Area:        model.AreaDB,
		DBNumber:    1,
		Offset:      0,
		Size:        8,
		Timeout:     5 * time.Millisecond,
		Parallelism: 4,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = CompareRead(ctx, req)
	}
}
