package client

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/wire"
)

// acceptFakeCOTP completes a TP0 Accept on raw (test peer acting as PLC).
func acceptFakeCOTP(t *testing.T, raw net.Conn) *cotp.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := cotp.Accept(ctx, raw, cotp.ServerConfig{MaxTPDULength: 1024})
	if err != nil {
		t.Fatalf("cotp.Accept: %v", err)
	}
	return c
}

// serveFakeSetup reads one S7 setup TSDU and writes a setup response with pduSize.
func serveFakeSetup(t *testing.T, srv *cotp.Conn, pduSize int) {
	t.Helper()
	ctx := context.Background()
	s7Data, err := srv.ReadTSDU(ctx)
	if err != nil {
		t.Fatalf("ReadTSDU setup: %v", err)
	}
	if len(s7Data) < 10 || s7Data[0] != 0x32 {
		t.Fatalf("expected S7 setup request, got %d bytes", len(s7Data))
	}
	pduRef := binary.BigEndian.Uint16(s7Data[4:6])
	if err := srv.WriteTSDU(ctx, buildS7SetupResponse(pduRef, pduSize)); err != nil {
		t.Fatalf("WriteTSDU setup: %v", err)
	}
}

// buildS7SetupResponse returns an S7 setup response payload (S7 header + param + data).
// Header declares param 8, data 2; total 12+8+2 = 22 bytes so client gets expected length.
func buildS7SetupResponse(pduRef uint16, pduSize int) []byte {
	resp := make([]byte, 22)
	resp[0] = 0x32
	resp[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(resp[4:6], pduRef)
	binary.BigEndian.PutUint16(resp[6:8], 8)
	binary.BigEndian.PutUint16(resp[8:10], 2)
	resp[12] = wire.FuncSetupComm
	binary.BigEndian.PutUint16(resp[14:16], 2)
	binary.BigEndian.PutUint16(resp[16:18], 2)
	binary.BigEndian.PutUint16(resp[18:20], uint16(pduSize)) // param[6:8] for ParseSetupCommResponse
	// resp[20:22] is data section (2 bytes); unused by parser
	return resp
}

// buildReadVarResponse builds an S7 Read Var response (param 2 bytes, data 6 bytes for one item).
// itemDataLenBits is the item length in bits (e.g. 16 for 2 bytes); dataBytes are the payload.
func buildReadVarResponse(pduRef uint16, itemDataLenBits uint16, dataBytes []byte) []byte {
	if len(dataBytes) < 2 {
		dataBytes = append(dataBytes, 0xAB, 0xCD)
	}
	if itemDataLenBits == 0 {
		itemDataLenBits = 16
	}
	paramLen, dataLen := 2, 6
	resp := make([]byte, 12+paramLen+dataLen)
	resp[0] = 0x32
	resp[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(resp[4:6], pduRef)
	binary.BigEndian.PutUint16(resp[6:8], uint16(paramLen))
	binary.BigEndian.PutUint16(resp[8:10], uint16(dataLen))
	resp[12] = wire.FuncReadVar
	resp[13] = 1
	resp[14] = wire.RetCodeSuccess
	resp[15] = 0x04
	binary.BigEndian.PutUint16(resp[16:18], itemDataLenBits)
	copy(resp[18:], dataBytes)
	return resp
}

// buildSZLResponse builds the S7 data section for an SZL response (retCode, 0x09, dataLen, szlID, szlIndex, payload).
// payloadLen is the declared SZL payload length; payload is copied into the response (at least payloadLen bytes used).
func buildSZLResponse(pduRef, szlID uint16, payloadLen int, payload []byte) []byte {
	dataSectionLen := 8 + payloadLen
	resp := make([]byte, 12+2+dataSectionLen)
	resp[0] = 0x32
	resp[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(resp[4:6], pduRef)
	binary.BigEndian.PutUint16(resp[6:8], 2)
	binary.BigEndian.PutUint16(resp[8:10], uint16(dataSectionLen))
	resp[14] = wire.RetCodeSuccess
	resp[15] = 0x09
	binary.BigEndian.PutUint16(resp[16:18], uint16(payloadLen))
	binary.BigEndian.PutUint16(resp[18:20], szlID)
	binary.BigEndian.PutUint16(resp[20:22], 0)
	if len(payload) >= payloadLen {
		copy(resp[22:], payload[:payloadLen])
	}
	return resp
}

// buildStartUploadResponse returns S7 Start Upload response (param with session ID; no data).
func buildStartUploadResponse(pduRef uint16, sessionID string) []byte {
	id := []byte(sessionID)
	if len(id) > 255 {
		id = id[:255]
	}
	param := make([]byte, 9+len(id))
	param[0] = wire.FuncUploadStart
	param[8] = byte(len(id))
	copy(param[9:], id)
	header := make([]byte, 12)
	header[0] = 0x32
	header[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(header[4:6], pduRef)
	binary.BigEndian.PutUint16(header[6:8], uint16(len(param)))
	binary.BigEndian.PutUint16(header[8:10], 0)
	return append(header, param...)
}

// buildUploadChunkResponse returns S7 Upload chunk response. done=true for last chunk.
func buildUploadChunkResponse(pduRef uint16, chunk []byte, done bool) []byte {
	param := []byte{wire.FuncUpload, 0}
	if !done {
		param[1] = 1
	}
	dataLen := 4 + len(chunk)
	data := make([]byte, dataLen)
	binary.BigEndian.PutUint16(data[2:4], uint16(len(chunk)*8))
	copy(data[4:], chunk)
	header := make([]byte, 12)
	header[0] = 0x32
	header[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(header[4:6], pduRef)
	binary.BigEndian.PutUint16(header[6:8], uint16(len(param)))
	binary.BigEndian.PutUint16(header[8:10], uint16(dataLen))
	return append(append(header, param...), data...)
}
