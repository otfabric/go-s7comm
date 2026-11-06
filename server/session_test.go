// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"log/slog"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

func testSession(t *testing.T, pdu int) *session {
	t.Helper()
	srv, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	db1 := make([]byte, 16)
	copy(db1, []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x80})
	for _, err := range []error{
		srv.RegisterDB(1, db1, AreaOptions{}),
		srv.RegisterDB(2, []byte{0xAA, 0xBB}, AreaOptions{ReadOnly: true}),
		srv.RegisterDB(3, make([]byte, 600), AreaOptions{}),
		srv.RegisterArea(AreaMerkers, make([]byte, 8), AreaOptions{}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return &session{srv: srv, pduSize: pdu, log: slog.New(slog.DiscardHandler)}
}

// job builds a job PDU.
func job(ref uint16, param, data []byte) []byte {
	return append(append(must(wire.EncodeS7Header(wire.ROSCTRJob, ref, len(param), len(data))), param...), data...)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// any12 builds one S7ANY item.
func any12(transport byte, count, db uint16, area byte, startBit uint32) []byte {
	b := []byte{0x12, 0x0A, wire.SyntaxIDS7Any, transport, 0, 0, 0, 0, area, byte(startBit >> 16), byte(startBit >> 8), byte(startBit)}
	binary.BigEndian.PutUint16(b[4:6], count)
	binary.BigEndian.PutUint16(b[6:8], db)
	return b
}

func readJob(items ...[]byte) []byte {
	param := []byte{wire.FuncReadVar, byte(len(items))}
	for _, it := range items {
		param = append(param, it...)
	}
	return job(7, param, nil)
}

// answer runs req and returns the Ack-Data parameter and data sections.
func answer(t *testing.T, s *session, req []byte) ([]byte, []byte) {
	t.Helper()
	out, keep := s.handle(req)
	if !keep || out == nil {
		t.Fatalf("handle: keep=%v out=%x", keep, out)
	}
	h, body, err := wire.ParseS7Header(out)
	if err != nil {
		t.Fatalf("answer header: %v", err)
	}
	if h.ROSCTR != wire.ROSCTRAckData || h.PDURef != binary.BigEndian.Uint16(req[4:6]) || h.ErrorClass != 0 || h.ErrorCode != 0 {
		t.Fatalf("answer header %+v", h)
	}
	return body[:h.ParamLength], body[h.ParamLength : h.ParamLength+h.DataLength]
}

func TestSetupClampsAndEchoes(t *testing.T) {
	s := testSession(t, 480)
	for _, tc := range []struct{ ask, want int }{{0, 240}, {239, 240}, {480, 480}, {65535, 480}} {
		param, _ := answer(t, s, must(wire.EncodeSetupCommRequest(3, 2, 5, tc.ask)))
		got, err := wire.ParseSetupCommResponse(param)
		if err != nil {
			t.Fatal(err)
		}
		if got.PDUSize != tc.want || got.MaxAmqCalling != 2 || got.MaxAmqCalled != 5 || s.pduSize != tc.want {
			t.Errorf("ask %d: %+v, session %d", tc.ask, got, s.pduSize)
		}
	}
}

func TestReadTransportSizesAndCodes(t *testing.T) {
	s := testSession(t, 480)
	_, data := answer(t, s, readJob(
		any12(wire.TransportSizeByte, 3, 1, wire.AreaDB, 0),     // bytes 01 02 03 + fill
		any12(wire.TransportSizeBit, 1, 1, wire.AreaDB, 7*8+7),  // bit 7 of 0x80
		any12(wire.TransportSizeInt, 1, 1, wire.AreaDB, 2*8),    // INT 03 04
		any12(wire.TransportSizeReal, 1, 1, wire.AreaDB, 4*8),   // REAL 05 06 07 80
		any12(wire.TransportSizeChar, 1, 1, wire.AreaDB, 0),     // CHAR 01 + fill
		any12(wire.TransportSizeByte, 1, 9, wire.AreaDB, 0),     // unknown DB
		any12(wire.TransportSizeByte, 1, 0, wire.AreaInputs, 0), // area not registered
		any12(wire.TransportSizeByte, 17, 1, wire.AreaDB, 0),    // past the end
		any12(wire.TransportSizeByte, 1, 1, wire.AreaDB, 3),     // unaligned
		any12(wire.TransportSizeBit, 2, 1, wire.AreaDB, 0),      // two bits
		any12(0x1D, 1, 1, wire.AreaDB, 0),                       // timer transport
		any12(wire.TransportSizeDWord, 1, 0, wire.AreaMerkers, 0),
	))
	want := []byte{
		0xFF, 0x04, 0x00, 0x18, 0x01, 0x02, 0x03, 0x00,
		0xFF, 0x03, 0x00, 0x01, 0x01, 0x00,
		0xFF, 0x05, 0x00, 0x10, 0x03, 0x04,
		0xFF, 0x07, 0x00, 0x04, 0x05, 0x06, 0x07, 0x80,
		0xFF, 0x09, 0x00, 0x01, 0x01, 0x00,
		0x0A, 0x00, 0x00, 0x00,
		0x0A, 0x00, 0x00, 0x00,
		0x05, 0x00, 0x00, 0x00,
		0x05, 0x00, 0x00, 0x00,
		0x05, 0x00, 0x00, 0x00,
		0x05, 0x00, 0x00, 0x00,
		0xFF, 0x04, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00,
	}
	if !bytes.Equal(data, want) {
		t.Errorf("data\n got %x\nwant %x", data, want)
	}
}

func TestReadNonS7AnyItem(t *testing.T) {
	s := testSession(t, 480)
	item := []byte{0x12, 0x07, wire.SyntaxIDDBRead, 1, 2, 3, 4, 5, 6}
	param, data := answer(t, s, job(1, append([]byte{wire.FuncReadVar, 2}, append(item, any12(wire.TransportSizeByte, 1, 1, wire.AreaDB, 0)...)...), nil))
	if param[1] != 2 || !bytes.Equal(data, []byte{0x0A, 0, 0, 0, 0xFF, 0x04, 0x00, 0x08, 0x01}) {
		t.Errorf("param %x data %x", param, data)
	}
}

func TestReadOverThePDU(t *testing.T) {
	s := testSession(t, 240)
	// 12 + 2 + 4 + 200 = 218 fits; the second item does not.
	_, data := answer(t, s, readJob(
		any12(wire.TransportSizeByte, 200, 3, wire.AreaDB, 0),
		any12(wire.TransportSizeByte, 100, 3, wire.AreaDB, 0),
		any12(wire.TransportSizeByte, 1, 1, wire.AreaDB, 0),
	))
	if len(data) != 4+200+4+5 || data[204] != dataOverPDU || data[208] != 0xFF {
		t.Errorf("data len %d: %x", len(data), data[200:])
	}
}

func TestReadOfManyDWordsIsRefusedNotTruncated(t *testing.T) {
	s := testSession(t, 240)
	_, data := answer(t, s, readJob(any12(wire.TransportSizeDWord, 150, 3, wire.AreaDB, 0)))
	if !bytes.Equal(data, []byte{dataOverPDU, 0, 0, 0}) {
		t.Errorf("data %x", data)
	}
	if lengthField(-1) != 0xFFFF || lengthField(70000) != 0xFFFF || lengthField(8) != 8 {
		t.Error("lengthField bounds")
	}
}

func TestReadDropsItemsPastTwenty(t *testing.T) {
	s := testSession(t, 480)
	items := make([][]byte, 25)
	for i := range items {
		items[i] = any12(wire.TransportSizeByte, 2, 1, wire.AreaDB, 0)
	}
	param, data := answer(t, s, readJob(items...))
	if param[1] != 20 || len(data) != 20*6 {
		t.Errorf("items %d, data %d", param[1], len(data))
	}
}

func writeJob(items ...[]byte) []byte {
	var param, data []byte
	param = []byte{wire.FuncWriteVar, byte(len(items) / 2)}
	for i := 0; i < len(items); i += 2 {
		param = append(param, items[i]...)
		d := items[i+1]
		if i+2 < len(items) && len(d)%2 != 0 {
			d = append(d, 0)
		}
		data = append(data, d...)
	}
	return job(9, param, data)
}

func dataItem(transport byte, raw uint16, payload ...byte) []byte {
	return append([]byte{0, transport, byte(raw >> 8), byte(raw)}, payload...)
}

func TestWriteCodes(t *testing.T) {
	s := testSession(t, 480)
	_, data := answer(t, s, writeJob(
		any12(wire.TransportSizeByte, 1, 1, wire.AreaDB, 8*8), dataItem(wire.DataTransportSizeByte, 8, 0x11),
		any12(wire.TransportSizeBit, 1, 1, wire.AreaDB, 9*8+3), dataItem(wire.DataTransportSizeBit, 1, 0x01),
		any12(wire.TransportSizeWord, 1, 1, wire.AreaDB, 10*8), dataItem(wire.DataTransportSizeOctet, 2, 0x22, 0x33),
		any12(wire.TransportSizeByte, 1, 2, wire.AreaDB, 0), dataItem(wire.DataTransportSizeByte, 8, 0x00),
		any12(wire.TransportSizeByte, 1, 9, wire.AreaDB, 0), dataItem(wire.DataTransportSizeByte, 8, 0x00),
		any12(wire.TransportSizeByte, 2, 1, wire.AreaDB, 15*8), dataItem(wire.DataTransportSizeByte, 16, 0, 0),
		any12(wire.TransportSizeByte, 2, 1, wire.AreaDB, 0), dataItem(wire.DataTransportSizeByte, 8, 0),
		any12(0x1C, 1, 1, wire.AreaDB, 0), dataItem(wire.DataTransportSizeOctet, 2, 0, 0),
		any12(wire.TransportSizeBit, 1, 1, wire.AreaDB, 9*8+4), dataItem(wire.DataTransportSizeBit, 1, 0x00),
	))
	want := []byte{0xFF, 0xFF, 0xFF, 0x0A, 0x0A, 0x05, 0x07, 0x06, 0xFF}
	if !bytes.Equal(data, want) {
		t.Errorf("codes %x, want %x", data, want)
	}
	got, _ := s.srv.Read(AreaDB, 1, 8, 4)
	if !bytes.Equal(got, []byte{0x11, 0x08, 0x22, 0x33}) {
		t.Errorf("memory %x", got)
	}
	ro, _ := s.srv.Read(AreaDB, 2, 0, 1)
	if ro[0] != 0xAA {
		t.Errorf("read-only DB2 changed: %x", ro)
	}
}

func TestUnansweredAndMalformed(t *testing.T) {
	s := testSession(t, 480)
	ud := append(must(wire.EncodeS7Header(wire.ROSCTRUserdata, 1, 2, 0)), 0x00, 0x01)
	for name, tc := range map[string]struct {
		req  []byte
		keep bool
	}{
		"user data":            {ud, true},
		"PLC stop":             {job(1, []byte{wire.FuncPLCStop, 0}, nil), true},
		"short":                {[]byte{0x32, 0x01}, false},
		"wrong protocol":       {append([]byte{0x72}, job(1, []byte{0x04, 0}, nil)[1:]...), false},
		"ack from client":      {append([]byte{0x32, 0x02}, job(1, []byte{0x04, 0}, nil)[2:]...), false},
		"length mismatch":      {append(job(1, []byte{wire.FuncReadVar, 1}, nil), 0x00), false},
		"no parameter":         {job(1, nil, nil), false},
		"read without items":   {job(1, []byte{wire.FuncReadVar, 0}, nil), false},
		"setup short":          {job(1, []byte{wire.FuncSetupComm, 0, 0}, nil), false},
		"write data truncated": {job(1, append([]byte{wire.FuncWriteVar, 1}, any12(2, 1, 1, wire.AreaDB, 0)...), []byte{0, 4}), false},
	} {
		out, keep := s.handle(tc.req)
		if out != nil || keep != tc.keep {
			t.Errorf("%s: out=%x keep=%v, want nil and %v", name, out, keep, tc.keep)
		}
	}
	items := make([][]byte, 0, 42)
	for range 21 {
		items = append(items, any12(wire.TransportSizeByte, 1, 1, wire.AreaDB, 0), dataItem(wire.DataTransportSizeByte, 8, 0))
	}
	if out, keep := s.handle(writeJob(items...)); out != nil || keep {
		t.Errorf("21-item write: out=%x keep=%v", out, keep)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, cfg := range []Config{
		{MaxPDUSize: 239}, {MaxPDUSize: 4097}, {MaxConnections: -1}, {HandshakeTimeout: -1}, {IdleTimeout: -1},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}

func TestMemoryAPIErrors(t *testing.T) {
	srv, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.RegisterDB(1, []byte{1, 2}, AreaOptions{}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"duplicate DB":     {srv.RegisterDB(1, []byte{1}, AreaOptions{}), ErrAreaExists},
		"DB 0":             {srv.RegisterDB(0, []byte{1}, AreaOptions{}), ErrUnknownArea},
		"DB 65536":         {srv.RegisterDB(65536, []byte{1}, AreaOptions{}), ErrUnknownArea},
		"empty":            {srv.RegisterDB(2, nil, AreaOptions{}), ErrOutOfRange},
		"too large":        {srv.RegisterArea(AreaMerkers, make([]byte, MaxAreaSize+1), AreaOptions{}), ErrOutOfRange},
		"DB via area":      {srv.RegisterArea(AreaDB, []byte{1}, AreaOptions{}), ErrUnknownArea},
		"timer area":       {srv.RegisterArea(Area(wire.AreaTimer), []byte{1}, AreaOptions{}), ErrUnknownArea},
		"read unknown":     {readErr(srv.Read(AreaMerkers, 0, 0, 1)), ErrUnknownArea},
		"read past end":    {readErr(srv.Read(AreaDB, 1, 1, 2)), ErrOutOfRange},
		"read negative":    {readErr(srv.Read(AreaDB, 1, -1, 1)), ErrOutOfRange},
		"write past end":   {srv.Write(AreaDB, 1, 2, []byte{1}), ErrOutOfRange},
		"write unknown DB": {srv.Write(AreaDB, 5, 0, []byte{1}), ErrUnknownArea},
	} {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("%s: %v, want %v", name, tc.err, tc.want)
		}
	}
	if AreaDB.String() != "DB" || AreaMerkers.String() != "M" {
		t.Error("Area.String")
	}
}

func readErr(_ []byte, err error) error { return err }
