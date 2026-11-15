// SPDX-License-Identifier: MIT

package client

import (
	"encoding/binary"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// The limits of SECURITY.md, "Limits on what a peer can send": each test
// here asserts one of them, with the bytes one call may allocate where the
// limit bounds memory.

// allocatedBy returns the bytes the process allocated while fn ran. The
// scripted peer runs in the same process, so its answers are counted too.
func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// padTo appends octets after the declared sections of pdu until it is n long.
func padTo(pdu []byte, n int) []byte {
	return append(pdu, make([]byte, n-len(pdu))...)
}

// userdataOverhead is the length of a user data answer without its payload:
// S7 header, parameter section, and the return code, transport size and
// length of the data section.
const userdataOverhead = wire.S7HeaderSize + 12 + 4

// szlAnswer builds a one-packet SZL answer of exactly total octets.
func szlAnswer(ref, id uint16, total int) []byte {
	return buildSZLReply(ref, id, make([]byte, total-userdataOverhead-4))
}

// answersOfSize answers each request with a well-formed answer of total octets.
func answersOfSize(total int) func(ref uint16, req []byte) []byte {
	return func(ref uint16, req []byte) []byte {
		if wire.ROSCTR(req[1]).IsUserdata() {
			return szlAnswer(ref, binary.BigEndian.Uint16(req[22:24]), total)
		}
		switch req[wire.S7HeaderSize] {
		case wire.FuncReadVar:
			return padTo(readVarAnswer(ref, []byte{1, 2}), total)
		case wire.FuncWriteVar:
			return padTo(buildWriteVarResponse(ref, wire.RetCodeSuccess), total)
		case wire.FuncUploadStart:
			return buildStartUploadResponse(ref, 7)
		case wire.FuncUpload:
			return buildUploadChunkResponse(ref, make([]byte, total-wire.AckDataHeaderSize-2-4), true)
		}
		return buildAckData(ref, 0, 0, []byte{req[wire.S7HeaderSize]}, nil)
	}
}

// An answer is at most as long as the PDU size negotiated for the session:
// one of exactly that length is taken, a longer one is refused with
// ErrProtocolFailure and nothing of it reaches the result. The session stays
// open, since the answer was read in full.
func TestAnswerLengthIsBoundedByNegotiatedPDUSize(t *testing.T) {
	const pdu = 240
	for _, tc := range []struct {
		name  string
		total int
		ok    bool
	}{
		{"negotiated size", pdu, true},
		{"one more", pdu + 1, false},
		{"largest message", 65535, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t,
				func(ref uint16) []byte { return buildS7SetupResponse(ref, pdu) },
				answersOfSize(tc.total))
			c := connectScripted(t, port)
			ctx := testContext(t)
			check := func(op string, err error) {
				t.Helper()
				if tc.ok && err != nil {
					t.Errorf("%s: an answer of %d bytes on a session of %d: %v", op, tc.total, pdu, err)
				}
				if !tc.ok && !errors.Is(err, ErrProtocolFailure) {
					t.Errorf("%s: an answer of %d bytes on a session of %d: err = %v, want ErrProtocolFailure", op, tc.total, pdu, err)
				}
			}

			res, err := c.ReadDB(ctx, 1, 0, 2)
			if err != nil {
				t.Fatalf("ReadDB: %v", err)
			}
			check("ReadDB", res.Err())
			if !tc.ok && (res.Status != ReadStatusProtocolErr || len(res.Data) != 0) {
				t.Errorf("ReadDB: status %s with %d bytes", res.Status, len(res.Data))
			}
			check("WriteDB", c.WriteDB(ctx, 1, 0, []byte{1}))
			raw, err := c.ReadDiagBufferRaw(ctx)
			check("ReadDiagBufferRaw", err)
			if want := tc.total - userdataOverhead - 4; tc.ok && len(raw) != want {
				t.Errorf("ReadDiagBufferRaw: %d bytes, want %d", len(raw), want)
			}
			if !tc.ok && raw != nil {
				t.Errorf("ReadDiagBufferRaw: %d bytes of a refused answer", len(raw))
			}
			block, err := c.UploadBlock(ctx, model.BlockDB, 1)
			check("UploadBlock", err)
			if !tc.ok && block != nil {
				t.Errorf("UploadBlock: %d bytes of a refused answer", len(block.Data))
			}
			if got := c.PDUSize(); got != pdu {
				t.Errorf("PDUSize = %d after the calls, want the session still open at %d", got, pdu)
			}
		})
	}
}

