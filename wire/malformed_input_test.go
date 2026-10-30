// SPDX-License-Identifier: MIT

package wire

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestValidateRequestSyntax_ReportsRawID(t *testing.T) {
	if err := ValidateRequestSyntax(SyntaxIDS7Any); err != nil {
		t.Fatalf("S7ANY rejected: %v", err)
	}
	for _, syntax := range []byte{SyntaxIDDBRead, SyntaxID1200Symbolic, SyntaxIDDriveES, 0x82, 0x83, 0x84, 0x00, 0xFF} {
		err := ValidateRequestSyntax(syntax)
		var unsupported *UnsupportedSyntaxError
		if !errors.As(err, &unsupported) {
			t.Fatalf("syntax 0x%02X: err = %v, want UnsupportedSyntaxError", syntax, err)
		}
		if unsupported.RawSyntaxID != syntax {
			t.Fatalf("RawSyntaxID = 0x%02X, want 0x%02X", unsupported.RawSyntaxID, syntax)
		}
		if want := "0x" + hexByte(syntax); !strings.Contains(err.Error(), want) {
			t.Fatalf("Error() = %q, want it to name %s", err, want)
		}
	}
}

func TestDecodeTypedItems(t *testing.T) {
	ok := func(data ...byte) ReadVarItem { return ReadVarItem{ReturnCode: RetCodeSuccess, Data: data} }

	t.Run("byte", func(t *testing.T) {
		if v, err := DecodeAsByte(ok(0x7F, 0x01)); err != nil || v != 0x7F {
			t.Fatalf("got 0x%02X, %v", v, err)
		}
	})
	t.Run("dword is big-endian", func(t *testing.T) {
		if v, err := DecodeAsDWord(ok(0x01, 0x02, 0x03, 0x04)); err != nil || v != 0x01020304 {
			t.Fatalf("got 0x%08X, %v", v, err)
		}
	})
	t.Run("int keeps sign", func(t *testing.T) {
		if v, err := DecodeAsInt(ok(0xFF, 0xFE)); err != nil || v != -2 {
			t.Fatalf("got %d, %v", v, err)
		}
	})
	t.Run("dint keeps sign", func(t *testing.T) {
		if v, err := DecodeAsDInt(ok(0xFF, 0xFF, 0xFF, 0xFD)); err != nil || v != -3 {
			t.Fatalf("got %d, %v", v, err)
		}
	})
	t.Run("real", func(t *testing.T) {
		v, err := DecodeAsReal(ok(0x40, 0x49, 0x0F, 0xDB))
		if err != nil || math.Abs(float64(v)-math.Pi) > 1e-6 {
			t.Fatalf("got %v, %v", v, err)
		}
	})

	decoders := map[string]struct {
		need   int
		decode func(ReadVarItem) error
	}{
		"byte":  {1, func(i ReadVarItem) error { _, err := DecodeAsByte(i); return err }},
		"word":  {2, func(i ReadVarItem) error { _, err := DecodeAsWord(i); return err }},
		"dword": {4, func(i ReadVarItem) error { _, err := DecodeAsDWord(i); return err }},
		"int":   {2, func(i ReadVarItem) error { _, err := DecodeAsInt(i); return err }},
		"dint":  {4, func(i ReadVarItem) error { _, err := DecodeAsDInt(i); return err }},
		"real":  {4, func(i ReadVarItem) error { _, err := DecodeAsReal(i); return err }},
	}
	for name, d := range decoders {
		t.Run(name+" rejects short payload", func(t *testing.T) {
			var s7Err *S7Error
			item := ReadVarItem{ReturnCode: RetCodeSuccess, Data: make([]byte, d.need-1)}
			if err := d.decode(item); !errors.As(err, &s7Err) {
				t.Fatalf("err = %v, want *S7Error", err)
			}
		})
		t.Run(name+" rejects failed item", func(t *testing.T) {
			var s7Err *S7Error
			item := ReadVarItem{ReturnCode: RetCodeAddressFault, Data: make([]byte, 8)}
			if err := d.decode(item); !errors.As(err, &s7Err) {
				t.Fatalf("err = %v, want *S7Error", err)
			}
		})
	}
}

