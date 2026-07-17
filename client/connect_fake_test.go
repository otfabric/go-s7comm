package client

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// TestConnectWithFakeServer runs a minimal fake PLC that responds with COTP CC and S7 setup.
// It exercises connectOnce, cotpConnect, s7Setup, ConnectionInfo, PDUSize, nextPDURef.
func TestConnectWithFakeServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()

		// 3. S7 read var -> read response (for ReadDB test)
		tsdu, err := srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			_ = srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0xAB, 0xCD}))
		}
		// 4. Second read (e.g. ReadInputs)
		tsdu, err = srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			_ = srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0x12, 0x34}))
		}
		// 5. Write -> write ack
		tsdu, err = srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			writeResp := make([]byte, 12+2+1)
			writeResp[0] = 0x32
			writeResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(writeResp[4:6], pduRef)
			binary.BigEndian.PutUint16(writeResp[6:8], 2)
			writeResp[8] = 0
			writeResp[9] = 1
			writeResp[12] = wire.FuncWriteVar
			writeResp[13] = 1
			writeResp[14] = wire.RetCodeSuccess
			_ = srv.WriteTSDU(ctx, writeResp)
		}
		// 6 & 7. SZL requests (Identify sends two: ModuleID and ComponentID)
		for i := 0; i < 2; i++ {
			tsdu, err = srv.ReadTSDU(ctx)
			if err != nil {
				return
			}
			if len(tsdu) >= 12 {
				pduRef := binary.BigEndian.Uint16(tsdu[4:6])
				payload := make([]byte, 38)
				copy(payload, []byte("6ES7 315-2AG10-0AB0          "))
				_ = srv.WriteTSDU(ctx, buildSZLResponse(pduRef, wire.SZLModuleID, 38, payload))
			}
		}
		// 8 & 8b. ProbeReadableRanges single offset with Repeat=2 (two read responses)
		for i := 0; i < 2; i++ {
			tsdu, err = srv.ReadTSDU(ctx)
			if err != nil {
				return
			}
			if len(tsdu) >= 12 {
				pduRef := binary.BigEndian.Uint16(tsdu[4:6])
				_ = srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0xDE, 0xAD}))
			}
		}
		// 9 & 10. ReadOutputs and ReadMerkers (two more read responses)
		for i := 0; i < 2; i++ {
			tsdu, err = srv.ReadTSDU(ctx)
			if err != nil {
				return
			}
			if len(tsdu) >= 12 {
				pduRef := binary.BigEndian.Uint16(tsdu[4:6])
				_ = srv.WriteTSDU(ctx, buildReadVarResponse(pduRef, 16, []byte{0, 0}))
			}
		}
		// 11. GetCPUState SZL (0x0424) - return state 0x08 = Run
		tsdu, err = srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			payload := make([]byte, 8)
			payload[2] = 0x08 // Run at resp.Data[2]
			_ = srv.WriteTSDU(ctx, buildSZLResponse(pduRef, wire.SZLCPUState, 8, payload))
		}
		// 12. GetProtectionLevel SZL (0x0232)
		tsdu, err = srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			payload := make([]byte, 8)
			payload[2] = 0 // No protection
			_ = srv.WriteTSDU(ctx, buildSZLResponse(pduRef, wire.SZLProtectionInfo, 8, payload))
		}
		// 13. ReadDiagBuffer SZL (0x00A0)
		tsdu, err = srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			payload := make([]byte, 24)
			payload[0] = 0
			payload[1] = 1
			payload[2] = 0x10
			payload[3] = 0x20
			_ = srv.WriteTSDU(ctx, buildSZLResponse(pduRef, wire.SZLDiagBuffer, 24, payload))
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second), WithRateLimit(1*time.Millisecond))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	connInfo := c.ConnectionInfo()
	if connInfo.Host != "127.0.0.1" || connInfo.Port != port || connInfo.Rack != 0 || connInfo.Slot != 1 {
		t.Errorf("ConnectionInfo: got %+v", connInfo)
	}
	if c.PDUSize() != 480 {
		t.Errorf("PDUSize: got %d, want 480", c.PDUSize())
	}

	// ReadDB exercises sendReceive and ReadArea path
	result, err := c.ReadDB(ctx, 1, 0, 2)
	if err != nil {
		t.Fatalf("ReadDB: %v", err)
	}
	if !result.OK() {
		t.Fatalf("ReadDB result not OK: %s", result.Status)
	}
	if len(result.Data) != 2 || result.Data[0] != 0xAB || result.Data[1] != 0xCD {
		t.Fatalf("ReadDB data: got %v", result.Data)
	}

	// Second read: ReadInputs (server handles 4th exchange)
	result2, err := c.ReadInputs(ctx, 0, 2)
	if err != nil {
		t.Fatalf("ReadInputs: %v", err)
	}
	if !result2.OK() {
		t.Fatalf("ReadInputs result not OK: %s", result2.Status)
	}

	// WriteDB exercises WriteArea and sendReceive for write
	if err := c.WriteDB(ctx, 1, 0, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("WriteDB: %v", err)
	}

	// Identify exercises SZL path (two SZL requests)
	devInfo, err := c.Identify(ctx)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if devInfo == nil {
		t.Fatal("Identify returned nil")
	}
	if devInfo.OrderNumber == "" {
		t.Log("Identify OrderNumber empty (server sent minimal SZL)")
	}

	// ProbeReadableRanges with one offset and Repeat=2 exercises probeOneOffset and byteSlicesEqual
	scanResult, err := c.ProbeReadableRanges(ctx, RangeProbeRequest{
		Area:      model.AreaDB,
		DBNumber:  1,
		Start:     0,
		End:       4,
		Step:      4,
		ProbeSize: 2,
		Repeat:    2,
	})
	if err != nil {
		t.Fatalf("ProbeReadableRanges: %v", err)
	}
	if len(scanResult.Probes) != 1 {
		t.Fatalf("expected 1 probe, got %d", len(scanResult.Probes))
	}
	if !scanResult.Probes[0].Result.OK() {
		t.Fatalf("probe result not OK: %s", scanResult.Probes[0].Result.Status)
	}

	// ReadOutputs and ReadMerkers (two more read responses from server)
	result3, err := c.ReadOutputs(ctx, 0, 2)
	if err != nil {
		t.Fatalf("ReadOutputs: %v", err)
	}
	if !result3.OK() {
		t.Fatalf("ReadOutputs result not OK: %s", result3.Status)
	}
	result4, err := c.ReadMerkers(ctx, 0, 2)
	if err != nil {
		t.Fatalf("ReadMerkers: %v", err)
	}
	if !result4.OK() {
		t.Fatalf("ReadMerkers result not OK: %s", result4.Status)
	}

	// GetCPUState and GetProtectionLevel exercise more SZL paths
	state, err := c.GetCPUState(ctx)
	if err != nil {
		t.Fatalf("GetCPUState: %v", err)
	}
	if state != model.CPUStateRun {
		t.Errorf("GetCPUState: got %v, want Run", state)
	}
	level, err := c.GetProtectionLevel(ctx)
	if err != nil {
		t.Fatalf("GetProtectionLevel: %v", err)
	}
	if level != model.ProtectionNone {
		t.Errorf("GetProtectionLevel: got %v, want None", level)
	}

	// ReadDiagBuffer exercises SZL diag path
	diagBuf, err := c.ReadDiagBuffer(ctx)
	if err != nil {
		t.Fatalf("ReadDiagBuffer: %v", err)
	}
	if diagBuf == nil {
		t.Fatal("ReadDiagBuffer returned nil")
	}
	if len(diagBuf.Entries) < 1 {
		t.Logf("ReadDiagBuffer entries: %d", len(diagBuf.Entries))
	}
}

