// SPDX-License-Identifier: MIT

package wire

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/otfabric/go-s7comm/internal/capture"
)

// The tests in this file decode the recorded traffic of real S7-300 and
// S7-400 CPUs in testdata/captures (testdata/captures/README.md lists every
// file, device and client).

func captureFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("../testdata/captures/*/*.pcap*")
	if err != nil || len(files) != 25 {
		t.Fatalf("captures: %d files, %v; want 25", len(files), err)
	}
	return files
}

func readCapture(t *testing.T, path string) []capture.Conversation {
	t.Helper()
	convs, err := capture.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return convs
}

// elementBytes is the size of one element of a request transport size, as
// a CPU answers it (BIT: one byte per bit item).
func elementBytes(ts byte) int {
	switch ts {
	case TransportSizeBit, TransportSizeByte, 0x03: // BIT, BYTE, CHAR
		return 1
	case 0x04, 0x05, 0x09, 0x0C, 0x1C, 0x1D: // WORD, INT, DATE, S5TIME, COUNTER, TIMER
		return 2
	case 0x06, 0x07, 0x08, 0x0A, 0x0B: // DWORD, DINT, REAL, TOD, TIME
		return 4
	case 0x0F: // DATE_AND_TIME
		return 8
	}
	return 0
}

type jobPair struct{ req, resp capture.PDU }

// jobPairs matches jobs with their Ack-Data by PDU reference.
func jobPairs(c capture.Conversation) []jobPair {
	open := map[uint16]capture.PDU{}
	var out []jobPair
	for _, p := range c.PDUs {
		h, _, err := ParseS7Header(p.Data)
		if err != nil {
			continue
		}
		switch {
		case p.FromClient && h.ROSCTR.IsJob():
			open[h.PDURef] = p
		case !p.FromClient && (h.ROSCTR.IsAckData() || h.ROSCTR.IsAck()):
			if q, ok := open[h.PDURef]; ok {
				out = append(out, jobPair{q, p})
				delete(open, h.PDURef)
			}
		}
	}
	return out
}

func splitPDU(t *testing.T, pdu []byte) (*S7Header, []byte, []byte) {
	t.Helper()
	h, rest, err := ParseS7Header(pdu)
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	pl := int(h.ParamLength)
	return h, rest[:pl], rest[pl : pl+int(h.DataLength)]
}

// Every recorded PDU has a valid header and InspectTPDU summarises it.
func TestCapturesEveryPDUDecodes(t *testing.T) {
	total := 0
	for _, f := range captureFiles(t) {
		for _, c := range readCapture(t, f) {
			for _, p := range c.PDUs {
				total++
				if _, _, err := ParseS7Header(p.Data); err != nil {
					t.Errorf("%s frame %d: %v", filepath.Base(f), p.Frame, err)
				}
				if _, err := InspectTPDU(append([]byte{0x02, 0xF0, 0x80}, p.Data...)); err != nil {
					t.Errorf("%s frame %d: InspectTPDU: %v", filepath.Base(f), p.Frame, err)
				}
			}
		}
	}
	if total != 14393 {
		t.Fatalf("%d PDUs, want 14393 (Wireshark 4.6.9)", total)
	}
}

// varStats counts what the Read Var, Write Var and Setup Communication
// checks covered, per capture.
type varStats struct{ setups, reads, readItems, writes, writeItems int }

