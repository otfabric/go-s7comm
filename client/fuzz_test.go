// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/internal/capture"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// recordedSZLLists returns the list data (record length, record count,
// records) of up to n SZL answers of real CPUs (testdata/captures).
func recordedSZLLists(tb testing.TB, n int) [][]byte {
	tb.Helper()
	var out [][]byte
	for _, path := range []string{
		"../testdata/captures/gymgit/snap7_s7-300_client-demo.pcapng",
		"../testdata/captures/wireshark/unknown_s7-300_read-plc-status.pcap",
	} {
		convs, err := capture.ReadFile(path)
		if err != nil {
			tb.Fatalf("%s: %v", path, err)
		}
		for _, c := range convs {
			for _, p := range c.PDUs {
				h, rest, err := wire.ParseS7Header(p.Data)
				if err != nil || p.FromClient || !h.ROSCTR.IsUserdata() {
					continue
				}
				param, data, ok := splitS7Payload(h, rest)
				if !ok || len(param) < 12 || param[5]&0x0f != wire.UserdataGroupCPU || param[6] != wire.UserdataSZL {
					continue
				}
				if resp, err := wire.ParseSZLResponse(data); err == nil && len(out) < n {
					out = append(out, resp.Data)
				}
			}
		}
	}
	if len(out) == 0 {
		tb.Fatal("no recorded SZL answer for the fuzz seeds")
	}
	return out
}

// FuzzSZLRecords feeds SZL list data to the client's own decoders. Records
// are views of the data, each of the declared length and none past its end;
// there are as many as declared, or as many as arrived when that is fewer.
// The module and component decoders refuse exactly the lists whose records
// are too short for them, and no text they return is longer than its field.
func FuzzSZLRecords(f *testing.F) {
	f.Add(szlList(moduleIDRecordSize, append([]byte{0x00, 0x01}, "6ES7 315-2EH14-0AB0"...), []byte{0x00, 0x07, 22: 0, 24: 'V', 25: 3, 26: 2, 27: 7}))
	f.Add(szlList(componentIDRecordSize, append([]byte{0x00, 0x02}, "CPU 315-2 PN/DP"...), append([]byte{0x00, 0x07}, "CPU 315-2 PN/DP"...)))
	f.Add(szlList(diagRecordSize, []byte{0x43, 0x02, 0xff}))
	f.Add([]byte{0, 0, 0, 1})
	f.Add([]byte{0, 4, 0xff, 0xff, 1, 2, 3})
	for _, list := range recordedSZLLists(f, 8) {
		f.Add(list)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		recs, err := szlRecords(data)
		if len(data) < 4 || binary.BigEndian.Uint16(data[0:2]) == 0 {
			if err == nil || recs != nil {
				t.Fatalf("%d records, %v from a list without a usable header", len(recs), err)
			}
			return
		}
		if err != nil {
			t.Fatalf("list header % x refused: %v", data[:4], err)
		}
		size, count := int(binary.BigEndian.Uint16(data[0:2])), int(binary.BigEndian.Uint16(data[2:4]))
		if want := min(count, (len(data)-4)/size); len(recs) != want {
			t.Fatalf("%d records; %d declared, %d arrived", len(recs), count, (len(data)-4)/size)
		}
		for i, r := range recs {
			if len(r) != size || &r[0] != &data[4+i*size] {
				t.Fatalf("record %d: %d octets, not the octets at offset %d", i, len(r), 4+i*size)
			}
		}

		var module model.DeviceInfo
		if err := applyModuleID(&module, data); (err != nil) != (len(recs) > 0 && size < moduleIDRecordSize) {
			t.Fatalf("module identification of %d records of %d octets: %v", len(recs), size, err)
		}
		if len(module.OrderNumber) > 20 || len(module.FWVersion) > len("V255.255.255") {
			t.Fatalf("module identification %+v", module)
		}
		var component model.DeviceInfo
		if err := applyComponentID(&component, data); (err != nil) != (len(recs) > 0 && size < componentIDRecordSize) {
			t.Fatalf("component identification of %d records of %d octets: %v", len(recs), size, err)
		}
		for _, text := range []string{component.SystemName, component.ModuleName, component.PlantID, component.Copyright,
			component.SerialNumber, component.ModuleType, component.CPUType, component.MemoryCardSerial} {
			if len(text) > 32 {
				t.Fatalf("component identification text of %d octets: %q", len(text), text)
			}
		}
	})
}