// TestReadAreaEmptyResponse verifies the client classifies an empty read response correctly.
func TestReadAreaEmptyResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		tsdu, err := srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			emptyResp := make([]byte, 12+2+4)
			emptyResp[0] = 0x32
			emptyResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(emptyResp[4:6], pduRef)
			binary.BigEndian.PutUint16(emptyResp[6:8], 2)
			binary.BigEndian.PutUint16(emptyResp[8:10], 4)
			emptyResp[12] = wire.FuncReadVar
			emptyResp[13] = 1
			emptyResp[14] = wire.RetCodeSuccess
			emptyResp[15] = 0x04
			binary.BigEndian.PutUint16(emptyResp[16:18], 0) // 0 bits
			_ = srv.WriteTSDU(ctx, emptyResp)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	result, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 4})
	if err != nil {
		t.Fatalf("ReadArea: %v", err)
	}
	if result.Status != ReadStatusEmptyRead {
		t.Errorf("expected EmptyRead status, got %s", result.Status)
	}
	if result.OK() {
		t.Error("result.OK() should be false for empty read")
	}
}

// TestReadAreaRejectedResponse verifies the client classifies a rejected (return code) response.
func TestReadAreaRejectedResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		tsdu, err := srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			rejResp := make([]byte, 12+2+4)
			rejResp[0] = 0x32
			rejResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(rejResp[4:6], pduRef)
			binary.BigEndian.PutUint16(rejResp[6:8], 2)
			binary.BigEndian.PutUint16(rejResp[8:10], 4)
			rejResp[12] = wire.FuncReadVar
			rejResp[13] = 1
			rejResp[14] = wire.RetCodeAccessFault
			rejResp[15] = 0x04
			binary.BigEndian.PutUint16(rejResp[16:18], 0)
			_ = srv.WriteTSDU(ctx, rejResp)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	result, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 4})
	if err != nil {
		t.Fatalf("ReadArea: %v", err)
	}
	if result.Status != ReadStatusRejected {
		t.Errorf("expected Rejected status, got %s", result.Status)
	}
	if result.OK() {
		t.Error("result.OK() should be false for rejected read")
	}
	if result.ItemStatus != "access denied" {
		t.Errorf("expected ItemStatus 'access denied', got %q", result.ItemStatus)
	}
}

