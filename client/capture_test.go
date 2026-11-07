// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/internal/capture"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// replayKey identifies a request by its parameter and data sections, byte
// for byte; only the S7 header (PDU reference) is ignored. A request the
// client builds differently from the real clients in the capture finds no
// answer.
func replayKey(pdu []byte) (string, bool) {
	h, rest, err := wire.ParseS7Header(pdu)
	if err != nil || (!h.ROSCTR.IsJob() && !h.ROSCTR.IsUserdata()) {
		return "", false
	}
	pl, dl := int(h.ParamLength), int(h.DataLength)
	return fmt.Sprintf("%d %s %s", h.ROSCTR, hex.EncodeToString(rest[:pl]), hex.EncodeToString(rest[pl:pl+dl])), true
}

// replayer answers a client with the responses a real CPU gave to the same
// requests in a capture.
type replayer struct {
	answers map[string][][]byte // request key -> responses, in capture order

	mu        sync.Mutex
	used      map[string]int
	unmatched []string
}

func newReplayer(t *testing.T, path string) *replayer {
	t.Helper()
	convs, err := capture.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	r := &replayer{answers: map[string][][]byte{}, used: map[string]int{}}
	for _, c := range convs {
		open := map[uint16]string{}
		for _, p := range c.PDUs {
			h, _, err := wire.ParseS7Header(p.Data)
			if err != nil {
				continue
			}
			if p.FromClient {
				if k, ok := replayKey(p.Data); ok {
					open[h.PDURef] = k
				}
				continue
			}
			if k, ok := open[h.PDURef]; ok {
				r.answers[k] = append(r.answers[k], p.Data)
				delete(open, h.PDURef)
			}
		}
	}
	return r
}

// answer returns the recorded response for req with req's PDU reference,
// the next unused one when the capture holds several.
func (r *replayer) answer(req []byte) []byte {
	k, ok := replayKey(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	rs := r.answers[k]
	if !ok || len(rs) == 0 {
		r.unmatched = append(r.unmatched, k)
		return nil
	}
	i := r.used[k]
	if i >= len(rs) {
		i = len(rs) - 1
	}
	r.used[k]++
	out := append([]byte(nil), rs[i]...)
	copy(out[4:6], req[4:6])
	return out
}

// serve accepts one client and answers it until it disconnects. A request
// the capture does not hold gets a user data or Ack-Data error, as a CPU
// refusing it would send.
func (r *replayer) serve(t *testing.T) (addr string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				c, err := cotp.Accept(ctx, conn, cotp.ServerConfig{MaxTPDULength: 1024})
				if err != nil {
					return
				}
				defer func() { _ = c.Close() }()
				for {
					req, err := c.ReadTSDU(ctx)
					if err != nil {
						return
					}
					out := r.answer(req)
					if out == nil {
						out = refusal(req)
					}
					if err := c.WriteTSDU(ctx, out); err != nil {
						return
					}
				}
			}()
		}
	}()
	host, p, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(p)
	return host, n
}

// refusal answers a request the capture does not hold: Ack-Data with
// header error 0x8104 for a job, a user data response with error 0x8104
// and return code 0x0A otherwise.
func refusal(req []byte) []byte {
	ref := binary.BigEndian.Uint16(req[4:6])
	if len(req) > 1 && wire.ROSCTR(req[1]).IsUserdata() && len(req) >= 12+8 {
		p := req[12 : 12+8]
		param := []byte{0x00, 0x01, 0x12, 0x08, 0x12, 0x80 | p[5]&0x0f, p[6], p[7], 0x00, 0x00, 0x81, 0x04}
		data := []byte{0x0A, 0x00, 0x00, 0x00}
		h := make([]byte, 10)
		h[0], h[1] = 0x32, byte(wire.ROSCTRUserdata)
		binary.BigEndian.PutUint16(h[4:6], ref)
		binary.BigEndian.PutUint16(h[6:8], uint16(len(param)))
		binary.BigEndian.PutUint16(h[8:10], uint16(len(data)))
		return append(append(h, param...), data...)
	}
	out, _ := wire.EncodeAckData(ref, 0x81, 0x04, nil, nil)
	return out
}

// replayClient connects a client to a replay of path.
func replayClient(t *testing.T, path string) (*Client, *replayer) {
	t.Helper()
	r := newReplayer(t, path)
	host, port := r.serve(t)
	c := New(host, WithPort(port), WithRackSlot(0, 2), WithTimeout(3*time.Second))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, r
}

