// SPDX-License-Identifier: MIT

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

// buildReadVarBitResponse builds a successful one-bit Read Var response (BIT transport, length 1).
func buildReadVarBitResponse(pduRef uint16, value byte) []byte {
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
	resp[15] = wire.DataTransportSizeBit
	binary.BigEndian.PutUint16(resp[16:18], 1)
	resp[18] = value
	return resp
}

// buildReadVarRejectedResponse builds a Read Var response with a non-success item return code.
func buildReadVarRejectedResponse(pduRef uint16, retCode byte) []byte {
	paramLen, dataLen := 2, 4
	resp := make([]byte, 12+paramLen+dataLen)
	resp[0] = 0x32
	resp[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(resp[4:6], pduRef)
	binary.BigEndian.PutUint16(resp[6:8], uint16(paramLen))
	binary.BigEndian.PutUint16(resp[8:10], uint16(dataLen))
	resp[12] = wire.FuncReadVar
	resp[13] = 1
	resp[14] = retCode
	resp[15] = 0x00
	return resp
}

// buildWriteVarResponse builds a Write Var ack with the given item return code.
func buildWriteVarResponse(pduRef uint16, retCode byte) []byte {
	resp := make([]byte, 12+2+1)
	resp[0] = 0x32
	resp[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(resp[4:6], pduRef)
	binary.BigEndian.PutUint16(resp[6:8], 2)
	resp[8] = 0
	resp[9] = 1
	resp[12] = wire.FuncWriteVar
	resp[13] = 1
	resp[14] = retCode
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
	data := make([]byte, 8+payloadLen)
	data[0] = wire.RetCodeSuccess
	data[1] = 0x09
	binary.BigEndian.PutUint16(data[2:4], uint16(4+payloadLen))
	binary.BigEndian.PutUint16(data[4:6], szlID)
	if len(payload) >= payloadLen {
		copy(data[8:], payload[:payloadLen])
	}
	return buildUserdataResponse(pduRef, wire.UserdataGroupCPU, wire.UserdataSZL, 0, false, data)
}

// buildUserdataResponse builds a user data response the way a CPU sends it:
// parameter 00 01 12 08 12 8<group> <subfunc> <seq> 00 <last> 00 00.
func buildUserdataResponse(pduRef uint16, group, subfunc, seq byte, more bool, data []byte) []byte {
	last := byte(0)
	if more {
		last = 1
	}
	param := []byte{0x00, 0x01, 0x12, 0x08, wire.UserdataRes, wire.UserdataTypeResponse | group, subfunc, seq, 0x00, last, 0x00, 0x00}
	h := make([]byte, 10)
	h[0] = 0x32
	h[1] = byte(wire.ROSCTRUserdata)
	binary.BigEndian.PutUint16(h[4:6], pduRef)
	binary.BigEndian.PutUint16(h[6:8], uint16(len(param)))
	binary.BigEndian.PutUint16(h[8:10], uint16(len(data)))
	return append(append(h, param...), data...)
}

// szlList builds SZL list data: record length, record count, records.
func szlList(recLen int, recs ...[]byte) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:2], uint16(recLen))
	binary.BigEndian.PutUint16(out[2:4], uint16(len(recs)))
	for _, r := range recs {
		rec := make([]byte, recLen)
		copy(rec, r)
		out = append(out, rec...)
	}
	return out
}

// buildStartUploadResponse returns a Start Upload response as a CPU sends
// it: status, 2 bytes, the 4-byte upload ID, the block length in ASCII.
func buildStartUploadResponse(pduRef uint16, uploadID uint32) []byte {
	param := []byte{wire.FuncUploadStart, 0x00, 0x01, 0x00, 0, 0, 0, 0, 0x07, '0', '0', '0', '0', '2', '1', '6'}
	binary.BigEndian.PutUint32(param[4:8], uploadID)
	header := make([]byte, 12)
	header[0] = 0x32
	header[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(header[4:6], pduRef)
	binary.BigEndian.PutUint16(header[6:8], uint16(len(param)))
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
	binary.BigEndian.PutUint16(data[0:2], uint16(len(chunk)))
	data[3] = 0xfb
	copy(data[4:], chunk)
	header := make([]byte, 12)
	header[0] = 0x32
	header[1] = byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(header[4:6], pduRef)
	binary.BigEndian.PutUint16(header[6:8], uint16(len(param)))
	binary.BigEndian.PutUint16(header[8:10], uint16(dataLen))
	return append(append(header, param...), data...)
}

// buildBlockResponse builds a block functions response with payload as the
// data after return code, transport size and length.
func buildBlockResponse(pduRef uint16, subfunc byte, payload []byte) []byte {
	data := append([]byte{wire.RetCodeSuccess, 0x09, byte(len(payload) >> 8), byte(len(payload))}, payload...)
	return buildUserdataResponse(pduRef, wire.UserdataGroupBlock, subfunc, 0, false, data)
}

// blockInfoRecord builds a 78-byte block info record in the S7-300 layout.
func blockInfoRecord(loadMem, localData, mc7 int) []byte {
	r := make([]byte, 78)
	binary.BigEndian.PutUint16(r[0:2], 0x0100)
	binary.BigEndian.PutUint16(r[2:4], 0x004a)
	copy(r[6:8], "pp")
	r[9], r[10], r[11] = 0x01, 0x05, 0x0a
	binary.BigEndian.PutUint16(r[12:14], 1)
	binary.BigEndian.PutUint32(r[14:18], uint32(loadMem))
	binary.BigEndian.PutUint16(r[38:40], uint16(localData))
	binary.BigEndian.PutUint16(r[40:42], uint16(mc7))
	copy(r[42:50], "AUTHOR")
	copy(r[50:58], "FAMILY")
	copy(r[58:66], "NAME")
	r[66] = 0x12
	binary.BigEndian.PutUint16(r[68:70], 0xbeef)
	return r
}
