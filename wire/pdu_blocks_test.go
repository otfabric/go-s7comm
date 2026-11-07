// SPDX-License-Identifier: MIT

package wire

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

// unhex decodes a hex string with spaces, as Wireshark prints bytes.
func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(string(bytes.ReplaceAll([]byte(s), []byte(" "), nil)))
	if err != nil {
		t.Fatalf("hex %q: %v", s, err)
	}
	return b
}

// sectionsOf returns the parameter and data sections of an encoded PDU.
func sectionsOf(t testing.TB, pdu []byte) ([]byte, []byte) {
	t.Helper()
	h, rest, err := ParseS7Header(pdu)
	if err != nil {
		t.Fatalf("ParseS7Header: %v", err)
	}
	return rest[:h.ParamLength], rest[h.ParamLength : h.ParamLength+h.DataLength]
}

// Requests as Snap7 and TIA Portal V13 sent them to a CPU 315-2 PN/DP
// (testdata/captures/gymgit; frames named in captures_test.go).
func TestBlockRequestsMatchRealClients(t *testing.T) {
	info, err := EncodeBlockInfoRequest(1, BlockTypeDB, 0)
	if err != nil {
		t.Fatal(err)
	}
	start, err := EncodeStartUploadRequest(1, BlockTypeSDB, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		pdu         []byte
		param, data string
	}{
		{"block count", EncodeBlockCountRequest(1), "00 01 12 04 11 43 01 00", "0a 00 00 00"},
		{"block list OB", EncodeBlockListRequest(1, BlockTypeOB), "00 01 12 04 11 43 02 00", "ff 09 00 02 30 38"},
		{"block list follow-up", EncodeUserdataFollowUp(1, UserdataGroupBlock, UserdataBlockList, 1), "00 01 12 08 12 43 02 01 00 00 00 00", "0a 00 00 00"},
		{"block info DB 0", info, "00 01 12 04 11 43 03 00", "ff 09 00 08 30 41 30 30 30 30 30 41"},
		{"start upload SDB 0", start, "1d 00 00 00 00 00 00 00 09 5f 30 42 30 30 30 30 30 41", ""},
		{"upload", EncodeUploadRequest(1, 7), "1e 00 00 00 00 00 00 07", ""},
		{"end upload", EncodeEndUploadRequest(1, 7), "1f 00 00 00 00 00 00 07", ""},
		{"SZL 0x0011", EncodeSZLRequest(1, SZLModuleID, 0), "00 01 12 04 11 44 01 00", "ff 09 00 04 00 11 00 00"},
		{"SZL follow-up", EncodeUserdataFollowUp(1, UserdataGroupCPU, UserdataSZL, 2), "00 01 12 08 12 44 01 02 00 00 00 00", "0a 00 00 00"},
	} {
		param, data := sectionsOf(t, tc.pdu)
		if !bytes.Equal(param, unhex(t, tc.param)) || !bytes.Equal(data, unhex(t, tc.data)) {
			t.Errorf("%s: param % x data % x, want %s | %s", tc.name, param, data, tc.param, tc.data)
		}
	}
}

func TestBlockAddressLimits(t *testing.T) {
	for _, bt := range []byte{0x00, 0x37, 0x39, 0x40, 0x47, 0xFF} {
		if req, err := EncodeStartUploadRequest(1, bt, 7); !errors.Is(err, ErrUnknownBlockType) || req != nil {
			t.Fatalf("type 0x%02X: %x, %v; want ErrUnknownBlockType", bt, req, err)
		}
		if _, err := EncodeBlockInfoRequest(1, bt, 7); !errors.Is(err, ErrUnknownBlockType) {
			t.Fatalf("type 0x%02X: %v; want ErrUnknownBlockType", bt, err)
		}
	}
	for _, n := range []int{-1, MaxUploadBlockNumber + 1} {
		if _, err := EncodeStartUploadRequest(1, BlockTypeDB, n); !errors.Is(err, ErrOutOfRange) {
			t.Fatalf("number %d: %v; want ErrOutOfRange", n, err)
		}
	}
	for bt, c := range map[byte]byte{BlockTypeOB: '8', BlockTypeDB: 'A', BlockTypeSDB: 'B', BlockTypeFC: 'C', BlockTypeSFC: 'D', BlockTypeFB: 'E', BlockTypeSFB: 'F'} {
		req, err := EncodeStartUploadRequest(1, bt, 1234)
		if err != nil {
			t.Fatal(err)
		}
		if want := []byte{'_', '0', c, '0', '1', '2', '3', '4', 'A'}; !bytes.HasSuffix(req, want) {
			t.Errorf("type 0x%02X: % x, want suffix %q", bt, req, want)
		}
	}
}

