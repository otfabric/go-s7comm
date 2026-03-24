package client

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/transport"
	"github.com/otfabric/go-s7comm/wire"
)

func TestListAllBlocksPartialFailure(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	blocks, err := c.ListAllBlocks(ctx)
	if err == nil {
		t.Fatal("ListAllBlocks on disconnected client should return error")
	}
	if len(blocks) != 0 {
		t.Errorf("expected no blocks when all list calls fail, got %d", len(blocks))
	}
	// Should be a joined error (multiple block types failed)
	var joinErr interface{ Unwrap() []error }
	if !errors.As(err, &joinErr) {
		t.Errorf("expected errors.Join-style error, got %T", err)
	}
	if joinErr != nil && len(joinErr.Unwrap()) < 2 {
		t.Errorf("expected multiple wrapped errors, got %d", len(joinErr.Unwrap()))
	}
}

func TestListBlocksNotConnected(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	_, err := c.ListBlocks(ctx, model.BlockDB)
	if err == nil {
		t.Fatal("ListBlocks without connection should return error")
	}
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestGetBlockInfoBlockNumberRejected(t *testing.T) {
	c := New("host")
	ctx := context.Background()
	_, err := c.GetBlockInfo(ctx, model.BlockDB, -1)
	if err == nil {
		t.Fatal("GetBlockInfo with negative block number should return error")
	}
	_, err = c.GetBlockInfo(ctx, model.BlockDB, 70000)
	if err == nil {
		t.Fatal("GetBlockInfo with block number > 65535 should return error")
	}
}

// startFakeUploadServer runs a fake PLC that performs handshake then responds to Start Upload + N chunk responses.
// Chunks are sent in order; last chunk has Done=true. Session ID is "S1".
func startFakeUploadServer(t *testing.T, chunks [][]byte) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		tr := transport.New(conn, 2*time.Second)
		payload, _ := tr.Receive()
		dec, _ := cotp.Decode(payload)
		sendCOTPCC(tr, &dec)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 18 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			resp := buildS7SetupResponse(pduRef, 480)
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		// Start Upload
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT == nil || len(dec.DT.UserData) < 12 {
			return
		}
		s7 := dec.DT.UserData
		pduRef := binary.BigEndian.Uint16(s7[4:6])
		startResp := buildStartUploadResponse(pduRef, "S1")
		dtBytes, _ := wire.EncodeCOTPDT(startResp)
		_ = tr.Send(dtBytes)
		// Upload chunks
		for i, ch := range chunks {
			payload, _ = tr.Receive()
			dec, _ = cotp.Decode(payload)
			if dec.DT == nil || len(dec.DT.UserData) < 12 {
				return
			}
			s7 = dec.DT.UserData
			pduRef = binary.BigEndian.Uint16(s7[4:6])
			done := i == len(chunks)-1
			chunkResp := buildUploadChunkResponse(pduRef, ch, done)
			dtBytes, _ = wire.EncodeCOTPDT(chunkResp)
			_ = tr.Send(dtBytes)
		}
		// End Upload (client sends; we may receive and ignore)
		_, _ = tr.Receive()
	}()
	return port, func() { _ = ln.Close() }
}

func TestUploadBlock_NotConnected(t *testing.T) {
	c := New("127.0.0.1")
	defer func() { _ = c.Close() }()
	_, err := c.UploadBlock(context.Background(), model.BlockDB, 1)
	if err == nil {
		t.Fatal("expected error when not connected")
	}
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestUploadBlock_WithFakeServer_Success(t *testing.T) {
	chunk1 := make([]byte, 100)
	for i := range chunk1 {
		chunk1[i] = byte(i)
	}
	chunk2 := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	port, cleanup := startFakeUploadServer(t, [][]byte{chunk1, chunk2})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	block, err := c.UploadBlock(ctx, model.BlockDB, 1)
	if err != nil {
		t.Fatalf("UploadBlock: %v", err)
	}
	if block == nil {
		t.Fatal("block is nil")
	}
	expected := append(chunk1, chunk2...)
	if len(block.Data) != len(expected) {
		t.Errorf("block data length %d, want %d", len(block.Data), len(expected))
	}
	if len(block.Data) >= len(expected) && !bytesEqual(block.Data, expected) {
		t.Errorf("block data mismatch")
	}
}

// startFakeUploadServerBadStartResponse does handshake then responds to Start Upload with invalid param (wrong function code).
func startFakeUploadServerBadStartResponse(t *testing.T) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		tr := transport.New(conn, 2*time.Second)
		payload, _ := tr.Receive()
		dec, _ := cotp.Decode(payload)
		sendCOTPCC(tr, &dec)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 18 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			resp := buildS7SetupResponse(pduRef, 480)
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT == nil || len(dec.DT.UserData) < 12 {
			return
		}
		s7 := dec.DT.UserData
		pduRef := binary.BigEndian.Uint16(s7[4:6])
		// Respond with wrong function code so ParseStartUploadResponse fails
		badParam := []byte{wire.FuncUpload, 0, 0, 0, 0, 0, 0, 0, 2, 'X', 'Y'}
		header := make([]byte, 12)
		header[0] = 0x32
		header[1] = byte(wire.ROSCTRAckData)
		binary.BigEndian.PutUint16(header[4:6], pduRef)
		binary.BigEndian.PutUint16(header[6:8], uint16(len(badParam)))
		binary.BigEndian.PutUint16(header[8:10], 0)
		startResp := append(header, badParam...)
		dtBytes, _ := wire.EncodeCOTPDT(startResp)
		_ = tr.Send(dtBytes)
	}()
	return port, func() { _ = ln.Close() }
}