// FuzzUserdataAnswers runs the calls that collect a user data answer against
// a peer that grants any PDU size and answers every request with the fuzzed
// payload, marked "more data follows" for the fuzzed number of packets.
// Whatever the peer sends, the session's PDU size is at most the proposed
// one, and what a call returns is bounded by the limits of SECURITY.md: 257
// packets of that size, 65,536 blocks, 256 block types, 32 octets per text.
func FuzzUserdataAnswers(f *testing.F) {
	list := szlList(diagRecordSize, []byte{0x43, 0x02, 0xff}, []byte{0x13, 0x81, 0x1b})
	f.Add(uint8(0), uint16(480), uint8(0), []byte{0x00, 0x01, 0x22, 0x05, 0x00, 0x02, 0x22, 0x05})
	f.Add(uint8(1), uint16(240), uint8(1), []byte{0x30, 0x41, 0x00, 0x02, 0x30, 0x38, 0x00, 0x01})
	f.Add(uint8(2), uint16(480), uint8(2), append([]byte{0x00, 0xa0, 0x00, 0x00}, list...))
	f.Add(uint8(3), uint16(65535), uint8(255), append([]byte{0x00, 0xa0, 0x00, 0x00}, list...))
	f.Add(uint8(4), uint16(0), uint8(0), append([]byte{0x00, 0x11, 0x00, 0x00}, szlList(moduleIDRecordSize, []byte{0x00, 0x01, 'X'})...))
	f.Add(uint8(5), uint16(480), uint8(0), append([]byte{0x04, 0x24, 0x00, 0x00}, szlList(20, []byte{0x51, 0x44, 0xff, 0x08})...))
	f.Add(uint8(6), uint16(480), uint8(0), append([]byte{0x02, 0x32, 0x00, 0x04}, szlList(40, []byte{0, 4, 0, 1, 0, 0, 0, 2})...))
	f.Add(uint8(0), uint16(4096), uint8(3), make([]byte, 600))
	f.Fuzz(func(t *testing.T, call uint8, granted uint16, packets uint8, payload []byte) {
		const proposed = 480
		if len(payload) > 70000 {
			payload = payload[:70000]
		}
		sent := 0
		peers := &pipePeers{
			setup: func(ref uint16) []byte { return buildS7SetupResponse(ref, int(granted)) },
			op: func(ref uint16, req []byte) []byte {
				if !wire.ROSCTR(req[1]).IsUserdata() || len(req) < wire.S7HeaderSize+8 {
					return nil
				}
				param := req[wire.S7HeaderSize:]
				sent++
				data := append([]byte{wire.RetCodeSuccess, 0x09, byte(len(payload) >> 8), byte(len(payload))}, payload...)
				return buildUserdataResponse(ref, param[5]&0x0f, param[6], 1, sent <= int(packets), data)
			},
		}
		defer peers.wait()
		c := New("pipe", WithDialer(peers.dial), WithRackSlot(0, 1), WithMaxPDU(proposed))
		defer func() { _ = c.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.Connect(ctx); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		if got := c.PDUSize(); got > proposed || got != min(int(granted), proposed) {
			t.Fatalf("PDU size %d: %d proposed, %d granted", got, proposed, granted)
		}
		// The most one call can collect: 257 packets, each at most as long
		// as an answer on this session may be.
		collected := (maxUserdataFollowUps + 1) * answerLimit(c.PDUSize(), proposed)

		var err error
		switch call % 7 {
		case 0:
			var blocks []model.BlockInfo
			if blocks, err = c.ListBlocks(ctx, model.BlockDB); len(blocks) > maxBlockListEntries || 4*len(blocks) > collected {
				t.Fatalf("ListBlocks returned %d blocks", len(blocks))
			}
		case 1:
			var counts map[model.BlockType]int
			if counts, err = c.BlockCounts(ctx); len(counts) > maxBlockTypes || 4*len(counts) > collected {
				t.Fatalf("BlockCounts returned %d types", len(counts))
			}
		case 2:
			var raw []byte
			if raw, err = c.ReadDiagBufferRaw(ctx); len(raw) > collected {
				t.Fatalf("ReadDiagBufferRaw returned %d octets, at most %d can arrive", len(raw), collected)
			}
		case 3:
			var diag *model.DiagBuffer
			if diag, err = c.ReadDiagBuffer(ctx); diag != nil && (len(diag.Entries)*diagRecordSize > collected || diag.TotalCount > len(diag.Entries)) {
				t.Fatalf("ReadDiagBuffer returned %d entries (total %d)", len(diag.Entries), diag.TotalCount)
			}
		case 4:
			var info *model.DeviceInfo
			if info, err = c.Identify(ctx); info != nil {
				for _, text := range []string{info.OrderNumber, info.SystemName, info.ModuleName, info.PlantID, info.Copyright,
					info.SerialNumber, info.ModuleType, info.CPUType, info.MemoryCardSerial, info.FWVersion} {
					if len(text) > 32 {
						t.Fatalf("Identify returned a text of %d octets", len(text))
					}
				}
			}
		case 5:
			var state model.CPUState
			if state, err = c.GetCPUState(ctx); err != nil && state != model.CPUStateUnknown {
				t.Fatalf("GetCPUState returned %v with %v", state, err)
			}
		case 6:
			var level model.ProtectionLevel
			if level, err = c.GetProtectionLevel(ctx); err != nil && level != model.ProtectionNone {
				t.Fatalf("GetProtectionLevel returned %v with %v", level, err)
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the call did not end: %v", err)
		}
	})
}
