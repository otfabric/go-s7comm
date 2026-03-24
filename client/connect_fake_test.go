package client

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/transport"
	"github.com/otfabric/go-s7comm/wire"
)

// TestConnectWithFakeServer runs a minimal fake PLC that responds with COTP CC and S7 setup.
// It exercises connectOnce, cotpConnect, s7Setup, ConnectionInfo, PDUSize, nextPDURef.
func TestConnectWithFakeServer(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	// Fake server: respond to COTP CR with CC, then to S7 setup with setup response
	go func() {
		conn := transport.New(serverConn, 2*time.Second)
		// 1. Read COTP CR
		payload, err := conn.Receive()
		if err != nil {
			return
		}
		dec, err := cotp.Decode(payload)
		if err != nil || dec.Type != cotp.TypeCR {
			return
		}
		// Send CC (same TSAPs, TPDU size)
		cc := &cotp.CC{
			CDT: 0, DestinationRef: 0, SourceRef: 0, ClassOption: 0,
			CallingSelector: dec.CR.CallingSelector,
			CalledSelector:  dec.CR.CalledSelector,
			TPDUSize:        dec.CR.TPDUSize,
		}
		ccBytes, _ := cc.MarshalBinary()
		_ = conn.Send(ccBytes)

		// 2. Read COTP DT (S7 setup request)
		payload, err = conn.Receive()
		if err != nil {
			return
		}
		dec, err = cotp.Decode(payload)
		if err != nil || dec.DT == nil {
			return
		}
		s7Data := dec.DT.UserData
		if len(s7Data) < 10+8 || s7Data[0] != 0x32 {
			return
		}
		pduRef := binary.BigEndian.Uint16(s7Data[4:6])

		// Build S7 setup response: 12-byte ack header + 8-byte setup param
		resp := make([]byte, 20)
		resp[0] = 0x32
		resp[1] = byte(wire.ROSCTRAckData)
		binary.BigEndian.PutUint16(resp[4:6], pduRef)
		binary.BigEndian.PutUint16(resp[6:8], 8)
		binary.BigEndian.PutUint16(resp[8:10], 0)
		resp[10] = 0
		resp[11] = 0
		resp[12] = wire.FuncSetupComm
		resp[13] = 0
		binary.BigEndian.PutUint16(resp[14:16], 2)
		binary.BigEndian.PutUint16(resp[16:18], 2)
		binary.BigEndian.PutUint16(resp[18:20], 480)

		dtBytes, _ := wire.EncodeCOTPDT(resp)
		_ = conn.Send(dtBytes)
	}()

	// Client side: we need to inject the pipe into the client instead of TCP dial.
	// We can't do that without changing the client. So use a different approach:
	// start a real TCP listener that accepts one connection and runs the fake server logic.
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
		tr := transport.New(conn, 2*time.Second)
		// COTP CR -> CC
		payload, _ := tr.Receive()
		dec, _ := cotp.Decode(payload)
		if dec.CR != nil {
			cc := &cotp.CC{CDT: 0, DestinationRef: 0, SourceRef: 0, ClassOption: 0,
				CallingSelector: dec.CR.CallingSelector, CalledSelector: dec.CR.CalledSelector, TPDUSize: dec.CR.TPDUSize}
			ccBytes, _ := cc.MarshalBinary()
			_ = tr.Send(ccBytes)
		}
		// S7 setup -> setup response
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
		// 3. S7 read var -> read response (for ReadDB test)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			// Build read response: 12-byte ack header + param (2) + data (6)
			const paramLen, dataLen = 2, 6
			resp := make([]byte, 12+paramLen+dataLen)
			resp[0] = 0x32
			resp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(resp[4:6], pduRef)
			binary.BigEndian.PutUint16(resp[6:8], paramLen)
			binary.BigEndian.PutUint16(resp[8:10], dataLen)
			resp[10] = 0
			resp[11] = 0
			resp[12] = wire.FuncReadVar
			resp[13] = 1
			resp[14] = wire.RetCodeSuccess
			resp[15] = 0x04                             // byte
			binary.BigEndian.PutUint16(resp[16:18], 16) // 2 bytes = 16 bits
			resp[18] = 0xAB
			resp[19] = 0xCD
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		// 4. Second read (e.g. ReadInputs)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			const paramLen, dataLen = 2, 6
			resp := make([]byte, 12+paramLen+dataLen)
			resp[0] = 0x32
			resp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(resp[4:6], pduRef)
			binary.BigEndian.PutUint16(resp[6:8], paramLen)
			binary.BigEndian.PutUint16(resp[8:10], dataLen)
			resp[12] = wire.FuncReadVar
			resp[13] = 1
			resp[14] = wire.RetCodeSuccess
			resp[15] = 0x04
			binary.BigEndian.PutUint16(resp[16:18], 16)
			resp[18] = 0x12
			resp[19] = 0x34
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		// 5. Write -> write ack
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 && dec.DT.UserData[10] == wire.FuncWriteVar {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			writeResp := make([]byte, 12+2+1)
			writeResp[0] = 0x32
			writeResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(writeResp[4:6], pduRef)
			binary.BigEndian.PutUint16(writeResp[6:8], 2)
			writeResp[8] = 0
			writeResp[9] = 1
			writeResp[10] = 0
			writeResp[11] = 0
			writeResp[12] = wire.FuncWriteVar
			writeResp[13] = 1
			writeResp[14] = wire.RetCodeSuccess
			dtBytes, _ := wire.EncodeCOTPDT(writeResp)
			_ = tr.Send(dtBytes)
		}
		// 6 & 7. SZL requests (Identify sends two: ModuleID and ComponentID)
		for i := 0; i < 2; i++ {
			payload, _ = tr.Receive()
			dec, _ = cotp.Decode(payload)
			if dec.DT != nil && len(dec.DT.UserData) >= 12 {
				s7 := dec.DT.UserData
				pduRef := binary.BigEndian.Uint16(s7[4:6])
				// SZL response: 12-byte header + 2 param + data (retCode, 0x09, dataLen, szlID, szlIndex, data)
				// ParseSZLResponse bounds resp.Data to data[8:4+dataLen]; Identify needs >=20 (module) and >=34 (component)
				const szlParamLen = 2
				const szlPayloadLen = 38 // so resp.Data has 38 bytes (enough for component)
				szlDataLen := 8 + szlPayloadLen
				szlResp := make([]byte, 12+szlParamLen+szlDataLen)
				szlResp[0] = 0x32
				szlResp[1] = byte(wire.ROSCTRAckData)
				binary.BigEndian.PutUint16(szlResp[4:6], pduRef)
				binary.BigEndian.PutUint16(szlResp[6:8], szlParamLen)
				binary.BigEndian.PutUint16(szlResp[8:10], uint16(szlDataLen))
				szlResp[10] = 0
				szlResp[11] = 0
				szlResp[14] = wire.RetCodeSuccess
				szlResp[15] = 0x09
				binary.BigEndian.PutUint16(szlResp[16:18], szlPayloadLen)
				binary.BigEndian.PutUint16(szlResp[18:20], wire.SZLModuleID)
				copy(szlResp[22:44], []byte("6ES7 315-2AG10-0AB0          ")) // OrderNumber at resp.Data[2:22]
				dtBytes, _ := wire.EncodeCOTPDT(szlResp)
				_ = tr.Send(dtBytes)
			}
		}
		// 8 & 8b. ProbeReadableRanges single offset with Repeat=2 (two read responses)
		for i := 0; i < 2; i++ {
			payload, _ = tr.Receive()
			dec, _ = cotp.Decode(payload)
			if dec.DT != nil && len(dec.DT.UserData) >= 12 {
				s7 := dec.DT.UserData
				pduRef := binary.BigEndian.Uint16(s7[4:6])
				const paramLen, dataLen = 2, 6
				resp := make([]byte, 12+paramLen+dataLen)
				resp[0] = 0x32
				resp[1] = byte(wire.ROSCTRAckData)
				binary.BigEndian.PutUint16(resp[4:6], pduRef)
				binary.BigEndian.PutUint16(resp[6:8], paramLen)
				binary.BigEndian.PutUint16(resp[8:10], dataLen)
				resp[12] = wire.FuncReadVar
				resp[13] = 1
				resp[14] = wire.RetCodeSuccess
				resp[15] = 0x04
				binary.BigEndian.PutUint16(resp[16:18], 16)
				resp[18] = 0xDE
				resp[19] = 0xAD
				dtBytes, _ := wire.EncodeCOTPDT(resp)
				_ = tr.Send(dtBytes)
			}
		}
		// 9 & 10. ReadOutputs and ReadMerkers (two more read responses)
		for i := 0; i < 2; i++ {
			payload, _ = tr.Receive()
			dec, _ = cotp.Decode(payload)
			if dec.DT != nil && len(dec.DT.UserData) >= 12 {
				s7 := dec.DT.UserData
				pduRef := binary.BigEndian.Uint16(s7[4:6])
				resp := make([]byte, 20)
				resp[0] = 0x32
				resp[1] = byte(wire.ROSCTRAckData)
				binary.BigEndian.PutUint16(resp[4:6], pduRef)
				binary.BigEndian.PutUint16(resp[6:8], 2)
				binary.BigEndian.PutUint16(resp[8:10], 6)
				resp[12] = wire.FuncReadVar
				resp[13] = 1
				resp[14] = wire.RetCodeSuccess
				resp[15] = 0x04
				binary.BigEndian.PutUint16(resp[16:18], 16)
				resp[18] = 0
				resp[19] = 0
				dtBytes, _ := wire.EncodeCOTPDT(resp)
				_ = tr.Send(dtBytes)
			}
		}
		// 11. GetCPUState SZL (0x0424) - return state 0x08 = Run
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			szlResp := make([]byte, 12+2+12)
			szlResp[0] = 0x32
			szlResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(szlResp[4:6], pduRef)
			binary.BigEndian.PutUint16(szlResp[6:8], 2)
			binary.BigEndian.PutUint16(szlResp[8:10], 12)
			szlResp[14] = wire.RetCodeSuccess
			szlResp[15] = 0x09
			binary.BigEndian.PutUint16(szlResp[16:18], 8)
			binary.BigEndian.PutUint16(szlResp[18:20], wire.SZLCPUState)
			// resp.Data = data[8:], so resp.Data[2] = state byte
			szlResp[22+2] = 0x08 // Run
			dtBytes, _ := wire.EncodeCOTPDT(szlResp)
			_ = tr.Send(dtBytes)
		}
		// 12. GetProtectionLevel SZL (0x0232)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			szlResp := make([]byte, 12+2+12)
			szlResp[0] = 0x32
			szlResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(szlResp[4:6], pduRef)
			binary.BigEndian.PutUint16(szlResp[6:8], 2)
			binary.BigEndian.PutUint16(szlResp[8:10], 12)
			szlResp[14] = wire.RetCodeSuccess
			szlResp[15] = 0x09
			binary.BigEndian.PutUint16(szlResp[16:18], 8)
			binary.BigEndian.PutUint16(szlResp[18:20], wire.SZLProtectionInfo)
			szlResp[22+2] = 0 // No protection (resp.Data[2])
			dtBytes, _ := wire.EncodeCOTPDT(szlResp)
			_ = tr.Send(dtBytes)
		}
		// 13. ReadDiagBuffer SZL (0x00A0)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			diagResp := make([]byte, 12+2+28) // 4 + 24 bytes SZL data (one 20-byte entry + padding)
			diagResp[0] = 0x32
			diagResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(diagResp[4:6], pduRef)
			binary.BigEndian.PutUint16(diagResp[6:8], 2)
			binary.BigEndian.PutUint16(diagResp[8:10], 28)
			diagResp[14] = wire.RetCodeSuccess
			diagResp[15] = 0x09
			binary.BigEndian.PutUint16(diagResp[16:18], 24)
			binary.BigEndian.PutUint16(diagResp[18:20], wire.SZLDiagBuffer)
			// One 20-byte entry: EventID, EventClass, Priority at offset 0,1,2,3
			diagResp[22] = 0
			diagResp[23] = 1
			diagResp[24] = 0x10
			diagResp[25] = 0x20
			dtBytes, _ := wire.EncodeCOTPDT(diagResp)
			_ = tr.Send(dtBytes)
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
		conn, _ := ln.Accept()
		if conn == nil {
			return
		}
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
		// Read request -> respond with success but 0 bytes data (empty read)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			// Param 2 bytes, data: retCode, 0x04, length=0 (0 bytes) -> 4 bytes total
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
			dtBytes, _ := wire.EncodeCOTPDT(emptyResp)
			_ = tr.Send(dtBytes)
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
		conn, _ := ln.Accept()
		if conn == nil {
			return
		}
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
		// Read request -> respond with access fault (rejected)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
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
			dtBytes, _ := wire.EncodeCOTPDT(rejResp)
			_ = tr.Send(dtBytes)
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
		conn, _ := ln.Accept()
		if conn == nil {
			return
		}
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
		// Read request (4 bytes) -> respond with only 2 bytes (short read)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			shortResp := make([]byte, 12+2+8) // 8 = 4 header + 4 (2 bytes = 16 bits)
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
			dtBytes, _ := wire.EncodeCOTPDT(shortResp)
			_ = tr.Send(dtBytes)
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
		conn, _ := ln.Accept()
		if conn == nil {
			return
		}
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
		// Read request -> respond with wrong function code (not FuncReadVar) so ParseReadVarResponse fails
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			badResp := make([]byte, 12+2+4)
			badResp[0] = 0x32
			badResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(badResp[4:6], pduRef)
			binary.BigEndian.PutUint16(badResp[6:8], 2)
			binary.BigEndian.PutUint16(badResp[8:10], 4)
			badResp[12] = wire.FuncWriteVar // wrong function
			badResp[13] = 1
			badResp[14] = wire.RetCodeSuccess
			dtBytes, _ := wire.EncodeCOTPDT(badResp)
			_ = tr.Send(dtBytes)
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
		conn, _ := ln.Accept()
		if conn == nil {
			return
		}
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
		// Read request -> respond with item count 0 (param: FuncReadVar, 0; data: empty)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 12 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			zeroResp := make([]byte, 12+2+0)
			zeroResp[0] = 0x32
			zeroResp[1] = byte(wire.ROSCTRAckData)
			binary.BigEndian.PutUint16(zeroResp[4:6], pduRef)
			binary.BigEndian.PutUint16(zeroResp[6:8], 2)
			binary.BigEndian.PutUint16(zeroResp[8:10], 0)
			zeroResp[12] = wire.FuncReadVar
			zeroResp[13] = 0
			dtBytes, _ := wire.EncodeCOTPDT(zeroResp)
			_ = tr.Send(dtBytes)
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
