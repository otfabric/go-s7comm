// SPDX-License-Identifier: MIT

package wire

import (
	"bytes"
	"errors"
	"testing"
)

func TestSetupCommRoundTrip(t *testing.T) {
	req, err := EncodeSetupCommRequest(5, 3, 4, 960)
	if err != nil {
		t.Fatal(err)
	}
	h, body, err := ParseS7Header(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSetupCommRequest(body[:h.ParamLength])
	if err != nil || *got != (SetupCommRequest{MaxAmqCalling: 3, MaxAmqCalled: 4, PDUSize: 960}) {
		t.Fatalf("ParseSetupCommRequest = %+v, %v", got, err)
	}
	out, err := EncodeSetupCommResponse(5, 3, 4, 480)
	if err != nil {
		t.Fatal(err)
	}
	h, body, err = ParseS7Header(out)
	if err != nil || h.ROSCTR != ROSCTRAckData || h.PDURef != 5 {
		t.Fatalf("response header %+v, %v", h, err)
	}
	resp, err := ParseSetupCommResponse(body)
	if err != nil || resp.PDUSize != 480 || resp.MaxAmqCalling != 3 || resp.MaxAmqCalled != 4 {
		t.Fatalf("ParseSetupCommResponse = %+v, %v", resp, err)
	}
	for _, bad := range [][]byte{nil, {FuncSetupComm, 0, 0}, {FuncReadVar, 0, 0, 1, 0, 1, 1, 0xE0}} {
		if _, err := ParseSetupCommRequest(bad); !errors.Is(err, ErrMalformedRequest) {
			t.Errorf("ParseSetupCommRequest(%x) = %v", bad, err)
		}
	}
	for _, args := range [][3]int{{-1, 1, 1}, {1, 70000, 1}, {1, 1, -5}} {
		if _, err := EncodeSetupCommResponse(1, args[0], args[1], args[2]); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("EncodeSetupCommResponse%v = %v", args, err)
		}
	}
}

func TestReadVarRequestRoundTrip(t *testing.T) {
	addrs := []S7AnyAddress{{Area: AreaDB, DBNumber: 7, Start: 3, Size: 10}, {Area: AreaMerkers, Start: 100, Size: 1}}
	req, err := EncodeReadVarRequest(9, addrs)
	if err != nil {
		t.Fatal(err)
	}
	h, body, _ := ParseS7Header(req)
	specs, err := ParseReadVarRequest(body[:h.ParamLength])
	if err != nil {
		t.Fatal(err)
	}
	want := []VarSpec{
		{Syntax: SyntaxIDS7Any, TransportSize: TransportSizeByte, Count: 10, DBNumber: 7, Area: AreaDB, StartBit: 24},
		{Syntax: SyntaxIDS7Any, TransportSize: TransportSizeByte, Count: 1, Area: AreaMerkers, StartBit: 800},
	}
	if len(specs) != 2 || specs[0] != want[0] || specs[1] != want[1] {
		t.Fatalf("specs %+v", specs)
	}
}