func TestParseReadVarResponse_RejectedItems(t *testing.T) {
	param := func(count byte) []byte { return []byte{FuncReadVar, count} }
	success := []byte{RetCodeSuccess, byte(RespTransportSizeByte), 0x00, 0x02, 0xAA, 0xBB}

	t.Run("nonzero transport and zero length has no payload", func(t *testing.T) {
		data := append([]byte{RetCodeAddressFault, byte(RespTransportSizeByte), 0, 0}, success...)
		items, err := ParseReadVarResponse(param(2), data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if items[0].ReturnCode != RetCodeAddressFault || items[0].Data != nil {
			t.Fatalf("item 0 = %+v", items[0])
		}
		if string(items[1].Data) != "\xAA\xBB" {
			t.Fatalf("item 1 = %+v", items[1])
		}
	})
	t.Run("rejected odd payload and fill byte are skipped", func(t *testing.T) {
		data := append([]byte{RetCodeAccessFault, byte(RespTransportSizeByte), 0, 3, 1, 2, 3, 0}, success...)
		items, err := ParseReadVarResponse(param(2), data)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if items[0].Data != nil || items[0].RawLength != 3 {
			t.Fatalf("rejected item exposed payload: %+v", items[0])
		}
		if string(items[1].Data) != "\xAA\xBB" {
			t.Fatalf("misaligned after rejected item: %+v", items[1])
		}
	})
	t.Run("last rejected item may omit the fill byte", func(t *testing.T) {
		items, err := ParseReadVarResponse(param(1), []byte{RetCodeAccessFault, byte(RespTransportSizeByte), 0, 1, 9})
		if err != nil || len(items) != 1 {
			t.Fatalf("items = %+v, err = %v", items, err)
		}
	})

	failures := []struct {
		name  string
		count byte
		data  []byte
		want  error
	}{
		{"header truncated", 1, []byte{RetCodeSuccess, 0x02, 0}, ErrTruncatedItemHeader},
		{"second header missing", 2, success, ErrTruncatedItemHeader},
		{"rejected payload overruns", 1, []byte{RetCodeAccessFault, byte(RespTransportSizeByte), 0, 9, 1}, ErrTruncatedItemPayload},
		{"rejected item missing fill before next", 2, []byte{RetCodeAccessFault, byte(RespTransportSizeByte), 0, 1, 9}, ErrTruncatedItemPayload},
		{"success item missing fill before next", 2, []byte{RetCodeSuccess, byte(RespTransportSizeByte), 0, 1, 9}, ErrTruncatedItemPayload},
		{"success payload overruns", 1, []byte{RetCodeSuccess, byte(RespTransportSizeBit), 0xFF, 0xFF}, ErrTruncatedItemPayload},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseReadVarResponse(param(tc.count), tc.data); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	for name, retCode := range map[string]byte{"rejected": RetCodeAccessFault, "success": RetCodeSuccess} {
		t.Run(name+" item with unknown transport", func(t *testing.T) {
			var s7Err *S7Error
			_, err := ParseReadVarResponse(param(1), []byte{retCode, 0x7E, 0, 8, 1})
			if !errors.As(err, &s7Err) || !strings.Contains(s7Err.Message, "0x7E") {
				t.Fatalf("err = %v, want unknown transport error", err)
			}
		})
	}
}

func TestParseWriteVarResponse_Malformed(t *testing.T) {
	cases := []struct {
		name        string
		param, data []byte
		wantCode    byte
	}{
		{"param too short", []byte{FuncWriteVar}, []byte{RetCodeSuccess}, 0},
		{"wrong function", []byte{FuncReadVar, 1}, []byte{RetCodeSuccess}, 0},
		{"no return code", []byte{FuncWriteVar, 1}, nil, 0},
		{"item rejected", []byte{FuncWriteVar, 1}, []byte{RetCodeBusy}, RetCodeBusy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s7Err *S7Error
			if err := ParseWriteVarResponse(tc.param, tc.data); !errors.As(err, &s7Err) || s7Err.Code != tc.wantCode {
				t.Fatalf("err = %v, want S7Error code 0x%02X", err, tc.wantCode)
			}
		})
	}
}

func TestEncodeStartUploadRequest_BlockTypeCharacter(t *testing.T) {
	cases := []struct {
		blockType byte
		want      byte
	}{
		{BlockTypeOB, '8'},
		{BlockTypeDB, 'A'},
		{BlockTypeFC, 'C'},
		{BlockTypeSFC, 'D'},
		{BlockTypeFB, 'E'},
		{BlockTypeSFB, 'F'},
	}
	for _, tc := range cases {
		req := EncodeStartUploadRequest(1, tc.blockType, 1234)
		filename := req[S7HeaderSize+9:]
		if filename[1] != tc.want {
			t.Errorf("block type 0x%02X: character %q, want %q", tc.blockType, filename[1], tc.want)
		}
		if string(filename[2:7]) != "01234" {
			t.Errorf("block number field = %q, want 01234", filename[2:7])
		}
	}
}

func TestParseStartUploadResponse_Malformed(t *testing.T) {
	valid := []byte{FuncUploadStart, 0, 0, 0, 0, 0, 0, 0, 2, '7', ' '}
	if id, err := ParseStartUploadResponse(valid); err != nil || id != "7" {
		t.Fatalf("id = %q, err = %v; want trimmed session ID", id, err)
	}

	wrongFunc := append([]byte{}, valid...)
	wrongFunc[0] = FuncUpload
	zeroLen := append([]byte{}, valid...)
	zeroLen[8] = 0
	overrun := append([]byte{}, valid...)
	overrun[8] = 3

	for name, param := range map[string][]byte{
		"empty": nil, "too short": valid[:9], "wrong function": wrongFunc,
		"zero length": zeroLen, "length overrun": overrun,
	} {
		t.Run(name, func(t *testing.T) {
			var s7Err *S7Error
			if _, err := ParseStartUploadResponse(param); !errors.As(err, &s7Err) {
				t.Fatalf("err = %v, want *S7Error", err)
			}
		})
	}
}

func TestParseUploadResponse_Boundaries(t *testing.T) {
	t.Run("nonzero status means more chunks", func(t *testing.T) {
		chunk, err := ParseUploadResponse([]byte{FuncUpload, 1}, []byte{0, 0, 0, 16, 0xAA, 0xBB})
		if err != nil || chunk.Done || len(chunk.Data) != 2 {
			t.Fatalf("chunk = %+v, err = %v", chunk, err)
		}
	})
	t.Run("length in bits rounds up", func(t *testing.T) {
		chunk, err := ParseUploadResponse([]byte{FuncUpload, 0}, []byte{0, 0, 0, 9, 0xAA, 0xBB, 0xCC})
		if err != nil || !chunk.Done || len(chunk.Data) != 2 {
			t.Fatalf("chunk = %+v, err = %v", chunk, err)
		}
	})
	t.Run("empty final chunk", func(t *testing.T) {
		chunk, err := ParseUploadResponse([]byte{FuncUpload, 0}, []byte{0, 0, 0, 0})
		if err != nil || !chunk.Done || len(chunk.Data) != 0 {
			t.Fatalf("chunk = %+v, err = %v", chunk, err)
		}
	})

	failures := map[string]struct{ param, data []byte }{
		"param too short": {[]byte{FuncUpload}, []byte{0, 0, 0, 0}},
		"wrong function":  {[]byte{FuncUploadEnd, 0}, []byte{0, 0, 0, 0}},
		"data too short":  {[]byte{FuncUpload, 0}, []byte{0, 0, 0}},
	}
	for name, tc := range failures {
		t.Run(name, func(t *testing.T) {
			if chunk, err := ParseUploadResponse(tc.param, tc.data); err == nil {
				t.Fatalf("chunk = %+v, want error", chunk)
			}
		})
	}
}

func TestEncodeUploadSessionID_TruncatedTo255(t *testing.T) {
	long := strings.Repeat("x", 300)
	for name, req := range map[string][]byte{
		"upload":     EncodeUploadRequest(1, long),
		"end upload": EncodeEndUploadRequest(1, long),
	} {
		t.Run(name, func(t *testing.T) {
			param := req[S7HeaderSize:]
			if param[8] != 255 || len(param) != 9+255 {
				t.Fatalf("declared %d, section %d; want 255 and %d", param[8], len(param), 9+255)
			}
		})
	}
}

func TestHeaderErrorString_AllClasses(t *testing.T) {
	cases := []struct {
		class, code byte
		want        string
	}{
		{ErrClassNoError, 0x00, ""},
		{ErrClassNoError, 0x05, "header error class=0x00 code=0x05"},
		{ErrClassAccess, 0x00, "no access rights"},
		{ErrClassAccess, 0x01, "invalid address"},
		{ErrClassAccess, 0x04, "invalid data type"},
		{ErrClassAccess, 0x09, "access error code=0x09"},
		{ErrClassObject, 0x01, "object does not exist"},
		{ErrClassResource, 0x01, "resource busy"},
		{ErrClassApp, 0x02, "S7 error class=0x81 code=0x02"},
		{ErrClassService, 0x02, "S7 error class=0x84 code=0x02"},
		{ErrClassSupplies, 0x02, "S7 error class=0x85 code=0x02"},
		{0x99, 0x02, "S7 error class=0x99 code=0x02"},
	}
	for _, tc := range cases {
		if got := HeaderErrorString(tc.class, tc.code); got != tc.want {
			t.Errorf("HeaderErrorString(0x%02X, 0x%02X) = %q, want %q", tc.class, tc.code, got, tc.want)
		}
	}
}

func TestROSCTRClassification(t *testing.T) {
	cases := []struct {
		r                      ROSCTR
		job, ack, ack2, userda bool
	}{
		{ROSCTRJob, true, false, false, false},
		{ROSCTRAck, false, true, false, false},
		{ROSCTRAckData, false, false, true, false},
		{ROSCTRUserdata, false, false, false, true},
		{ROSCTR(0x09), false, false, false, false},
	}
	for _, tc := range cases {
		if tc.r.IsJob() != tc.job || tc.r.IsAck() != tc.ack || tc.r.IsAckData() != tc.ack2 || tc.r.IsUserdata() != tc.userda {
			t.Errorf("ROSCTR 0x%02X misclassified", byte(tc.r))
		}
	}
}

// SDB is a block type of the client API; its upload must not name a DB of the same number.
func TestEncodeStartUploadRequest_SDBIsNotDB(t *testing.T) {
	req := EncodeStartUploadRequest(1, BlockTypeSDB, 7)
	if got := req[S7HeaderSize+9+1]; got != 'B' {
		t.Fatalf("block type character = %q, want 'B'", got)
	}
}