// noUnmatched fails when the client sent a request the capture does not
// hold, which means it built it differently from the real clients.
func (r *replayer) noUnmatched(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.unmatched) > 0 {
		t.Fatalf("requests no real client sent: %q", r.unmatched)
	}
}

// A CPU 315-2 PN/DP as Snap7 read it (gymgit snap7_s7-300_client-demo).
// Every expected value is in Snap7's own log of that session
// (snap7_s7-300_client-demo.log), except the firmware, which Wireshark
// decodes from SZL 0x0011 index 7 (Ausbg 0x5603, Ausbe 0x0207).
func TestCaptureReplayCPU315Snap7Session(t *testing.T) {
	c, r := replayClient(t, "../testdata/captures/gymgit/snap7_s7-300_client-demo.pcapng")
	ctx := context.Background()

	if c.PDUSize() != 240 {
		t.Errorf("PDU size %d, want 240 (log: PDU Negotiated 240)", c.PDUSize())
	}
	info, err := c.Identify(ctx) // SZL 0x001C comes in two packets
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	want := model.DeviceInfo{
		OrderNumber: "6ES7 315-2EH14-0AB0", SerialNumber: "S C-B1U393142011", ModuleName: "PLC_1",
		Copyright: "Original Siemens Equipment", ModuleType: "CPU 315-2 PN/DP", FWVersion: "V3.2.7",
		CPUType: "CPU 315-2 PN/DP", SystemName: "S7300/ET200M station_1", MemoryCardSerial: "MMC 4A1AC019",
	}
	if *info != want {
		t.Errorf("Identify = %+v\nwant       %+v", *info, want)
	}
	if st, err := c.GetCPUState(ctx); err != nil || st != model.CPUStateRun {
		t.Errorf("GetCPUState = %v, %v; want RUN", st, err)
	}
	counts, err := c.BlockCounts(ctx)
	if err != nil {
		t.Fatalf("BlockCounts: %v", err)
	}
	wantCounts := map[model.BlockType]int{model.BlockOB: 1, model.BlockFB: 1, model.BlockFC: 0, model.BlockSFB: 15, model.BlockSFC: 77, model.BlockDB: 2, model.BlockSDB: 8}
	if fmt.Sprint(counts) != fmt.Sprint(wantCounts) {
		t.Errorf("BlockCounts = %v, want %v", counts, wantCounts)
	}
	up, err := c.UploadBlock(ctx, model.BlockSDB, 0)
	if err != nil {
		t.Fatalf("UploadBlock: %v", err)
	}
	// 216 bytes (start upload: "0000216"); Snap7 prints the 144-byte body after the 36-byte block header.
	if len(up.Data) != 216 || !bytes.HasPrefix(up.Data[36:], []byte{0x1c, 0x03, 0x10, 0x01, 0x01, 0x01, 0x00, 0x00, 0x1f, 0x02, 0x02, 0x04, 0x00, 0x01, 0x21, 0x05}) ||
		!bytes.Contains(up.Data[36:180], []byte("S7300/ET200M station_1")) || !bytes.Contains(up.Data[36:180], []byte("PLC_1")) {
		t.Errorf("SDB 0 = %d bytes: % x", len(up.Data), up.Data)
	}
	// Get DB0: the CPU answers block info with return code 0x0A (log: "CPU : Item not available").
	var s7Err *wire.S7Error
	if _, err := c.GetBlockInfo(ctx, model.BlockDB, 0); !errors.As(err, &s7Err) || s7Err.Code != wire.RetCodeNotAvailable {
		t.Errorf("GetBlockInfo DB 0 = %v, want object not available", err)
	}
	r.noUnmatched(t)
}