// TestReadAreaShortRead verifies the client classifies a short read correctly.
func TestReadAreaShortRead(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		tsdu, err := srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			shortResp := make([]byte, 12+2+8)
			shortResp[0] = 0x32
			shortResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(shortResp[4:6], pduRef)
			binary.BigEndian.PutUint16(shortResp[6:8], 2)
			binary.BigEndian.PutUint16(shortResp[8:10], 8)
			shortResp[12] = wire.FuncReadVar
			shortResp[13] = 1
			shortResp[14] = wire.RetCodeSuccess
			shortResp[15] = 0x04
			binary.BigEndian.PutUint16(shortResp[16:18], 16) // 2 bytes
			shortResp[18] = 0xAA
			shortResp[19] = 0xBB
			_ = srv.WriteTSDU(ctx, shortResp)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	result, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 4})
	if err != nil {
		t.Fatalf("ReadArea: %v", err)
	}
	if result.Status != ReadStatusShortRead {
		t.Errorf("expected ShortRead status, got %s", result.Status)
	}
	if result.RequestedLength != 4 || result.ReturnedLength != 2 {
		t.Errorf("requested=%d returned=%d", result.RequestedLength, result.ReturnedLength)
	}
}

// TestReadAreaProtocolError verifies the client classifies a malformed response as protocol error.
func TestReadAreaProtocolError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		tsdu, err := srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			badResp := make([]byte, 12+2+4)
			badResp[0] = 0x32
			badResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(badResp[4:6], pduRef)
			binary.BigEndian.PutUint16(badResp[6:8], 2)
			binary.BigEndian.PutUint16(badResp[8:10], 4)
			badResp[12] = wire.FuncWriteVar // wrong function
			badResp[13] = 1
			badResp[14] = wire.RetCodeSuccess
			_ = srv.WriteTSDU(ctx, badResp)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	result, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 4})
	if err != nil {
		t.Fatalf("ReadArea: %v", err)
	}
	if result.Status != ReadStatusProtocolErr {
		t.Errorf("expected ProtocolErr status, got %s", result.Status)
	}
}

// TestReadAreaZeroItems verifies the client handles a read response with zero items (no data returned).
func TestReadAreaZeroItems(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		srv := acceptFakeCOTP(t, conn)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		ctx := context.Background()
		tsdu, err := srv.ReadTSDU(ctx)
		if err != nil {
			return
		}
		if len(tsdu) >= 12 {
			pduRef := binary.BigEndian.Uint16(tsdu[4:6])
			zeroResp := make([]byte, 12+2+0)
			zeroResp[0] = 0x32
			zeroResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(zeroResp[4:6], pduRef)
			binary.BigEndian.PutUint16(zeroResp[6:8], 2)
			binary.BigEndian.PutUint16(zeroResp[8:10], 0)
			zeroResp[12] = wire.FuncReadVar
			zeroResp[13] = 0
			_ = srv.WriteTSDU(ctx, zeroResp)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithTimeout(2*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	result, err := c.ReadArea(ctx, model.Address{Area: model.AreaDB, DBNumber: 1, Start: 0, Size: 4})
	if err != nil {
		t.Fatalf("ReadArea: %v", err)
	}
	if result.Status != ReadStatusEmptyRead {
		t.Errorf("expected EmptyRead status for zero items, got %s", result.Status)
	}
}

// TestConnectionInfoAndPDUSizeWithoutConnect verifies zero values when not connected.
func TestConnectionInfoAndPDUSizeWithoutConnect(t *testing.T) {
	c := New("host", WithPort(102), WithRackSlot(0, 1))
	info := c.ConnectionInfo()
	if info.Host != "host" || info.Port != 102 {
		t.Errorf("ConnectionInfo (not connected): got %+v", info)
	}
	if c.PDUSize() != 0 {
		t.Errorf("PDUSize (not connected): got %d", c.PDUSize())
	}
}