// Negotiation can only lower the proposed PDU size: a larger size in the
// setup answer counts as the proposal.
func TestNegotiatedPDUSizeIsAtMostTheProposal(t *testing.T) {
	for _, tc := range []struct{ propose, granted, want int }{
		{480, 479, 479}, {480, 480, 480}, {480, 481, 480}, {480, 65535, 480},
		{240, 239, 239}, {240, 240, 240}, {240, 241, 240}, {240, 65535, 240},
		{960, 65535, 960}, {480, 0, 0},
	} {
		port := startScriptedPeer(t, func(ref uint16) []byte { return buildS7SetupResponse(ref, tc.granted) }, nil)
		c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithMaxPDU(tc.propose))
		if err := c.Connect(testContext(t)); err != nil {
			t.Fatalf("proposed %d, granted %d: Connect: %v", tc.propose, tc.granted, err)
		}
		if got := c.PDUSize(); got != tc.want {
			t.Errorf("proposed %d, granted %d: PDUSize = %d, want %d", tc.propose, tc.granted, got, tc.want)
		}
		if got := c.ConnectionInfo().PDUSize; got != tc.want {
			t.Errorf("proposed %d, granted %d: ConnectionInfo().PDUSize = %d, want %d", tc.propose, tc.granted, got, tc.want)
		}
		_ = c.Close()
	}
}

// A rack/slot probe proposes 480 and reports no larger size; a strict
// probe's follow-up answer is bounded by it too.
func TestProbeRackSlotsPDUSizeIsAtMostTheProposal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		total  int
		status ProbeStatus
	}{
		{"answer of the proposed size", probeMaxPDU, StatusValidQuery},
		{"answer one longer", probeMaxPDU + 1, StatusValidConnect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := startScriptedPeer(t,
				func(ref uint16) []byte { return buildS7SetupResponse(ref, 65535) },
				answersOfSize(tc.total))
			res, err := ProbeRackSlots(testContext(t), RackSlotProbeRequest{
				Address: "127.0.0.1", Port: port, Parallelism: 1, Strict: true, Confirm: ConfirmSZL,
			})
			if err != nil {
				t.Fatalf("ProbeRackSlots: %v", err)
			}
			got := res.Candidates[0]
			if got.PDUSize != probeMaxPDU {
				t.Errorf("PDUSize = %d, want the proposed %d", got.PDUSize, probeMaxPDU)
			}
			if got.Status != tc.status {
				t.Errorf("status = %s (%s), want %s", got.Status, got.Error, tc.status)
			}
		})
	}
}

// When the setup answer grants no PDU size, answers are bounded by the size
// the client proposed.
func TestAnswerLengthIsBoundedByProposalWithoutNegotiatedSize(t *testing.T) {
	for _, tc := range []struct {
		total int
		ok    bool
	}{{480, true}, {481, false}} {
		port := startScriptedPeer(t,
			func(ref uint16) []byte { return buildS7SetupResponse(ref, 0) },
			answersOfSize(tc.total))
		c := connectScripted(t, port)
		_, err := c.ReadDiagBufferRaw(testContext(t))
		if tc.ok && err != nil {
			t.Errorf("answer of %d bytes: %v", tc.total, err)
		}
		if !tc.ok && !errors.Is(err, ErrProtocolFailure) {
			t.Errorf("answer of %d bytes: err = %v, want ErrProtocolFailure", tc.total, err)
		}
	}
}

// userdataCalls are the calls that collect a user data answer of several
// packets, with the function group and subfunction of their request.
var userdataCalls = []struct {
	name           string
	group, subfunc byte
	call           func(c *Client, t *testing.T) (resultBytes int, err error)
}{
	{"ListBlocks", wire.UserdataGroupBlock, wire.UserdataBlockList, func(c *Client, t *testing.T) (int, error) {
		blocks, err := c.ListBlocks(testContext(t), model.BlockDB)
		return len(blocks), err
	}},
	{"BlockCounts", wire.UserdataGroupBlock, wire.UserdataBlockCount, func(c *Client, t *testing.T) (int, error) {
		counts, err := c.BlockCounts(testContext(t))
		return len(counts), err
	}},
	{"ReadDiagBufferRaw", wire.UserdataGroupCPU, wire.UserdataSZL, func(c *Client, t *testing.T) (int, error) {
		raw, err := c.ReadDiagBufferRaw(testContext(t))
		return len(raw), err
	}},
}

