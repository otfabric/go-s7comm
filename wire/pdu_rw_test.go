package wire

import (
	"errors"
	"testing"
)

func TestEncodeReadVarRequest(t *testing.T) {
	addrs := []S7AnyAddress{{Area: AreaDB, DBNumber: 1, Start: 0, Size: 4}}
	msg := EncodeReadVarRequest(1, addrs)
	if len(msg) < 14 {
		t.Fatalf("unexpected message length: %d", len(msg))
	}
	if msg[10] != FuncReadVar {
		t.Fatalf("expected read var function at parameter start")
	}
}

func TestNormalizeResponseDataLength(t *testing.T) {
	tests := []struct {
		transportSize ResponseTransportSize
		rawLength     uint16
		wantBytes     int
		wantErr       bool
	}{
		{RespTransportSizeBit, 1, 1, false},
		{RespTransportSizeBit, 8, 1, false},
		{RespTransportSizeByte, 4, 4, false},
		{RespTransportSizeWord, 16, 2, false},
		{RespTransportSizeDWord, 32, 4, false},
		{RespTransportSizeReal, 32, 4, false},
		{RespTransportSizeDATE, 16, 2, false},
		{RespTransportSizeTOD, 32, 4, false},
		{RespTransportSizeTIME, 32, 4, false},
		{RespTransportSizeS5TIME, 16, 2, false},
		{RespTransportSizeDT, 64, 8, false},
		{RespTransportSizeCount, 16, 2, false},
		{RespTransportSizeTimer, 16, 2, false},
		{RespTransportSizeIECCount, 16, 2, false},
		{RespTransportSizeIECTimer, 16, 2, false},
		{RespTransportSizeHSCounter, 16, 2, false},
		{ResponseTransportSize(0xFF), 10, 0, true},
	}
	for _, tt := range tests {
		got, err := NormalizeResponseDataLength(tt.transportSize, tt.rawLength)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NormalizeResponseDataLength(0x%02X, %d) expected error", tt.transportSize, tt.rawLength)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeResponseDataLength(0x%02X, %d): %v", tt.transportSize, tt.rawLength, err)
			continue
		}
		if got != tt.wantBytes {
			t.Errorf("NormalizeResponseDataLength(0x%02X, %d) = %d bytes, want %d", tt.transportSize, tt.rawLength, got, tt.wantBytes)
		}
	}
}

