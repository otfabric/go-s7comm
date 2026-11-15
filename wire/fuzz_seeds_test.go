// SPDX-License-Identifier: MIT

package wire

import (
	"testing"

	"github.com/otfabric/go-s7comm/internal/capture"
)

// seedCaptures are the recordings the fuzz targets take their seeds from: a
// Snap7 session with SZL reads, block functions and an upload, TIA Portal
// going online, and libnodave reading and writing variables.
var seedCaptures = []string{
	"../testdata/captures/gymgit/snap7_s7-300_client-demo.pcapng",
	"../testdata/captures/gymgit/tia-v13_s7-300_go-online.pcapng",
	"../testdata/captures/wireshark/libnodave_s7-300_read-write-var.pcap",
}

// recordedPDU is one PDU of a seed capture with its sections.
type recordedPDU struct {
	fromClient  bool
	pdu         []byte
	header      *S7Header
	param, data []byte
}

// seedPDUs returns up to n recorded PDUs that keep accepts, as fuzz seeds:
// answers of real CPUs and requests of real clients (testdata/captures).
func seedPDUs(tb testing.TB, n int, keep func(p recordedPDU) bool) []recordedPDU {
	tb.Helper()
	var out []recordedPDU
	for _, path := range seedCaptures {
		convs, err := capture.ReadFile(path)
		if err != nil {
			tb.Fatalf("%s: %v", path, err)
		}
		for _, c := range convs {
			for _, p := range c.PDUs {
				h, rest, err := ParseS7Header(p.Data)
				if err != nil {
					continue
				}
				pl, dl := int(h.ParamLength), int(h.DataLength)
				r := recordedPDU{fromClient: p.FromClient, pdu: p.Data, header: h, param: rest[:pl], data: rest[pl : pl+dl]}
				if keep(r) {
					out = append(out, r)
					if len(out) == n {
						return out
					}
				}
			}
		}
	}
	if len(out) == 0 {
		tb.Fatal("no recorded PDU for the fuzz seeds")
	}
	return out
}

// recordedReadAnswer returns a recorded Read Var answer of n items without
// a header error, from WinCC Advanced reading an S7-300.
func recordedReadAnswer(tb testing.TB, n int) recordedPDU {
	tb.Helper()
	convs, err := capture.ReadFile("../testdata/captures/gymgit/wincc-advanced_s7-300_alarm-read-1.pcapng")
	if err != nil {
		tb.Fatal(err)
	}
	for _, c := range convs {
		for _, p := range c.PDUs {
			h, rest, err := ParseS7Header(p.Data)
			if err != nil || p.FromClient || !h.ROSCTR.IsAckData() || h.ErrorClass != 0 || h.ParamLength < 2 {
				continue
			}
			pl, dl := int(h.ParamLength), int(h.DataLength)
			if rest[0] == FuncReadVar && int(rest[1]) == n {
				return recordedPDU{pdu: p.Data, header: h, param: rest[:pl], data: rest[pl : pl+dl]}
			}
		}
	}
	tb.Fatalf("no recorded Read Var answer of %d items", n)
	return recordedPDU{}
}

// answerTo keeps the Ack-Data answers to the job function fn.
func answerTo(fn byte) func(recordedPDU) bool {
	return func(p recordedPDU) bool {
		return !p.fromClient && p.header.ROSCTR.IsAckData() && len(p.param) > 0 && p.param[0] == fn
	}
}

// within returns the offset of inner in outer when inner is a view of
// outer's bytes; an empty inner is within at offset 0.
func within(outer, inner []byte) (int, bool) {
	if len(inner) == 0 {
		return 0, true
	}
	for i := range outer {
		if &outer[i] == &inner[0] {
			return i, i+len(inner) <= len(outer)
		}
	}
	return 0, false
}