// endlessUserdata answers every user data request with a packet of total
// octets marked "more data follows".
func endlessUserdata(total int, packets *atomic.Int32) func(ref uint16, req []byte) []byte {
	return func(ref uint16, req []byte) []byte {
		packets.Add(1)
		param := req[wire.S7HeaderSize:]
		group, subfunc, first := param[5]&0x0f, param[6], param[3] == 0x04
		payload := make([]byte, total-userdataOverhead)
		if group == wire.UserdataGroupCPU && subfunc == wire.UserdataSZL && first {
			copy(payload, req[22:26]) // first packet of an SZL answer: ID and index
		}
		data := append([]byte{wire.RetCodeSuccess, 0x09, byte(len(payload) >> 8), byte(len(payload))}, payload...)
		return buildUserdataResponse(ref, group, subfunc, 1, true, data)
	}
}

// Packets longer than the session allows are not collected: the call ends at
// the first one, whatever PDU size the setup answer named, and allocates a
// small multiple of that one message.
func TestUserdataOversizedPacketsAllocationBudget(t *testing.T) {
	const budget = 2 << 20 // one 65535-byte message, built, sent, reassembled and refused
	for _, tc := range userdataCalls {
		t.Run(tc.name, func(t *testing.T) {
			var packets atomic.Int32
			port := startScriptedPeer(t,
				func(ref uint16) []byte { return buildS7SetupResponse(ref, 65535) },
				endlessUserdata(65535, &packets))
			c := connectScripted(t, port)
			var err error
			var n int
			got := allocatedBy(func() { n, err = tc.call(c, t) })
			if !errors.Is(err, ErrProtocolFailure) || n != 0 {
				t.Fatalf("result of %d, err = %v; want ErrProtocolFailure and no result", n, err)
			}
			if p := packets.Load(); p != 1 {
				t.Errorf("the peer was asked for %d packets, want 1", p)
			}
			t.Logf("allocated %d bytes, budget %d", got, budget)
			if got > budget {
				t.Errorf("allocated %d bytes, budget %d", got, budget)
			}
		})
	}
}

// blockListPeer answers a block list request with n entries, perPacket in
// each packet.
func blockListPeer(n, perPacket int) func(ref uint16, req []byte) []byte {
	sent := 0
	return func(ref uint16, _ []byte) []byte {
		count := min(perPacket, n-sent)
		payload := make([]byte, 4*count)
		for i := range count {
			binary.BigEndian.PutUint16(payload[4*i:], uint16(sent+i))
		}
		sent += count
		data := append([]byte{wire.RetCodeSuccess, 0x09, byte(len(payload) >> 8), byte(len(payload))}, payload...)
		return buildUserdataResponse(ref, wire.UserdataGroupBlock, wire.UserdataBlockList, 1, sent < n, data)
	}
}

// A block number is 16 bits, so a list of one type names at most 65,536
// blocks: that many are returned, one more is refused.
func TestListBlocksEntryLimit(t *testing.T) {
	const pdu = 4096
	for _, tc := range []struct {
		entries int
		ok      bool
	}{{0, true}, {1, true}, {maxBlockListEntries - 1, true}, {maxBlockListEntries, true}, {maxBlockListEntries + 1, false}} {
		port := startScriptedPeer(t,
			func(ref uint16) []byte { return buildS7SetupResponse(ref, pdu) },
			blockListPeer(tc.entries, 1000))
		c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithMaxPDU(pdu))
		if err := c.Connect(testContext(t)); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		blocks, err := c.ListBlocks(testContext(t), model.BlockDB)
		_ = c.Close()
		if !tc.ok {
			if !errors.Is(err, ErrProtocolFailure) || blocks != nil {
				t.Errorf("%d entries: %d blocks, err = %v; want ErrProtocolFailure", tc.entries, len(blocks), err)
			}
			continue
		}
		if err != nil || len(blocks) != tc.entries {
			t.Errorf("%d entries: %d blocks, %v", tc.entries, len(blocks), err)
			continue
		}
		if n := len(blocks); n > 0 && (blocks[0].Number != 0 || blocks[n-1].Number != n-1) {
			t.Errorf("%d entries: first block %d, last block %d", tc.entries, blocks[0].Number, blocks[n-1].Number)
		}
	}
}