// Read Var: every request decodes with the server's parser, every answer
// with the client's, one item per request item, and every successful S7ANY
// item holds exactly the requested number of bytes (the length field is
// bits for BIT/BYTE/INT answers and bytes for DINT/REAL/OCTET STRING, as
// real CPUs send them). Write Var and Setup Communication decode both ways,
// and ParseWriteVarResponse fails exactly when an item was refused.
func TestCapturesVariableServices(t *testing.T) {
	want := map[string]varStats{
		"snap7_s7-300_client-demo.pcapng":               {1, 2, 6, 2, 6},
		"snap7_s7-300_read-var.pcapng":                  {0, 1, 1, 0, 0},
		"snap7_s7-300_setup-communication.pcapng":       {1, 0, 0, 0, 0},
		"step7-v5.5_s7-300_download-blocks.pcapng":      {1, 0, 0, 0, 0},
		"tia-v13_s7-300_download-hw-config.pcapng":      {6, 0, 0, 0, 0},
		"tia-v13_s7-300_go-online.pcapng":               {1, 0, 0, 0, 0},
		"wincc-advanced_s7-300_alarm-read-1.pcapng":     {1, 55, 645, 2, 2},
		"wincc-advanced_s7-300_alarm-read-2.pcapng":     {1, 228, 1029, 8, 8},
		"wincc-advanced_s7-300_alarm-read-write.pcapng": {1, 38, 304, 36, 36},
		"wincc-advanced_s7-400_production.pcapng":       {1, 84, 84, 27, 27},
		"libnodave_s7-300_benchmark.pcap":               {1, 3000, 6996, 2002, 2002},
		"libnodave_s7-300_read-write-var.pcap":          {1, 3, 3, 4, 4},
		"unknown_s7-300_download-db1.pcap":              {3, 0, 0, 0, 0},
		"unknown_s7-300_list-blocks.pcap":               {5, 0, 0, 0, 0},
		"unknown_s7-300_read-plc-status.pcap":           {1, 0, 0, 0, 0},
		"unknown_s7-300_read-set-clock.pcap":            {1, 0, 0, 0, 0},
	}
	for _, f := range captureFiles(t) {
		name := filepath.Base(f)
		var got varStats
		for _, c := range readCapture(t, f) {
			for _, pr := range jobPairs(c) {
				_, qp, qd := splitPDU(t, pr.req.Data)
				h, rp, rd := splitPDU(t, pr.resp.Data)
				if len(qp) == 0 {
					continue
				}
				where := fmt.Sprintf("%s frame %d", name, pr.resp.Frame)
				switch qp[0] {
				case FuncSetupComm:
					got.setups++
					if _, err := ParseSetupCommRequest(qp); err != nil {
						t.Errorf("%s: setup request: %v", where, err)
					}
					if _, err := ParseSetupCommResponse(rp); err != nil {
						t.Errorf("%s: setup response: %v", where, err)
					}
				case FuncReadVar:
					got.reads++
					specs, err := ParseReadVarRequest(qp)
					if err != nil {
						t.Errorf("%s: read request: %v", where, err)
						continue
					}
					items, err := ParseReadVarResponse(rp, rd)
					if err != nil || h.ErrorClass != 0 {
						t.Errorf("%s: read response: %v (header %02x%02x)", where, err, h.ErrorClass, h.ErrorCode)
						continue
					}
					if len(items) != len(specs) {
						t.Errorf("%s: %d items for %d requested", where, len(items), len(specs))
						continue
					}
					got.readItems += len(items)
					for i, it := range items {
						s := specs[i]
						if it.ReturnCode != RetCodeSuccess || s.Syntax != SyntaxIDS7Any {
							continue // DBREAD items (S7-400) have their own length layout
						}
						if want := int(s.Count) * elementBytes(s.TransportSize); len(it.Data) != want {
							t.Errorf("%s item %d: %d bytes, want %d (transport 0x%02x, answer 0x%02x)", where, i, len(it.Data), want, s.TransportSize, it.RawTransportSize)
						}
					}
				case FuncWriteVar:
					got.writes++
					specs, data, err := ParseWriteVarRequest(qp, qd)
					if err != nil || len(specs) != len(data) {
						t.Errorf("%s: write request: %v", where, err)
						continue
					}
					codes, err := ParseWriteVarResponseCodes(rp, rd)
					if err != nil || len(codes) != len(specs) {
						t.Errorf("%s: write response: % x, %v", where, codes, err)
						continue
					}
					got.writeItems += len(codes)
					allOK := bytes.Count(codes, []byte{RetCodeSuccess}) == len(codes)
					if err := ParseWriteVarResponse(rp, rd); (err == nil) != allOK {
						t.Errorf("%s: codes % x, ParseWriteVarResponse = %v", where, codes, err)
					}
				}
			}
		}
		if got != want[name] {
			t.Errorf("%s: %+v, want %+v", name, got, want[name])
		}
	}
}

// A CPU 315-2 PN/DP answers one Write Var of five items (M, I, Q, timer,
// counter) with FF FF FF 03 03: it refuses the writes to the existing
// timer and counter with "object access is not allowed" (0x03), while it
// answers a read of the same five items (the next job) with FF for each.
// ParseWriteVarResponse reports the first refused item.
func TestCapturesRefusedWriteIsAccessDenied(t *testing.T) {
	convs := readCapture(t, "../testdata/captures/gymgit/snap7_s7-300_client-demo.pcapng")
	var codes, readCodes []byte
	for _, pr := range jobPairs(convs[0]) {
		_, qp, _ := splitPDU(t, pr.req.Data)
		_, rp, rd := splitPDU(t, pr.resp.Data)
		switch {
		case pr.resp.Frame == 54:
			var err error
			if codes, err = ParseWriteVarResponseCodes(rp, rd); err != nil {
				t.Fatal(err)
			}
			var s7Err *S7Error
			if err := ParseWriteVarResponse(rp, rd); !errors.As(err, &s7Err) || s7Err.Code != RetCodeAccessFault {
				t.Fatalf("ParseWriteVarResponse = %v, want item 3 access denied", err)
			}
		case pr.resp.Frame == 56 && qp[0] == FuncReadVar:
			items, err := ParseReadVarResponse(rp, rd)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range items {
				readCodes = append(readCodes, it.ReturnCode)
			}
		}
	}
	if !bytes.Equal(codes, []byte{0xFF, 0xFF, 0xFF, 0x03, 0x03}) || !bytes.Equal(readCodes, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Fatalf("write codes % x, read codes % x", codes, readCodes)
	}
}