// Answers of a CPU 315-2 PN/DP (TIA Portal V13 go online, frames 88, 94, 136).
func TestParseBlockAnswers(t *testing.T) {
	counts, err := ParseBlockCountResponse(unhex(t, "30 38 00 01 30 45 00 01 30 43 00 00 30 41 00 02 30 42 00 08 30 44 00 4d 30 46 00 0f"))
	if err != nil {
		t.Fatal(err)
	}
	want := []BlockCount{{BlockTypeOB, 1}, {BlockTypeFB, 1}, {BlockTypeFC, 0}, {BlockTypeDB, 2}, {BlockTypeSDB, 8}, {BlockTypeSFC, 77}, {BlockTypeSFB, 15}}
	if len(counts) != len(want) {
		t.Fatalf("counts = %+v", counts)
	}
	for i := range want {
		if counts[i] != want[i] {
			t.Errorf("count %d = %+v, want %+v", i, counts[i], want[i])
		}
	}
	if _, err := ParseBlockCountResponse([]byte{0x30, 0x38, 0}); err == nil {
		t.Error("cut block count accepted")
	}

	list, err := ParseBlockListResponse(unhex(t, "00 01 22 05 00 02 22 05"))
	if err != nil || len(list) != 2 || list[1].BlockNumber != 2 || list[1].Flags != 0x22 || list[1].Language != 5 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := ParseBlockListResponse([]byte{0, 1, 2}); err == nil {
		t.Error("cut block list accepted")
	}

	bi, err := ParseBlockInfoResponse(unhex(t, "01 00 00 4a 22 00 70 70 01 01 05 0a 00 01 00 00 00 66 00 00 00 03 04 ef 14 b0 2d 97 01 cb 65 50 11 fc 00 14 00 00 00 00 00 0a 53 49 4d 41 54 49 43 00 49 45 43 5f 54 43 00 00 43 54 55 00 00 00 00 00 10 00 79 8c 00 00 00 00 00 00 00 00"))
	if err != nil {
		t.Fatal(err)
	}
	wantInfo := BlockInfoData{Flags: 1, Language: 5, SubBlockType: 0x0a, Number: 1, LoadMemory: 102, Security: 3, SSBLength: 20, MC7Size: 10,
		Author: "SIMATIC", Family: "IEC_TC", Name: "CTU", Version: "1.0", Checksum: 0x798c}
	if bi != wantInfo {
		t.Fatalf("block info = %+v\nwant %+v", bi, wantInfo)
	}
	if _, err := ParseBlockInfoResponse(make([]byte, blockInfoSize-1)); err == nil {
		t.Error("short block info accepted")
	}
}

// Upload answers of a CPU 315-2 PN/DP to Snap7 (client demo, frames 18, 20).
func TestParseUploadAnswers(t *testing.T) {
	start, err := ParseStartUploadResponse(unhex(t, "1d 00 01 00 00 00 00 07 07 30 30 30 30 32 31 36"))
	if err != nil || start.UploadID != 7 || start.Length != 216 {
		t.Fatalf("start = %+v, %v", start, err)
	}
	if s, err := ParseStartUploadResponse(unhex(t, "1d 00 01 00 00 00 00 07")); err != nil || s.Length != -1 {
		t.Fatalf("without length: %+v, %v", s, err)
	}
	if s, err := ParseStartUploadResponse(unhex(t, "1d 00 01 00 00 00 00 07 02 78 79")); err != nil || s.Length != -1 {
		t.Fatalf("non-numeric length: %+v, %v", s, err)
	}
	for name, p := range map[string][]byte{
		"empty": nil, "short": unhex(t, "1d 00 01 00 00 00 00"), "wrong function": unhex(t, "1e 00 01 00 00 00 00 07"),
		"length overrun": unhex(t, "1d 00 01 00 00 00 00 07 05 30"),
	} {
		var s7Err *S7Error
		if _, err := ParseStartUploadResponse(p); !errors.As(err, &s7Err) {
			t.Errorf("%s: err = %v, want *S7Error", name, err)
		}
	}

	chunk, err := ParseUploadResponse([]byte{FuncUpload, 0x00}, unhex(t, "00 03 00 fb aa bb cc"))
	if err != nil || !chunk.Done || !bytes.Equal(chunk.Data, []byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("last chunk = %+v, %v", chunk, err)
	}
	if chunk, err := ParseUploadResponse([]byte{FuncUpload, 0x01}, unhex(t, "00 00 00 fb")); err != nil || chunk.Done {
		t.Fatalf("status 0x01 must mean more data: %+v, %v", chunk, err)
	}
	for name, tc := range map[string]struct{ p, d []byte }{
		"param too short": {[]byte{FuncUpload}, []byte{0, 0, 0, 0}},
		"wrong function":  {[]byte{FuncUploadEnd, 0}, []byte{0, 0, 0, 0}},
		"data too short":  {[]byte{FuncUpload, 0}, []byte{0, 0, 0}},
		"length overrun":  {[]byte{FuncUpload, 0}, []byte{0, 5, 0, 0xfb, 1}},
	} {
		if chunk, err := ParseUploadResponse(tc.p, tc.d); err == nil {
			t.Errorf("%s: chunk = %+v, want error", name, chunk)
		}
	}
}