// A block count answer is keyed by a one-byte block type: the map holds at
// most 256 types and is sized for no more, however many entries the answer
// repeats.
func TestBlockCountMapSizeAndAllocationBudget(t *testing.T) {
	for _, n := range []int{0, 1, maxBlockTypes - 1, maxBlockTypes, maxBlockTypes + 1, 1 << 20} {
		counts := make([]wire.BlockCount, n)
		for i := range counts {
			counts[i] = wire.BlockCount{BlockType: byte(i), Count: i}
		}
		var got map[model.BlockType]int
		allocated := allocatedBy(func() { got = blockCountMap(counts) })
		if want := min(n, maxBlockTypes); len(got) != want {
			t.Errorf("%d entries: %d types, want %d", n, len(got), want)
		}
		if n > 0 && got[model.BlockType(byte(n-1))] != n-1 {
			t.Errorf("%d entries: type %#x counts %d, want the last entry's %d", n, byte(n-1), got[model.BlockType(byte(n-1))], n-1)
		}
		// A map of 256 one-byte keys with int values, with room to spare.
		if budget := uint64(32 << 10); allocated > budget {
			t.Errorf("%d entries: allocated %d bytes, budget %d", n, allocated, budget)
		}
	}
}

// A block count answer that fills a large PDU with one repeated type decodes
// to that one type.
func TestBlockCountsRepeatedType(t *testing.T) {
	const pdu = 4096
	payload := make([]byte, (pdu-userdataOverhead)/4*4)
	for i := 0; i < len(payload); i += 4 {
		payload[i], payload[i+1], payload[i+3] = 0x30, wire.BlockTypeDB, 1
	}
	port := startScriptedPeer(t,
		func(ref uint16) []byte { return buildS7SetupResponse(ref, pdu) },
		func(ref uint16, _ []byte) []byte {
			return buildBlockResponse(ref, wire.UserdataBlockCount, payload)
		})
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithMaxPDU(pdu))
	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	if counts, err := c.BlockCounts(testContext(t)); err != nil || len(counts) != 1 || counts[model.BlockDB] != 1 {
		t.Fatalf("BlockCounts = %v, %v", counts, err)
	}
}

// endlessUpload answers Start Upload, then every Upload request with a
// segment of segment bytes marked "more follows".
func endlessUpload(segment int, requests *atomic.Int32) func(ref uint16, req []byte) []byte {
	chunk := make([]byte, segment)
	return func(ref uint16, req []byte) []byte {
		switch req[wire.S7HeaderSize] {
		case wire.FuncUploadStart:
			return buildStartUploadResponse(ref, 7)
		case wire.FuncUpload:
			requests.Add(1)
			return buildUploadChunkResponse(ref, chunk, false)
		}
		return buildAckData(ref, 0, 0, []byte{req[wire.S7HeaderSize]}, nil)
	}
}

// An upload that never ends is cut at 8 MiB when its segments are long (a
// session of the largest PDU size), and the call allocates a small multiple
// of that.
func TestUploadBlockByteLimitAllocationBudget(t *testing.T) {
	const pdu = 65535
	const segment = pdu - wire.AckDataHeaderSize - 2 - 4
	// The collected block and its growth, the transport's reassembly and the
	// peer's own building of each segment: about ten times the block.
	const budget = 24 * maxUploadBytes
	var requests atomic.Int32
	port := startScriptedPeer(t,
		func(ref uint16) []byte { return buildS7SetupResponse(ref, pdu) },
		endlessUpload(segment, &requests))
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithMaxPDU(pdu))
	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	var block *model.BlockData
	var err error
	got := allocatedBy(func() { block, err = c.UploadBlock(t.Context(), model.BlockDB, 1) })
	if !errors.Is(err, ErrProtocolFailure) || block != nil {
		t.Fatalf("err = %v, want ErrProtocolFailure and no block", err)
	}
	if n, want := int(requests.Load()), maxUploadBytes/segment+1; n != want {
		t.Errorf("%d segments requested, want %d (the first one past %d bytes)", n, want, maxUploadBytes)
	}
	t.Logf("allocated %d bytes, budget %d", got, budget)
	if got > budget {
		t.Errorf("allocated %d bytes, budget %d", got, budget)
	}
}

// An upload that never ends is cut at 4,096 segments when they are short.
func TestUploadBlockSegmentLimit(t *testing.T) {
	var requests atomic.Int32
	port := startScriptedPeer(t, nil, endlessUpload(1, &requests))
	c := connectScripted(t, port)
	block, err := c.UploadBlock(t.Context(), model.BlockDB, 1)
	if err == nil || block != nil || !strings.Contains(err.Error(), "4096 chunks") {
		t.Fatalf("err = %v, want the segment limit", err)
	}
	if n := requests.Load(); n != maxUploadChunks {
		t.Errorf("%d segments requested, want %d", n, maxUploadChunks)
	}
}
