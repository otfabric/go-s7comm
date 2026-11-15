// SPDX-License-Identifier: MIT

package wire

import (
	"bytes"
	"testing"
)

// The decoders of this package return slices borrowed from their input
// (ReadVarItem.Data, the codes of ParseWriteVarResponseCodes, the payload of
// ParseUserdataData, SZLResponse.Data, UploadChunk.Data, the rest of
// ParseS7Header, WriteVarData.Data). These tests pin both halves of that
// contract: a borrowed slice shows a later change of the input, and it has
// no capacity beyond its length, so an append to it leaves the input alone.

// borrowed is one decoder result with the input it came from.
type borrowed struct {
	name   string
	input  []byte // the buffer the decoder was given
	result []byte // a slice the decoder returned
	at     int    // offset of result[0] in input
}

// borrowedResults decodes fixtures and returns every borrowed slice. Each
// input is a region of a larger buffer, as a PDU inside a receive buffer is,
// so that an append past a result would land on bytes that follow it.
func borrowedResults(t *testing.T) []borrowed {
	t.Helper()
	// in copies b into a buffer with 16 spare bytes after it.
	in := func(b []byte) []byte {
		buf := make([]byte, len(b), len(b)+16)
		copy(buf, b)
		return buf
	}
	var out []borrowed

	// Read Var answer of two items: 3 bytes with a fill byte, then 2 bytes.
	readData := in([]byte{RetCodeSuccess, DataTransportSizeByte, 0, 24, 0xA1, 0xA2, 0xA3, 0x00, RetCodeSuccess, DataTransportSizeByte, 0, 16, 0xB1, 0xB2})
	items, err := ParseReadVarResponse([]byte{FuncReadVar, 2}, readData)
	if err != nil || len(items) != 2 {
		t.Fatalf("ParseReadVarResponse: %+v, %v", items, err)
	}
	out = append(out,
		borrowed{"ReadVarItem.Data, first item", readData, items[0].Data, 4},
		borrowed{"ReadVarItem.Data, last item", readData, items[1].Data, 12})

	writeData := in([]byte{RetCodeSuccess, RetCodeAddressFault, 0x77})
	codes, err := ParseWriteVarResponseCodes([]byte{FuncWriteVar, 2}, writeData)
	if err != nil {
		t.Fatalf("ParseWriteVarResponseCodes: %v", err)
	}
	out = append(out, borrowed{"ParseWriteVarResponseCodes", writeData, codes, 0})

	userdata := in([]byte{RetCodeSuccess, 0x09, 0, 2, 0x30, 0x38, 0x77})
	payload, err := ParseUserdataData(userdata)
	if err != nil {
		t.Fatalf("ParseUserdataData: %v", err)
	}
	out = append(out, borrowed{"ParseUserdataData", userdata, payload, 4})

	szlData := in([]byte{RetCodeSuccess, 0x09, 0, 6, 0x04, 0x24, 0, 0, 0xC1, 0xC2, 0x77})
	szl, err := ParseSZLResponse(szlData)
	if err != nil {
		t.Fatalf("ParseSZLResponse: %v", err)
	}
	out = append(out, borrowed{"SZLResponse.Data", szlData, szl.Data, 8})

	uploadData := in([]byte{0, 3, 0, 0xfb, 0xD1, 0xD2, 0xD3, 0x77})
	chunk, err := ParseUploadResponse([]byte{FuncUpload, 0}, uploadData)
	if err != nil {
		t.Fatalf("ParseUploadResponse: %v", err)
	}
	out = append(out, borrowed{"UploadChunk.Data", uploadData, chunk.Data, 4})

	pdu := in([]byte{0x32, byte(ROSCTRAckData), 0, 0, 0, 1, 0, 2, 0, 1, 0, 0, FuncWriteVar, 1, RetCodeSuccess})
	_, rest, err := ParseS7Header(pdu)
	if err != nil {
		t.Fatalf("ParseS7Header: %v", err)
	}
	out = append(out, borrowed{"rest of ParseS7Header", pdu, rest, 12})

	// Write Var job of two items: 1 byte with a fill byte, then 2 bytes.
	item := []byte{0x12, 0x0a, SyntaxIDS7Any, TransportSizeByte, 0, 1, 0, 1, AreaDB, 0, 0, 0}
	jobParam := append(append([]byte{FuncWriteVar, 2}, item...), item...)
	jobData := in([]byte{0, DataTransportSizeByte, 0, 8, 0xE1, 0x00, 0, DataTransportSizeByte, 0, 16, 0xF1, 0xF2})
	_, writes, err := ParseWriteVarRequest(jobParam, jobData)
	if err != nil || len(writes) != 2 {
		t.Fatalf("ParseWriteVarRequest: %+v, %v", writes, err)
	}
	out = append(out,
		borrowed{"WriteVarData.Data, first item", jobData, writes[0].Data, 4},
		borrowed{"WriteVarData.Data, last item", jobData, writes[1].Data, 10})
	return out
}

// An append to a borrowed result allocates: nothing of the input changes,
// neither the bytes after the result (the next item, a fill byte) nor the
// spare capacity of the buffer.
func TestAppendToBorrowedResultLeavesInputAlone(t *testing.T) {
	for _, b := range borrowedResults(t) {
		t.Run(b.name, func(t *testing.T) {
			if len(b.result) == 0 {
				t.Fatal("fixture decodes to an empty slice")
			}
			if cap(b.result) != len(b.result) {
				t.Errorf("capacity %d beyond the length %d", cap(b.result), len(b.result))
			}
			whole := b.input[:cap(b.input)]
			before := bytes.Clone(whole)
			grown := append(b.result, 0xEE, 0xEE, 0xEE, 0xEE)
			if !bytes.Equal(whole, before) {
				t.Errorf("append wrote into the input:\nwas % x\nnow % x", before, whole)
			}
			if !bytes.Equal(grown[:len(b.result)], b.result) {
				t.Errorf("appended slice does not start with the result")
			}
		})
	}
}

// A borrowed result is a view of the input, not a copy: it shows a change
// made to the input afterwards. A caller that keeps the result copies it.
func TestBorrowedResultShowsLaterChangeOfInput(t *testing.T) {
	for _, b := range borrowedResults(t) {
		t.Run(b.name, func(t *testing.T) {
			kept := bytes.Clone(b.result)
			for i := range b.result {
				b.input[b.at+i] ^= 0xFF
			}
			for i := range b.result {
				if b.result[i] != kept[i]^0xFF {
					t.Fatalf("byte %d of the result is %#x after the input changed from %#x: not the documented view", i, b.result[i], kept[i])
				}
			}
		})
	}
}