// Every user data request of the five clients in the captures (Snap7,
// STEP 7 5.5, TIA Portal V13, WinCC Advanced and the unidentified client of
// the Wireshark samples) starts 00 01 12 04 11 (first request) or
// 00 01 12 08 12 (follow-up or push); every user data answer decodes with
// ParseUserdataParam, and every first SZL packet with ParseSZLResponse.
// Messages a CPU sends on its own (alarms, cyclic data) carry a request
// parameter and are counted apart.
func TestCapturesUserdata(t *testing.T) {
	first, follow := []byte{0x00, 0x01, 0x12, 0x04, 0x11}, []byte{0x00, 0x01, 0x12, 0x08, 0x12}
	var requests, answers, pushes, szl int
	for _, f := range captureFiles(t) {
		for _, c := range readCapture(t, f) {
			followUps := map[string]bool{} // PDU references of follow-up requests
			for _, p := range c.PDUs {
				h, param, data := splitPDU(t, p.Data)
				if !h.ROSCTR.IsUserdata() {
					continue
				}
				where := fmt.Sprintf("%s frame %d", filepath.Base(f), p.Frame)
				if p.FromClient {
					requests++
					switch {
					case bytes.HasPrefix(param, first):
					case bytes.HasPrefix(param, follow):
						followUps[string(p.Data[4:6])] = true
					default:
						t.Errorf("%s: request parameter % x", where, param)
					}
					continue
				}
				if bytes.HasPrefix(param, first) || len(param) < 3 || param[2] != 0x12 {
					pushes++ // the CPU's own messages (diagnostics, alarms), not answers
					continue
				}
				answers++
				up, err := ParseUserdataParam(param)
				if err != nil {
					t.Errorf("%s: %v", where, err)
					continue
				}
				if up.Group == UserdataGroupCPU && up.Subfunction == UserdataSZL && len(data) > 0 && data[0] == RetCodeSuccess && !followUps[string(p.Data[4:6])] {
					szl++
					if _, err := ParseSZLResponse(data); err != nil {
						t.Errorf("%s: SZL: %v", where, err)
					}
				}
			}
		}
	}
	t.Logf("%d user data requests, %d answers, %d CPU messages, %d first SZL packets", requests, answers, pushes, szl)
	if requests == 0 || answers == 0 || szl == 0 {
		t.Fatal("no user data in the captures")
	}
}

// The encoders produce, byte for byte, requests that real clients sent:
// each encoded PDU (PDU reference aside) is one of the recorded requests.
func TestCapturesEncodersMatchRecordedRequests(t *testing.T) {
	recorded := map[string]map[string]bool{}
	for _, f := range captureFiles(t) {
		set := map[string]bool{}
		for _, c := range readCapture(t, f) {
			for _, p := range c.PDUs {
				if p.FromClient {
					set[string(p.Data[6:])] = true
				}
			}
		}
		recorded[filepath.Base(f)] = set
	}
	must := func(b []byte, err error) []byte {
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	snap7, tia, lnd := "snap7_s7-300_client-demo.pcapng", "tia-v13_s7-300_go-online.pcapng", "libnodave_s7-300_read-write-var.pcap"
	for _, tc := range []struct {
		name, file string
		pdu        []byte
	}{
		{"setup communication", snap7, must(EncodeSetupCommRequest(1, 1, 1, 480))},
		{"SZL 0x0011", snap7, EncodeSZLRequest(1, SZLModuleID, 0)},
		{"SZL 0x001C", snap7, EncodeSZLRequest(1, SZLComponentID, 0)},
		{"SZL 0x0424", snap7, EncodeSZLRequest(1, SZLCPUState, 0)},
		{"SZL follow-up", snap7, EncodeUserdataFollowUp(1, UserdataGroupCPU, UserdataSZL, 2)},
		{"block count", snap7, EncodeBlockCountRequest(1)},
		{"block info DB 0", snap7, must(EncodeBlockInfoRequest(1, BlockTypeDB, 0))},
		{"start upload SDB 0", snap7, must(EncodeStartUploadRequest(1, BlockTypeSDB, 0))},
		{"upload", snap7, EncodeUploadRequest(1, 7)},
		{"end upload", snap7, EncodeEndUploadRequest(1, 7)},
		{"block list SFC", tia, EncodeBlockListRequest(1, BlockTypeSFC)},
		{"block list follow-up", tia, EncodeUserdataFollowUp(1, UserdataGroupBlock, UserdataBlockList, 1)},
		{"block info OB 1", tia, must(EncodeBlockInfoRequest(1, BlockTypeOB, 1))},
		{"SZL 0x0232 index 4", tia, EncodeSZLRequest(1, SZLProtectionInfo, 4)},
		{"SZL 0x00A0", tia, EncodeSZLRequest(1, SZLDiagBuffer, 0)},
		{"read M 0..15", lnd, must(EncodeReadVarRequest(1, []S7AnyAddress{{Area: AreaMerkers, Start: 0, Size: 16}}))},
	} {
		if !recorded[tc.file][string(tc.pdu[6:])] {
			t.Errorf("%s: % x is not among the requests of %s", tc.name, tc.pdu, tc.file)
		}
	}
}