func TestUploadBlock_InvalidStartUploadResponse(t *testing.T) {
	port, cleanup := startFakeUploadServerBadStartResponse(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	_, err := c.UploadBlock(ctx, model.BlockDB, 1)
	if err == nil {
		t.Fatal("expected error from invalid start upload response")
	}
	if !errors.Is(err, ErrProtocolFailure) {
		t.Errorf("expected ErrProtocolFailure, got %v", err)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestUploadBlock_EmptyPayloadReturnsError: done=true with no payload returns "upload completed with no payload".
func TestUploadBlock_EmptyPayloadReturnsError(t *testing.T) {
	port, cleanup := startFakeUploadServer(t, [][]byte{[]byte{}}) // one chunk, empty data, done=true
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	_, err := c.UploadBlock(ctx, model.BlockDB, 1)
	if err == nil {
		t.Fatal("expected error for upload with no payload")
	}
	if !strings.Contains(err.Error(), "upload completed with no payload") {
		t.Errorf("expected 'upload completed with no payload' in error, got %q", err.Error())
	}
}

// TestUploadBlock_ProtocolFailureOnChunkNoRetry: protocol failure on chunk fails fast without retrying.
func TestUploadBlock_ProtocolFailureOnChunkNoRetry(t *testing.T) {
	port, cleanup := startFakeUploadServerBadChunkResponse(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	_, err := c.UploadBlock(ctx, model.BlockDB, 1)
	if err == nil {
		t.Fatal("expected error from bad chunk response")
	}
	if !errors.Is(err, ErrProtocolFailure) {
		t.Errorf("expected ErrProtocolFailure, got %v", err)
	}
	if !strings.Contains(err.Error(), "upload chunk 0") {
		t.Errorf("expected 'upload chunk 0' in error, got %q", err.Error())
	}
}

// startFakeUploadServerBadChunkResponse: handshake + start upload, then first chunk gets malformed response.
func startFakeUploadServerBadChunkResponse(t *testing.T) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		tr := transport.New(conn, 2*time.Second)
		payload, _ := tr.Receive()
		dec, _ := cotp.Decode(payload)
		sendCOTPCC(tr, &dec)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 18 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			resp := buildS7SetupResponse(pduRef, 480)
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT == nil || len(dec.DT.UserData) < 12 {
			return
		}
		s7 := dec.DT.UserData
		pduRef := binary.BigEndian.Uint16(s7[4:6])
		startResp := buildStartUploadResponse(pduRef, "S1")
		dtBytes, _ := wire.EncodeCOTPDT(startResp)
		_ = tr.Send(dtBytes)
		// First chunk request: respond with wrong function code so ParseUploadResponse fails
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT == nil || len(dec.DT.UserData) < 12 {
			return
		}
		s7 = dec.DT.UserData
		pduRef = binary.BigEndian.Uint16(s7[4:6])
		badParam := []byte{0x00, 0x00} // wrong function, not wire.FuncUpload
		header := make([]byte, 12)
		header[0] = 0x32
		header[1] = byte(wire.ROSCTRAckData)
		binary.BigEndian.PutUint16(header[4:6], pduRef)
		binary.BigEndian.PutUint16(header[6:8], uint16(len(badParam)))
		binary.BigEndian.PutUint16(header[8:10], 0)
		badResp := append(header, badParam...)
		dtBytes, _ = wire.EncodeCOTPDT(badResp)
		_ = tr.Send(dtBytes)
	}()
	return port, func() { _ = ln.Close() }
}

// TestUploadBlock_CleanupFailureDoesNotChangeResult: cleanup (EndUpload) failure does not change the returned result.
func TestUploadBlock_CleanupFailureDoesNotChangeResult(t *testing.T) {
	chunk := []byte{0x01, 0x02, 0x03}
	port, cleanup := startFakeUploadServerCloseAfterLastChunk(t, [][]byte{chunk})
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port))
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	block, err := c.UploadBlock(ctx, model.BlockDB, 1)
	if err != nil {
		t.Fatalf("UploadBlock: %v", err)
	}
	if block == nil {
		t.Fatal("block is nil")
	}
	if len(block.Data) != 3 || block.Data[0] != 0x01 || block.Data[1] != 0x02 || block.Data[2] != 0x03 {
		t.Errorf("expected block data [1,2,3], got %v", block.Data)
	}
}

