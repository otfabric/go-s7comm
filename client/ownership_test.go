// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"testing"

	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// What the client returns is the caller's: ReadResult.Data, BlockData.Data,
// the diagnostic buffer and block lists are copies, not views of a received
// PDU. Changing a result changes nothing else, and a later call on the same
// client does not change a result returned before.
func TestResultsAreTheCallers(t *testing.T) {
	segment := []byte{0xD1, 0xD2, 0xD3, 0xD4}
	diag := szlList(20, []byte{0x43, 0x02, 0xff})
	port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
		if wire.ROSCTR(req[1]).IsUserdata() {
			return buildSZLReply(ref, wire.SZLDiagBuffer, diag)
		}
		switch req[wire.S7HeaderSize] {
		case wire.FuncReadVar:
			return readVarAnswer(ref, []byte{0xA1, 0xA2, 0xA3, 0xA4})
		case wire.FuncUploadStart:
			return buildStartUploadResponse(ref, 7)
		case wire.FuncUpload:
			return buildUploadChunkResponse(ref, segment, true)
		}
		return buildAckData(ref, 0, 0, []byte{req[wire.S7HeaderSize]}, nil)
	})
	c := connectScripted(t, port)

	read := func() []byte {
		t.Helper()
		res, err := c.ReadDB(testContext(t), 1, 0, 4)
		if err != nil || !res.OK() {
			t.Fatalf("ReadDB: %+v, %v", res, err)
		}
		return res.Data
	}
	upload := func() []byte {
		t.Helper()
		block, err := c.UploadBlock(testContext(t), model.BlockDB, 1)
		if err != nil {
			t.Fatalf("UploadBlock: %v", err)
		}
		return block.Data
	}
	diagRaw := func() []byte {
		t.Helper()
		raw, err := c.ReadDiagBufferRaw(testContext(t))
		if err != nil {
			t.Fatalf("ReadDiagBufferRaw: %v", err)
		}
		return raw
	}

	for name, call := range map[string]func() []byte{"ReadResult.Data": read, "BlockData.Data": upload, "ReadDiagBufferRaw": diagRaw} {
		first := call()
		want := bytes.Clone(first)
		// Other calls in between reuse whatever the client or the transport keeps.
		_, _, _ = read(), upload(), diagRaw()
		if !bytes.Equal(first, want) {
			t.Errorf("%s changed with later calls: % x, was % x", name, first, want)
		}
		for i := range first {
			first[i] = 0xEE
		}
		_ = append(first[:0], bytes.Repeat([]byte{0xEE}, 64)...)
		if again := call(); !bytes.Equal(again, want) {
			t.Errorf("%s: after changing the earlier result the call returns % x, want % x", name, again, want)
		}
	}
}