func TestUserdataParamAndData(t *testing.T) {
	up, err := ParseUserdataParam(unhex(t, "00 01 12 08 12 84 01 02 d5 01 00 00"))
	if err != nil || up.Group != UserdataGroupCPU || up.Subfunction != UserdataSZL || up.Sequence != 2 || !up.More || up.ErrorCode != 0 {
		t.Fatalf("param = %+v, %v", up, err)
	}
	up, err = ParseUserdataParam(unhex(t, "00 01 12 08 12 83 03 00 00 00 d2 09"))
	if err != nil || up.More || up.ErrorCode != 0xd209 {
		t.Fatalf("error param = %+v, %v", up, err)
	}
	for _, p := range []string{"00 01 12 04 11 44 01 00", "00 01 12 08 12 84 01 02 d5 01 00"} {
		if _, err := ParseUserdataParam(unhex(t, p)); err == nil {
			t.Errorf("%s accepted", p)
		}
	}

	if p, err := ParseUserdataData(unhex(t, "ff 09 00 02 30 38")); err != nil || !bytes.Equal(p, []byte{0x30, 0x38}) {
		t.Fatalf("payload = % x, %v", p, err)
	}
	var s7Err *S7Error
	if _, err := ParseUserdataData(unhex(t, "0a 00 00 00")); !errors.As(err, &s7Err) || s7Err.Code != RetCodeNotAvailable {
		t.Fatalf("refusal: %v", err)
	}
	for _, d := range []string{"ff 09 00", "ff 09 00 05 01"} {
		if _, err := ParseUserdataData(unhex(t, d)); err == nil {
			t.Errorf("%s accepted", d)
		}
	}
}

func FuzzParseStartUploadResponse(f *testing.F) {
	f.Add([]byte{FuncUploadStart, 0, 1, 0, 0, 0, 0, 7, 3, '2', '1', '6'})
	f.Add([]byte{FuncUploadStart, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0})
	f.Fuzz(func(t *testing.T, param []byte) { _, _ = ParseStartUploadResponse(param) })
}

func FuzzParseUploadResponse(f *testing.F) {
	f.Add([]byte{FuncUpload, 0}, []byte{0, 2, 0, 0xfb, 1, 2})
	f.Fuzz(func(t *testing.T, param, data []byte) {
		if c, err := ParseUploadResponse(param, data); err == nil && len(c.Data) > len(data) {
			t.Fatalf("chunk longer than data")
		}
	})
}

func FuzzParseUserdata(f *testing.F) {
	f.Add([]byte{0, 1, 0x12, 8, 0x12, 0x84, 1, 2, 0xd5, 1, 0, 0}, []byte{0xff, 9, 0, 2, 0x30, 0x38})
	f.Fuzz(func(t *testing.T, param, data []byte) {
		_, _ = ParseUserdataParam(param)
		if p, err := ParseUserdataData(data); err == nil && len(p) > len(data) {
			t.Fatalf("payload longer than data")
		}
		_, _ = ParseBlockCountResponse(data)
	})
}