// startFakeUploadServerCloseAfterLastChunk: like startFakeUploadServer but closes connection after sending last chunk (cleanup will fail).
func startFakeUploadServerCloseAfterLastChunk(t *testing.T, chunks [][]byte) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		tr := transport.New(conn, 2*time.Second)
		payload, _ := tr.Receive()
		dec, _ := cotp.Decode(payload)
		sendCOTPCC(tr, &dec)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 18 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			resp := buildS7SetupResponse(pduRef, 480)
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT == nil || len(dec.DT.UserData) < 12 {
			_ = conn.Close()
			return
		}
		s7 := dec.DT.UserData
		pduRef := binary.BigEndian.Uint16(s7[4:6])
		startResp := buildStartUploadResponse(pduRef, "S1")
		dtBytes, _ := wire.EncodeCOTPDT(startResp)
		_ = tr.Send(dtBytes)
		for i, ch := range chunks {
			payload, _ = tr.Receive()
			dec, _ = cotp.Decode(payload)
			if dec.DT == nil || len(dec.DT.UserData) < 12 {
				_ = conn.Close()
				return
			}
			s7 = dec.DT.UserData
			pduRef = binary.BigEndian.Uint16(s7[4:6])
			done := i == len(chunks)-1
			chunkResp := buildUploadChunkResponse(pduRef, ch, done)
			dtBytes, _ = wire.EncodeCOTPDT(chunkResp)
			_ = tr.Send(dtBytes)
			if done {
				_ = conn.Close() // close before client sends EndUpload; cleanup will fail
				return
			}
		}
	}()
	return port, func() { _ = ln.Close() }
}

// TestUploadBlock_ContextCanceledReturnsPromptly: context canceled/deadline during upload returns without long delay.
func TestUploadBlock_ContextCanceledReturnsPromptly(t *testing.T) {
	port, cleanup := startFakeUploadServerHangOnFirstChunk(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c := New("127.0.0.1", WithRackSlot(0, 1), WithPort(port), WithTimeout(100*time.Millisecond))
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	start := time.Now()
	_, err := c.UploadBlock(ctx, model.BlockDB, 1)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error when context times out during upload")
	}
	if elapsed > 2*time.Second {
		t.Errorf("UploadBlock should return promptly on context deadline, took %v", elapsed)
	}
}

// startFakeUploadServerHangOnFirstChunk: handshake + start upload, then never responds to first chunk (client will hit deadline).
func startFakeUploadServerHangOnFirstChunk(t *testing.T) (port int, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		tr := transport.New(conn, 2*time.Second)
		payload, _ := tr.Receive()
		dec, _ := cotp.Decode(payload)
		sendCOTPCC(tr, &dec)
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT != nil && len(dec.DT.UserData) >= 18 {
			s7 := dec.DT.UserData
			pduRef := binary.BigEndian.Uint16(s7[4:6])
			resp := buildS7SetupResponse(pduRef, 480)
			dtBytes, _ := wire.EncodeCOTPDT(resp)
			_ = tr.Send(dtBytes)
		}
		payload, _ = tr.Receive()
		dec, _ = cotp.Decode(payload)
		if dec.DT == nil || len(dec.DT.UserData) < 12 {
			return
		}
		s7 := dec.DT.UserData
		pduRef := binary.BigEndian.Uint16(s7[4:6])
		startResp := buildStartUploadResponse(pduRef, "S1")
		dtBytes, _ := wire.EncodeCOTPDT(startResp)
		_ = tr.Send(dtBytes)
		// Receive first chunk request but never respond (client will timeout / context deadline)
		_, _ = tr.Receive()
		// hang: do not send response
	}()
	return port, func() { _ = ln.Close() }
}