func TestParseVarSpecsMalformed(t *testing.T) {
	item := []byte{0x12, 0x0A, SyntaxIDS7Any, 2, 0, 1, 0, 1, AreaDB, 0, 0, 0}
	for name, param := range map[string][]byte{
		"short":           {FuncReadVar},
		"wrong function":  append([]byte{FuncWriteVar, 1}, item...),
		"no items":        {FuncReadVar, 0},
		"count overruns":  append([]byte{FuncReadVar, 2}, item...),
		"bad spec type":   append([]byte{FuncReadVar, 1, 0x11}, item[1:]...),
		"length overruns": append([]byte{FuncReadVar, 1}, item[:6]...),
		"S7ANY too short": {FuncReadVar, 1, 0x12, 0x03, SyntaxIDS7Any, 2, 0},
		"trailing bytes":  append(append([]byte{FuncReadVar, 1}, item...), 0),
		"one-byte header": {FuncReadVar, 1, 0x12},
	} {
		if _, err := ParseReadVarRequest(param); !errors.Is(err, ErrMalformedRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// An item that is not S7ANY keeps its place, address fields zero.
	specs, err := ParseReadVarRequest([]byte{FuncReadVar, 2, 0x12, 0x00, 0x12, 0x02, SyntaxIDDBRead, 1})
	if err != nil || len(specs) != 2 || specs[0] != (VarSpec{}) || specs[1] != (VarSpec{Syntax: SyntaxIDDBRead}) {
		t.Fatalf("non-S7ANY items: %+v %v", specs, err)
	}
}

func TestWriteVarRequestRoundTrip(t *testing.T) {
	req, err := EncodeWriteVarRequest(4, S7AnyAddress{Area: AreaDB, DBNumber: 1, Start: 2, Size: 3}, []byte{7, 8, 9})
	if err != nil {
		t.Fatal(err)
	}
	h, body, _ := ParseS7Header(req)
	specs, items, err := ParseWriteVarRequest(body[:h.ParamLength], body[h.ParamLength:h.ParamLength+h.DataLength])
	if err != nil || len(specs) != 1 || !bytes.Equal(items[0].Data, []byte{7, 8, 9}) || items[0].TransportSize != DataTransportSizeByte {
		t.Fatalf("write: %+v %+v %v", specs, items, err)
	}
	bit, err := EncodeWriteVarBitRequest(4, S7AnyBitAddress{Area: AreaMerkers, ByteOffset: 1, BitOffset: 2}, true)
	if err != nil {
		t.Fatal(err)
	}
	h, body, _ = ParseS7Header(bit)
	specs, items, err = ParseWriteVarRequest(body[:h.ParamLength], body[h.ParamLength:h.ParamLength+h.DataLength])
	if err != nil || specs[0].TransportSize != TransportSizeBit || specs[0].StartBit != 10 || !bytes.Equal(items[0].Data, []byte{1}) {
		t.Fatalf("bit write: %+v %+v %v", specs, items, err)
	}
}

func TestParseWriteVarRequestMultiAndMalformed(t *testing.T) {
	item := []byte{0x12, 0x0A, SyntaxIDS7Any, 2, 0, 1, 0, 1, AreaDB, 0, 0, 0}
	two := append(append([]byte{FuncWriteVar, 2}, item...), item...)
	data := []byte{0, DataTransportSizeByte, 0, 8, 0xAB, 0, 0, DataTransportSizeOctet, 0, 1, 0xCD}
	_, items, err := ParseWriteVarRequest(two, data)
	if err != nil || !bytes.Equal(items[0].Data, []byte{0xAB}) || !bytes.Equal(items[1].Data, []byte{0xCD}) {
		t.Fatalf("two items: %+v %v", items, err)
	}
	one := append([]byte{FuncWriteVar, 1}, item...)
	for name, tc := range map[string]struct{ param, data []byte }{
		"bad param":       {[]byte{FuncReadVar, 1}, nil},
		"no header":       {one, []byte{0, 4}},
		"bad transport":   {one, []byte{0, 0x02, 0, 8, 1}},
		"partial bits":    {one, []byte{0, DataTransportSizeByte, 0, 7, 1}},
		"short payload":   {one, []byte{0, DataTransportSizeByte, 0, 16, 1}},
		"trailing":        {one, []byte{0, DataTransportSizeByte, 0, 8, 1, 0, 0}},
		"second missing":  {two, []byte{0, DataTransportSizeByte, 0, 8, 1, 0}},
		"int bits uneven": {one, []byte{0, DataTransportSizeInt, 0, 9, 1, 1}},
	} {
		if _, _, err := ParseWriteVarRequest(tc.param, tc.data); !errors.Is(err, ErrMalformedRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadVarResponseRoundTrip(t *testing.T) {
	items := []ReadVarResult{
		{ReturnCode: RetCodeSuccess, TransportSize: DataTransportSizeByte, RawLength: 24, Data: []byte{1, 2, 3}},
		{ReturnCode: RetCodeAddressFault},
		{ReturnCode: RetCodeSuccess, TransportSize: DataTransportSizeBit, RawLength: 1, Data: []byte{1}},
	}
	out, err := EncodeReadVarResponse(6, items)
	if err != nil {
		t.Fatal(err)
	}
	h, body, err := ParseS7Header(out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseReadVarResponse(body[:h.ParamLength], body[h.ParamLength:])
	if err != nil || len(got) != 3 || !bytes.Equal(got[0].Data, []byte{1, 2, 3}) || got[1].ReturnCode != RetCodeAddressFault || !bytes.Equal(got[2].Data, []byte{1}) {
		t.Fatalf("parsed %+v, %v", got, err)
	}
	if n := ReadVarResultSize(items[0], false); n != 8 {
		t.Errorf("size with fill = %d", n)
	}
	if n := ReadVarResultSize(items[0], true); n != 7 {
		t.Errorf("size of last = %d", n)
	}
	if _, err := EncodeReadVarResponse(1, make([]ReadVarResult, 256)); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("256 items: %v", err)
	}
}

func TestWriteVarResponseRoundTrip(t *testing.T) {
	out, err := EncodeWriteVarResponse(2, []byte{RetCodeSuccess})
	if err != nil {
		t.Fatal(err)
	}
	h, body, _ := ParseS7Header(out)
	if err := ParseWriteVarResponse(body[:h.ParamLength], body[h.ParamLength:]); err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeWriteVarResponse(1, make([]byte, 256)); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("256 codes: %v", err)
	}
	if _, err := EncodeAckData(1, 0, 0, make([]byte, 70000), nil); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("oversize param: %v", err)
	}
}

func FuzzParseReadVarRequest(f *testing.F) {
	req, _ := EncodeReadVarRequest(1, []S7AnyAddress{{Area: AreaDB, DBNumber: 1, Size: 4}})
	f.Add(req[S7HeaderSize:])
	f.Add([]byte{FuncReadVar, 2, 0x12, 0x00, 0x12, 0x02, SyntaxIDDBRead, 1})
	f.Fuzz(func(t *testing.T, param []byte) {
		specs, err := ParseReadVarRequest(param)
		if err == nil && (len(specs) == 0 || len(specs) > 255) {
			t.Fatalf("%d specs without error", len(specs))
		}
	})
}

func FuzzParseWriteVarRequest(f *testing.F) {
	req, _ := EncodeWriteVarRequest(1, S7AnyAddress{Area: AreaDB, DBNumber: 1, Size: 3}, []byte{1, 2, 3})
	f.Add(req[S7HeaderSize:S7HeaderSize+14], req[S7HeaderSize+14:])
	f.Fuzz(func(t *testing.T, param, data []byte) {
		specs, items, err := ParseWriteVarRequest(param, data)
		if err != nil {
			return
		}
		if len(specs) != len(items) {
			t.Fatalf("%d specs, %d items", len(specs), len(items))
		}
		total := 0
		for _, it := range items {
			total += 4 + len(it.Data)
		}
		if total > len(data) {
			t.Fatalf("items take %d bytes of %d", total, len(data))
		}
	})
}

func FuzzParseSetupCommRequest(f *testing.F) {
	f.Add([]byte{FuncSetupComm, 0, 0, 1, 0, 1, 1, 0xE0})
	f.Fuzz(func(t *testing.T, param []byte) {
		got, err := ParseSetupCommRequest(param)
		if err == nil && (got.PDUSize < 0 || got.PDUSize > 65535) {
			t.Fatalf("PDU size %d", got.PDUSize)
		}
	})
}