// The same CPU as TIA Portal V13 went online (tia-v13_s7-300_go-online):
// block lists of each type (SFC in two packets), block headers, protection
// and the diagnostic buffer, compared with Wireshark 4.6.9's decoding of
// the capture.
func TestCaptureReplayCPU315TIAOnline(t *testing.T) {
	c, r := replayClient(t, "../testdata/captures/gymgit/tia-v13_s7-300_go-online.pcapng")
	ctx := context.Background()

	for bt, n := range map[model.BlockType]int{model.BlockOB: 1, model.BlockFB: 1, model.BlockDB: 2, model.BlockSDB: 8, model.BlockSFC: 77, model.BlockSFB: 15} {
		bl, err := c.ListBlocks(ctx, bt)
		if err != nil || len(bl) != n {
			t.Errorf("ListBlocks %v: %d blocks, %v; want %d", bt, len(bl), err, n)
		}
	}
	for _, tc := range []struct {
		bt   model.BlockType
		num  int
		want model.BlockInfo
	}{
		{model.BlockOB, 1, model.BlockInfo{Type: model.BlockOB, Number: 1, Language: 3, Flags: 1, LoadMemory: 324, LocalData: 26, MC7Size: 206, SBBLength: 28, Version: "0.1", CheckSum: 0x00c0}},
		{model.BlockDB, 1, model.BlockInfo{Type: model.BlockDB, Number: 1, Language: 5, Flags: 1, LoadMemory: 102, MC7Size: 10, SBBLength: 20, Author: "SIMATIC", Family: "IEC_TC", Name: "CTU", Version: "1.0", CheckSum: 0x798c}},
		{model.BlockFB, 1, model.BlockInfo{Type: model.BlockFB, Number: 1, Language: 1, Flags: 1, LoadMemory: 8960, LocalData: 122, MC7Size: 7764, SBBLength: 622, Author: "AUT_1", Family: "STDCONT", Name: "PID_CP", Version: "2.0", CheckSum: 0x8127}},
	} {
		got, err := c.GetBlockInfo(ctx, tc.bt, tc.num)
		if err != nil || *got != tc.want {
			t.Errorf("GetBlockInfo %v %d = %+v, %v\nwant %+v", tc.bt, tc.num, got, err, tc.want)
		}
	}
	if pl, err := c.GetProtectionLevel(ctx); err != nil || pl != model.ProtectionNone {
		t.Errorf("GetProtectionLevel = %v, %v; want level 1 (none)", pl, err)
	}
	diag, err := c.ReadDiagBuffer(ctx)
	if err != nil || len(diag.Entries) != 10 {
		t.Fatalf("ReadDiagBuffer = %+v, %v; want 10 entries", diag, err)
	}
	// Newest entry: event 0x4302, mode transition STARTUP to RUN, priority class 255.
	if e := diag.Entries[0]; e.EventID != 0x4302 || e.EventClass != 4 || e.Priority != 255 {
		t.Errorf("first diagnostic entry = %+v", e)
	}
	r.noUnmatched(t)
}

// An IM151-8 PN/DP CPU (ET 200S) from the Wireshark sample captures.
func TestCaptureReplayIM151Status(t *testing.T) {
	c, r := replayClient(t, "../testdata/captures/wireshark/unknown_s7-300_read-plc-status.pcap")
	ctx := context.Background()
	info, err := c.Identify(ctx)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if info.OrderNumber != "6ES7 151-8AB01-0AB0" || info.FWVersion != "V3.2.6" || info.ModuleType != "IM151-8 PN/DP CPU" ||
		info.SerialNumber != "S C-C6TW74882012" || info.MemoryCardSerial != "MMC 2900FC1A" {
		t.Errorf("Identify = %+v", *info)
	}
	if st, err := c.GetCPUState(ctx); err != nil || st != model.CPUStateRun {
		t.Errorf("GetCPUState = %v, %v; want RUN", st, err)
	}
	if diag, err := c.ReadDiagBuffer(ctx); err != nil || len(diag.Entries) != 10 {
		t.Errorf("ReadDiagBuffer = %v, %v", diag, err)
	}
	r.noUnmatched(t)
}

// The block lists of the same IM151-8 CPU, by an unidentified client.
func TestCaptureReplayIM151Blocks(t *testing.T) {
	c, r := replayClient(t, "../testdata/captures/wireshark/unknown_s7-300_list-blocks.pcap")
	ctx := context.Background()
	counts, err := c.BlockCounts(ctx)
	if err != nil {
		t.Fatalf("BlockCounts: %v", err)
	}
	for bt, n := range map[model.BlockType]int{model.BlockOB: 1, model.BlockDB: 1, model.BlockSDB: 12, model.BlockSFC: 71, model.BlockSFB: 14} {
		if counts[bt] != n {
			t.Errorf("count %v = %d, want %d", bt, counts[bt], n)
		}
		bl, err := c.ListBlocks(ctx, bt)
		if err != nil || len(bl) != n {
			t.Errorf("ListBlocks %v: %d, %v; want %d", bt, len(bl), err, n)
		}
	}
	r.noUnmatched(t)
}