func TestParseReadVarResponse(t *testing.T) {
	param := []byte{FuncReadVar, 0x01}
	data := []byte{RetCodeSuccess, 0x04, 0x00, 0x10, 0x12, 0x34}
	items, err := ParseReadVarResponse(param, data)
	if err != nil {
		t.Fatalf("ParseReadVarResponse error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	// Truncated item header
	_, err = ParseReadVarResponse([]byte{FuncReadVar, 1}, []byte{0, 0x04, 0})
	if err == nil {
		t.Fatal("expected error for truncated item header")
	}
	// Item data overrun (declare 4-byte item, only 5 bytes in buffer)
	_, err = ParseReadVarResponse([]byte{FuncReadVar, 1}, []byte{RetCodeSuccess, 0x04, 0x00, 0x20, 0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for item data overrun")
	}
	// Unknown transport size
	_, err = ParseReadVarResponse([]byte{FuncReadVar, 1}, []byte{RetCodeSuccess, 0xFF, 0x00, 0x10})
	if err == nil {
		t.Fatal("expected error for unknown transport size")
	}
	// Truncated item header (structured error)
	_, err = ParseReadVarResponse([]byte{FuncReadVar, 1}, []byte{0, 0x04})
	if err == nil {
		t.Fatal("expected error for truncated item header")
	}
	if !errors.Is(err, ErrTruncatedItemHeader) {
		t.Errorf("expected ErrTruncatedItemHeader, got %v", err)
	}
}

func TestReadVarItem_RawAndNormalizedPreserved(t *testing.T) {
	param := []byte{FuncReadVar, 1}
	data := []byte{RetCodeSuccess, 0x04, 0x00, 0x10, 0x12, 0x34}
	items, err := ParseReadVarResponse(param, data)
	if err != nil {
		t.Fatalf("ParseReadVarResponse: %v", err)
	}
	if len(items) != 1 {
		t.Fatal("expected 1 item")
	}
	it := items[0]
	if it.RawTransportSize != 0x04 || it.RawLength != 0x10 {
		t.Errorf("raw: transportSize=0x%02X rawLength=%d", it.RawTransportSize, it.RawLength)
	}
	if len(it.Data) != 2 || it.Data[0] != 0x12 || it.Data[1] != 0x34 {
		t.Errorf("normalized Data: %v", it.Data)
	}
}

func TestDecodeAsWordAndReal(t *testing.T) {
	// Success item: 2 bytes
	item := ReadVarItem{ReturnCode: RetCodeSuccess, Data: []byte{0x12, 0x34}}
	w, err := DecodeAsWord(item)
	if err != nil {
		t.Fatalf("DecodeAsWord: %v", err)
	}
	if w != 0x1234 {
		t.Errorf("DecodeAsWord = 0x%04X, want 0x1234", w)
	}
	// Non-success: no decode
	itemFail := ReadVarItem{ReturnCode: RetCodeAccessFault, Data: []byte{0x12, 0x34}}
	_, err = DecodeAsWord(itemFail)
	if err == nil {
		t.Fatal("DecodeAsWord on failed item expected error")
	}
	// REAL: 4 bytes
	itemReal := ReadVarItem{ReturnCode: RetCodeSuccess, Data: []byte{0x40, 0x49, 0x0F, 0xDB}}
	r, err := DecodeAsReal(itemReal)
	if err != nil {
		t.Fatalf("DecodeAsReal: %v", err)
	}
	if r < 3.14-0.01 || r > 3.14+0.01 {
		t.Errorf("DecodeAsReal = %f, want ~3.14", r)
	}
}

func TestParseReadVarResponse_MultiItemWithFillByte(t *testing.T) {
	// Two items: first item 1 byte payload (odd) + fill byte, second item 2 bytes.
	// Item1: retCode(1) + ts(1) + len(2) + payload(1) + fill(1) = 6 bytes
	// Item2: retCode(1) + ts(1) + len(2) + payload(2) = 6 bytes. Total 12 bytes.
	param := []byte{FuncReadVar, 0x02}
	data := []byte{
		RetCodeSuccess, 0x01, 0x00, 0x01, 0xAB, 0x00, // 1 bit = 1 byte, fill
		RetCodeSuccess, 0x04, 0x00, 0x10, 0x12, 0x34, // 16 bits = 2 bytes
	}
	items, err := ParseReadVarResponse(param, data)
	if err != nil {
		t.Fatalf("ParseReadVarResponse multi-item: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if len(items[0].Data) != 1 || items[0].Data[0] != 0xAB {
		t.Errorf("item0: want [0xAB], got %v", items[0].Data)
	}
	if len(items[1].Data) != 2 || items[1].Data[0] != 0x12 || items[1].Data[1] != 0x34 {
		t.Errorf("item1: want [0x12, 0x34], got %v", items[1].Data)
	}
}

func TestValidateRequestSyntax(t *testing.T) {
	if err := ValidateRequestSyntax(SyntaxIDS7Any); err != nil {
		t.Errorf("ValidateRequestSyntax(S7Any): %v", err)
	}
	for _, syntax := range []byte{SyntaxIDDBRead, SyntaxID1200Symbolic, SyntaxIDDriveES, 0x82, 0x83, 0x84} {
		err := ValidateRequestSyntax(syntax)
		if err == nil {
			t.Errorf("ValidateRequestSyntax(0x%02X): expected error", syntax)
		}
		var unsup *UnsupportedSyntaxError
		if !errors.As(err, &unsup) || unsup.RawSyntaxID != syntax {
			t.Errorf("ValidateRequestSyntax(0x%02X): got %v", syntax, err)
		}
	}
}

func TestValidateArea(t *testing.T) {
	for _, area := range []byte{AreaDataRecord, AreaInputs, AreaOutputs, AreaMerkers, AreaDB, AreaDI, AreaCounter, AreaTimer, AreaIECCounter200, AreaIECTimer200, AreaPeripheral, AreaSysInfo, AreaSysFlags, AreaS7200AN, AreaS7200AO} {
		if err := ValidateArea(area); err != nil {
			t.Errorf("ValidateArea(0x%02X): %v", area, err)
		}
	}
	if err := ValidateArea(0xFF); err == nil {
		t.Error("ValidateArea(0xFF): expected error")
	}
}

func TestAreaString(t *testing.T) {
	if got := AreaString(AreaInputs); got != "I" {
		t.Errorf("AreaString(I): got %q", got)
	}
	if got := AreaString(AreaDB); got != "DB" {
		t.Errorf("AreaString(DB): got %q", got)
	}
	if got := AreaString(0xFF); got != "0xFF" {
		t.Errorf("AreaString(unknown): got %q", got)
	}
}

func TestSyntaxIDString(t *testing.T) {
	if got := SyntaxIDString(SyntaxIDS7Any); got != "S7ANY" {
		t.Errorf("SyntaxIDString(S7ANY): got %q", got)
	}
	if got := SyntaxIDString(SyntaxIDDBRead); got != "DBREAD" {
		t.Errorf("SyntaxIDString(DBREAD): got %q", got)
	}
	if got := SyntaxIDString(0x99); got != "0x99" {
		t.Errorf("SyntaxIDString(unknown): got %q", got)
	}
}

func TestResponseTransportSize_String(t *testing.T) {
	if got := RespTransportSizeByte.String(); got != "BYTE" {
		t.Errorf("RespTransportSizeByte.String(): got %q", got)
	}
	if got := RespTransportSizeDATE.String(); got != "DATE" {
		t.Errorf("RespTransportSizeDATE.String(): got %q", got)
	}
	if got := RespTransportSizeIECCount.String(); got != "IEC_COUNTER" {
		t.Errorf("RespTransportSizeIECCount.String(): got %q", got)
	}
	if got := ResponseTransportSize(0xFE).String(); got != "0xFE" {
		t.Errorf("unknown transport size: got %q", got)
	}
}

func TestParseReadVarResponse_NonSuccessZeroTransportSize(t *testing.T) {
	// Snap7-style rejected item: return code + transport 0x00 + length 0.
	param := []byte{FuncReadVar, 0x01}
	data := []byte{RetCodeAddressFault, 0x00, 0x00, 0x00}
	items, err := ParseReadVarResponse(param, data)
	if err != nil {
		t.Fatalf("ParseReadVarResponse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ReturnCode != RetCodeAddressFault {
		t.Fatalf("ReturnCode=0x%02X, want address fault", items[0].ReturnCode)
	}
	if len(items[0].Data) != 0 {
		t.Fatalf("expected empty Data, got %v", items[0].Data)
	}
}

func TestParseReadVarResponse_MixedItemReturnCodes(t *testing.T) {
	// Two items: first success, second access fault. Header has no error; item-level return codes.
	param := []byte{FuncReadVar, 0x02}
	data := []byte{
		RetCodeSuccess, 0x04, 0x00, 0x10, 0x11, 0x22,
		RetCodeAccessFault, 0x04, 0x00, 0x10, 0x00, 0x00,
	}
	items, err := ParseReadVarResponse(param, data)
	if err != nil {
		t.Fatalf("ParseReadVarResponse mixed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ReturnCode != RetCodeSuccess || len(items[0].Data) != 2 {
		t.Errorf("item0: ReturnCode=%02X Data len=%d", items[0].ReturnCode, len(items[0].Data))
	}
	if items[1].ReturnCode != RetCodeAccessFault {
		t.Errorf("item1: ReturnCode=0x%02X, want access fault", items[1].ReturnCode)
	}
}

func TestParseReadVarResponse_TwoItemNoPadding(t *testing.T) {
	// Two items, both even length: 2 bytes + 2 bytes
	param := []byte{FuncReadVar, 0x02}
	data := []byte{
		RetCodeSuccess, 0x04, 0x00, 0x10, 0x11, 0x22,
		RetCodeSuccess, 0x04, 0x00, 0x10, 0x33, 0x44,
	}
	items, err := ParseReadVarResponse(param, data)
	if err != nil {
		t.Fatalf("ParseReadVarResponse two-item no padding: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if len(items[0].Data) != 2 || items[0].Data[0] != 0x11 || items[0].Data[1] != 0x22 {
		t.Errorf("item0: got %v", items[0].Data)
	}
	if len(items[1].Data) != 2 || items[1].Data[0] != 0x33 || items[1].Data[1] != 0x44 {
		t.Errorf("item1: got %v", items[1].Data)
	}
}

func TestParseWriteVarResponse(t *testing.T) {
	if err := ParseWriteVarResponse([]byte{FuncWriteVar, 1}, []byte{RetCodeSuccess}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if err := ParseWriteVarResponse([]byte{FuncWriteVar}, []byte{RetCodeSuccess}); err == nil {
		t.Fatal("expected error for param too short")
	}
	if err := ParseWriteVarResponse([]byte{FuncReadVar, 1}, []byte{RetCodeSuccess}); err == nil {
		t.Fatal("expected error for wrong function")
	}
	if err := ParseWriteVarResponse([]byte{FuncWriteVar, 1}, nil); err == nil {
		t.Fatal("expected error for data too short")
	}
	if err := ParseWriteVarResponse([]byte{FuncWriteVar, 1}, []byte{RetCodeAccessFault}); err == nil {
		t.Fatal("expected error for non-success return code")
	}
}

func TestEncodeWriteVarRequest(t *testing.T) {
	addr := S7AnyAddress{Area: AreaDB, DBNumber: 1, Start: 0, Size: 4}
	value := []byte{0x01, 0x02, 0x03, 0x04}
	msg := EncodeWriteVarRequest(1, addr, value)
	if len(msg) < 14 {
		t.Fatalf("unexpected message length: %d", len(msg))
	}
	if msg[10] != FuncWriteVar {
		t.Fatalf("expected write var function at parameter start")
	}
	// Odd-length value gets padded
	msg2 := EncodeWriteVarRequest(1, addr, []byte{0x01, 0x02, 0x03})
	if len(msg2)%2 != 0 {
		t.Fatalf("expected even length for padded message, got %d", len(msg2))
	}
}

func TestEncodeS7AnyBit(t *testing.T) {
	cases := []struct {
		name      string
		addr      S7AnyBitAddress
		wantStart int
		wantDB    uint16
	}{
		{"bit0", S7AnyBitAddress{Area: AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0}, 0, 1},
		{"bit7", S7AnyBitAddress{Area: AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 7}, 7, 1},
		{"byte10_bit3", S7AnyBitAddress{Area: AreaDB, DBNumber: 5, ByteOffset: 10, BitOffset: 3}, 83, 5},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			b := EncodeS7AnyBit(tt.addr)
			if len(b) != 12 {
				t.Fatalf("len=%d, want 12", len(b))
			}
			if b[3] != TransportSizeBit {
				t.Fatalf("transport=0x%02X, want BIT", b[3])
			}
			if b[4] != 0 || b[5] != 1 {
				t.Fatalf("element count=%d, want 1", int(b[4])<<8|int(b[5]))
			}
			gotDB := uint16(b[6])<<8 | uint16(b[7])
			if gotDB != tt.wantDB {
				t.Fatalf("DB=%d, want %d", gotDB, tt.wantDB)
			}
			if b[8] != AreaDB {
				t.Fatalf("area=0x%02X", b[8])
			}
			gotStart := int(b[9])<<16 | int(b[10])<<8 | int(b[11])
			if gotStart != tt.wantStart {
				t.Fatalf("start=%d, want %d", gotStart, tt.wantStart)
			}
		})
	}
}

func TestEncodeReadVarBitRequest(t *testing.T) {
	addr := S7AnyBitAddress{Area: AreaDB, DBNumber: 1, ByteOffset: 10, BitOffset: 3}
	msg := EncodeReadVarBitRequest(7, addr)
	if msg[10] != FuncReadVar {
		t.Fatalf("function=0x%02X", msg[10])
	}
	if msg[11] != 1 {
		t.Fatalf("item count=%d", msg[11])
	}
	spec := msg[12:24]
	want := EncodeS7AnyBit(addr)
	for i := range want {
		if spec[i] != want[i] {
			t.Fatalf("S7ANY mismatch at %d: got %v want %v", i, spec, want)
		}
	}
}

func TestEncodeWriteVarBitRequest(t *testing.T) {
	addr := S7AnyBitAddress{Area: AreaDB, DBNumber: 1, ByteOffset: 0, BitOffset: 0}
	for _, value := range []bool{false, true} {
		msg := EncodeWriteVarBitRequest(1, addr, value)
		if msg[10] != FuncWriteVar {
			t.Fatalf("value=%v function=0x%02X", value, msg[10])
		}
		// data section starts after header(10)+param(2+12=14) = 24
		data := msg[24:]
		if len(data) < 5 {
			t.Fatalf("value=%v data too short: %d", value, len(data))
		}
		if data[0] != 0x00 || data[1] != DataTransportSizeBit || data[2] != 0x00 || data[3] != 0x01 {
			t.Fatalf("value=%v data header=%v", value, data[:4])
		}
		wantPayload := byte(0)
		if value {
			wantPayload = 1
		}
		if data[4] != wantPayload {
			t.Fatalf("value=%v payload=0x%02X, want 0x%02X", value, data[4], wantPayload)
		}
		if len(data)%2 != 0 {
			t.Fatalf("value=%v data len not even: %d", value, len(data))
		}
	}
}

func TestDecodeAsBit(t *testing.T) {
	okFalse := ReadVarItem{ReturnCode: RetCodeSuccess, RawTransportSize: DataTransportSizeBit, RawLength: 1, Data: []byte{0x00}}
	v, err := DecodeAsBit(okFalse)
	if err != nil || v {
		t.Fatalf("false: got %v err=%v", v, err)
	}
	okTrue := ReadVarItem{ReturnCode: RetCodeSuccess, RawTransportSize: DataTransportSizeBit, RawLength: 1, Data: []byte{0x01}}
	v, err = DecodeAsBit(okTrue)
	if err != nil || !v {
		t.Fatalf("true: got %v err=%v", v, err)
	}
	okNonZero := ReadVarItem{ReturnCode: RetCodeSuccess, RawTransportSize: DataTransportSizeBit, RawLength: 1, Data: []byte{0x03}}
	v, err = DecodeAsBit(okNonZero)
	if err != nil || !v {
		t.Fatalf("nonzero: got %v err=%v", v, err)
	}
	if _, err := DecodeAsBit(ReadVarItem{ReturnCode: RetCodeAddressFault, RawTransportSize: 0, RawLength: 0}); err == nil {
		t.Fatal("expected error for rejected item")
	}
	if _, err := DecodeAsBit(ReadVarItem{ReturnCode: RetCodeSuccess, RawTransportSize: byte(RespTransportSizeByte), RawLength: 1, Data: []byte{0x01}}); err == nil {
		t.Fatal("expected error for BYTE transport")
	}
	if _, err := DecodeAsBit(ReadVarItem{ReturnCode: RetCodeSuccess, RawTransportSize: byte(RespTransportSizeBit), RawLength: 1, Data: []byte{0x01}}); err == nil {
		t.Fatal("expected error for request-style 0x01 transport in data section")
	}
	if _, err := DecodeAsBit(ReadVarItem{ReturnCode: RetCodeSuccess, RawTransportSize: DataTransportSizeBit, RawLength: 1, Data: nil}); err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestParseReadVarResponse_BitItem(t *testing.T) {
	param := []byte{FuncReadVar, 1}
	data := []byte{RetCodeSuccess, DataTransportSizeBit, 0x00, 0x01, 0x01}
	items, err := ParseReadVarResponse(param, data)
	if err != nil {
		t.Fatalf("ParseReadVarResponse: %v", err)
	}
	v, err := DecodeAsBit(items[0])
	if err != nil || !v {
		t.Fatalf("DecodeAsBit: %v %v", v, err)
	}
}

func BenchmarkParseReadVarResponse(b *testing.B) {
	param := []byte{FuncReadVar, 0x01}
	data := []byte{RetCodeSuccess, 0x04, 0x00, 0x10, 0x12, 0x34, 0x56, 0x78}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseReadVarResponse(param, data)
	}
}

func FuzzParseReadVarResponse(f *testing.F) {
	f.Add([]byte{FuncReadVar, 1}, []byte{RetCodeSuccess, 0x04, 0x00, 0x10, 0x12, 0x34})
	f.Fuzz(func(t *testing.T, param, data []byte) {
		_, _ = ParseReadVarResponse(param, data)
	})
}

func FuzzNormalizeResponseDataLength(f *testing.F) {
	f.Add(byte(RespTransportSizeWord), uint16(16))
	f.Add(byte(RespTransportSizeByte), uint16(4))
	f.Fuzz(func(t *testing.T, transportSize byte, rawLength uint16) {
		_, _ = NormalizeResponseDataLength(ResponseTransportSize(transportSize), rawLength)
	})
}
