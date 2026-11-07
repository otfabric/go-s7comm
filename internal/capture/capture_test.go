// SPDX-License-Identifier: MIT

package capture

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The S7 PDU count of every capture equals what Wireshark 4.6.9 decodes
// (tshark -Y 's7comm && !tcp.analysis.retransmission && !tcp.analysis.out_of_order',
// one per s7comm.header.rosctr). Gaps are the segments the gymgit captures
// leave out; none of them cuts a PDU.
func TestCapturesMatchWireshark(t *testing.T) {
	want := map[string]struct{ pdus, convs, cr, gaps int }{
		"libnodave_s7-300_benchmark.pcap":               {10006, 3, 1, 0},
		"libnodave_s7-300_read-write-var.pcap":          {16, 2, 1, 0},
		"snap7_s7-300_client-demo.pcapng":               {64, 1, 0, 0},
		"snap7_s7-300_plc-stop.pcapng":                  {2, 1, 0, 0},
		"snap7_s7-300_read-var.pcapng":                  {2, 1, 0, 0},
		"snap7_s7-300_setup-communication.pcapng":       {2, 1, 0, 0},
		"step7-v5.5_s7-300_auth-password.pcapng":        {6, 1, 0, 2},
		"step7-v5.5_s7-300_copy-ram-to-rom.pcapng":      {2, 1, 0, 0},
		"step7-v5.5_s7-300_download-blocks.pcapng":      {96, 1, 0, 59},
		"step7-v5.5_s7-300_plc-stop.pcapng":             {4, 1, 0, 0},
		"step7-v5.5_s7-300_read-diag-data.pcapng":       {410, 1, 0, 0},
		"step7-v5.5_s7-300_read-var-table.pcapng":       {331, 1, 0, 0},
		"step7-v5.5_s7-300_read-write-var-table.pcapng": {16, 1, 0, 0},
		"tia-v13_s7-300_download-hw-config.pcapng":      {154, 6, 0, 90},
		"tia-v13_s7-300_download-ob1.pcapng":            {106, 1, 0, 74},
		"tia-v13_s7-300_flash-led.pcapng":               {4, 1, 0, 1},
		"tia-v13_s7-300_go-online.pcapng":               {448, 1, 0, 223},
		"unknown_s7-300_download-db1.pcap":              {48, 4, 3, 0},
		"unknown_s7-300_list-blocks.pcap":               {70, 6, 5, 0},
		"unknown_s7-300_read-plc-status.pcap":           {144, 3, 1, 0},
		"unknown_s7-300_read-set-clock.pcap":            {26, 2, 1, 0},
		"wincc-advanced_s7-300_alarm-read-1.pcapng":     {128, 1, 0, 41},
		"wincc-advanced_s7-300_alarm-read-2.pcapng":     {486, 1, 0, 235},
		"wincc-advanced_s7-300_alarm-read-write.pcapng": {164, 1, 0, 79},
		"wincc-advanced_s7-400_production.pcapng":       {1658, 1, 0, 423},
	}
	files, err := filepath.Glob("../../testdata/captures/*/*.pcap*")
	if err != nil || len(files) != len(want) {
		t.Fatalf("%d capture files, %v; want %d", len(files), err, len(want))
	}
	total := 0
	for _, f := range files {
		convs, err := ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var cr, cc, gaps, dropped int
		for _, c := range convs {
			cr, cc, gaps, dropped = cr+c.ConnectRequests, cc+c.ConnectConfirms, gaps+c.Gaps, dropped+c.Dropped
			if !strings.HasSuffix(c.Server, ":102") {
				t.Errorf("%s: server %s", f, c.Server)
			}
		}
		n := Count(convs, func(PDU) bool { return true })
		total += n
		w := want[filepath.Base(f)]
		if n != w.pdus || len(convs) != w.convs || cr != w.cr || cc != w.cr || gaps != w.gaps || dropped != 0 {
			t.Errorf("%s: %d PDUs, %d conversations, CR %d CC %d, %d gaps, %d dropped; want %+v",
				filepath.Base(f), n, len(convs), cr, cc, gaps, dropped, w)
		}
	}
	if total != 14393 {
		t.Fatalf("%d PDUs in all, want 14393", total)
	}
}

// --- synthetic captures -------------------------------------------------

type seg struct {
	src, dst     [4]byte
	sport, dport uint16
	seq          uint32
	syn          bool
	payload      []byte
	vlan         bool
	fragment     bool
	proto        byte
}

var (
	cli = [4]byte{10, 0, 0, 1}
	plc = [4]byte{10, 0, 0, 2}
)

func up(seq uint32, p []byte) seg {
	return seg{src: cli, dst: plc, sport: 4000, dport: S7Port, seq: seq, payload: p}
}

func down(seq uint32, p []byte) seg {
	return seg{src: plc, dst: cli, sport: S7Port, dport: 4000, seq: seq, payload: p}
}

func (s seg) frame() []byte {
	tcp := make([]byte, 20, 20+len(s.payload))
	binary.BigEndian.PutUint16(tcp[0:2], s.sport)
	binary.BigEndian.PutUint16(tcp[2:4], s.dport)
	binary.BigEndian.PutUint32(tcp[4:8], s.seq)
	tcp[12] = 5 << 4
	tcp[13] = 0x18
	if s.syn {
		tcp[13] = 0x02
	}
	tcp = append(tcp, s.payload...)
	ip := make([]byte, 20, 20+len(tcp))
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+len(tcp)))
	if s.fragment {
		binary.BigEndian.PutUint16(ip[6:8], 0x2000)
	}
	ip[9] = 6
	if s.proto != 0 {
		ip[9] = s.proto
	}
	copy(ip[12:16], s.src[:])
	copy(ip[16:20], s.dst[:])
	ip = append(ip, tcp...)
	eth := make([]byte, 12)
	if s.vlan {
		eth = append(eth, 0x81, 0x00, 0x00, 0x01)
	}
	return append(append(eth, 0x08, 0x00), ip...)
}

// tpkt wraps a COTP TPDU.
func tpkt(cotp []byte) []byte {
	out := []byte{3, 0, 0, 0}
	binary.BigEndian.PutUint16(out[2:4], uint16(4+len(cotp)))
	return append(out, cotp...)
}

// dt is a COTP data TPDU; last marks the end of the TSDU.
func dt(last bool, data []byte) []byte {
	eot := byte(0x00)
	if last {
		eot = 0x80
	}
	return tpkt(append([]byte{0x02, 0xF0, eot}, data...))
}

var s7 = []byte{0x32, 0x01, 0, 0, 0, 1, 0, 0, 0, 0}

func pcapFile(bo binary.ByteOrder, link uint32, frames ...[]byte) []byte {
	h := make([]byte, 24)
	bo.PutUint32(h[0:4], 0xa1b2c3d4)
	bo.PutUint32(h[20:24], link)
	for _, f := range frames {
		r := make([]byte, 16)
		bo.PutUint32(r[8:12], uint32(len(f)))
		bo.PutUint32(r[12:16], uint32(len(f)))
		h = append(append(h, r...), f...)
	}
	return h
}

func ngBlock(bo binary.ByteOrder, typ uint32, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	b := make([]byte, 8, 12+len(body))
	bo.PutUint32(b[0:4], typ)
	bo.PutUint32(b[4:8], uint32(12+len(body)))
	b = append(b, body...)
	tail := make([]byte, 4)
	bo.PutUint32(tail, uint32(12+len(body)))
	return append(b, tail...)
}

func pcapngFile(bo binary.ByteOrder, frames ...[]byte) []byte {
	shb := make([]byte, 16)
	bo.PutUint32(shb[0:4], 0x1a2b3c4d)
	bo.PutUint16(shb[4:6], 1)
	idb := make([]byte, 8)
	bo.PutUint16(idb[0:2], 1)
	out := append(ngBlock(bo, 0x0a0d0d0a, shb), ngBlock(bo, 1, idb)...)
	for i, f := range frames {
		if i%2 == 1 { // simple packet block
			spb := make([]byte, 4)
			bo.PutUint32(spb, uint32(len(f)))
			out = append(out, ngBlock(bo, 3, append(spb, f...))...)
			continue
		}
		epb := make([]byte, 20)
		bo.PutUint32(epb[12:16], uint32(len(f)))
		bo.PutUint32(epb[16:20], uint32(len(f)))
		out = append(out, ngBlock(bo, 6, append(epb, f...))...)
	}
	return out
}

func frames(segs ...seg) [][]byte {
	out := make([][]byte, len(segs))
	for i, s := range segs {
		out[i] = s.frame()
	}
	return out
}

func TestReadFormats(t *testing.T) {
	conversation := frames(
		seg{src: cli, dst: plc, sport: 4000, dport: S7Port, seq: 99, syn: true},
		up(100, tpkt([]byte{0x06, 0xE0, 0, 0, 0, 1, 0})),                                           // COTP CR
		down(500, tpkt([]byte{0x06, 0xD0, 0, 1, 0, 1, 0})),                                         // COTP CC
		up(111, append(dt(false, s7[:4]), dt(true, s7[4:])...)),                                    // TSDU over two DT TPDUs
		down(511, dt(true, append([]byte{}, s7...))),                                               // answer
		seg{src: cli, dst: plc, sport: 4000, dport: 80, seq: 1, payload: []byte{1}},                // other port
		seg{src: cli, dst: plc, sport: 4000, dport: S7Port, seq: 1, proto: 17, payload: []byte{1}}, // UDP
	)
	for name, file := range map[string][]byte{
		"pcap little-endian":   pcapFile(binary.LittleEndian, 1, conversation...),
		"pcap big-endian":      pcapFile(binary.BigEndian, 1, conversation...),
		"pcapng little-endian": pcapngFile(binary.LittleEndian, conversation...),
		"pcapng big-endian":    pcapngFile(binary.BigEndian, conversation...),
	} {
		t.Run(name, func(t *testing.T) {
			convs, err := Read(file)
			if err != nil {
				t.Fatal(err)
			}
			if len(convs) != 1 {
				t.Fatalf("%d conversations", len(convs))
			}
			c := convs[0]
			if c.Client != "10.0.0.1:4000" || c.Server != "10.0.0.2:102" || c.ConnectRequests != 1 || c.ConnectConfirms != 1 || c.Gaps != 0 {
				t.Fatalf("conversation %+v", c)
			}
			if len(c.PDUs) != 2 || !c.PDUs[0].FromClient || c.PDUs[1].FromClient || c.PDUs[0].Frame != 4 || string(c.PDUs[0].Data) != string(s7) {
				t.Fatalf("PDUs %+v", c.PDUs)
			}
		})
	}
}

func TestReassembly(t *testing.T) {
	a, b := dt(true, s7), dt(true, s7)
	convs, err := Read(pcapFile(binary.LittleEndian, 1, frames(
		seg{src: cli, dst: plc, sport: 4000, dport: S7Port, seq: 9, vlan: true, payload: a}, // VLAN tag
		up(9, a), // retransmission: dropped
		up(9+uint32(len(a))-3, append(a[len(a)-3:], b...)), // overlap: only new bytes kept
		up(1000, dt(false, s7)),                            // gap, then a TSDU without its end
		up(1000+uint32(len(a)), []byte{3, 0, 0, 40, 2}),    // a TPKT cut by the end of the capture
	)...))
	if err != nil {
		t.Fatal(err)
	}
	c := convs[0]
	if len(c.PDUs) != 2 || c.Gaps != 1 || c.Dropped != 2 {
		t.Fatalf("%d PDUs, %d gaps, %d dropped; want 2, 1, 2", len(c.PDUs), c.Gaps, c.Dropped)
	}
	if n := Count(convs, func(p PDU) bool { return p.FromClient }); n != 2 {
		t.Fatalf("Count = %d", n)
	}
}

func TestReadErrors(t *testing.T) {
	good := up(1, dt(true, s7)).frame()
	ipFragment := seg{src: cli, dst: plc, sport: 4000, dport: S7Port, seq: 1, fragment: true, payload: []byte{1}}.frame()
	bad := func(p []byte) []byte { return pcapFile(binary.LittleEndian, 1, up(1, p).frame()) }
	for name, file := range map[string][]byte{
		"empty":                  nil,
		"unknown magic":          {1, 2, 3, 4, 5},
		"pcap header cut":        pcapFile(binary.LittleEndian, 1)[:20],
		"pcap record header cut": append(pcapFile(binary.LittleEndian, 1), 0, 0),
		"pcap record overrun":    pcapFile(binary.LittleEndian, 1, good)[:40],
		"not Ethernet":           pcapFile(binary.LittleEndian, 113, good),
		"IPv4 fragment":          pcapFile(binary.LittleEndian, 1, ipFragment),
		"TPKT version":           bad([]byte{4, 0, 0, 7, 2, 0xF0, 0x80}),
		"TPKT length":            bad([]byte{3, 0, 0, 5, 2}),
		"COTP length":            bad([]byte{3, 0, 0, 7, 9, 0xF0, 0x80}),
		"COTP DT length":         bad([]byte{3, 0, 0, 7, 1, 0xF0, 0x80}),
		"pcapng block cut":       pcapngFile(binary.LittleEndian)[:30],
		"pcapng bad block len":   append(pcapngFile(binary.LittleEndian), 6, 0, 0, 0, 13, 0, 0, 0, 0, 0, 0, 0),
		"pcapng byte order":      ngBlock(binary.LittleEndian, 0x0a0d0d0a, make([]byte, 16)),
		"pcapng no interface":    append(ngBlock(binary.LittleEndian, 0x0a0d0d0a, []byte{0x4d, 0x3c, 0x2b, 0x1a, 0, 0, 0, 0}), ngBlock(binary.LittleEndian, 6, make([]byte, 20))...),
		"pcapng short IDB":       append(pcapngFile(binary.LittleEndian), ngBlock(binary.LittleEndian, 1, nil)...),
		"pcapng short EPB":       append(pcapngFile(binary.LittleEndian), ngBlock(binary.LittleEndian, 6, make([]byte, 8))...),
		"pcapng short SPB":       append(pcapngFile(binary.LittleEndian), ngBlock(binary.LittleEndian, 3, nil)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(file); err == nil {
				t.Fatal("no error")
			}
		})
	}
	if _, err := Read([]byte{1, 2, 3}); !errors.Is(err, ErrFormat) {
		t.Fatalf("err = %v, want ErrFormat", err)
	}
	if _, err := ReadFile("does-not-exist.pcap"); err == nil {
		t.Fatal("missing file read")
	}
}

// Frames that carry no S7 traffic are ignored, not errors.
func TestIgnoredFrames(t *testing.T) {
	short := []byte{1, 2, 3}
	arp := append(make([]byte, 12), 0x08, 0x06, 0)
	vlanCut := append(make([]byte, 12), 0x81, 0x00, 0)
	ipv6 := append(make([]byte, 12), 0x86, 0xdd)
	ipShort := append(make([]byte, 12), 0x08, 0x00, 0x45)
	tcpShort := up(1, nil).frame()[:14+20+10]
	badIHL := up(1, nil).frame()
	badIHL[14] = 0x41
	tcpHeaderLen := up(1, nil).frame()
	tcpHeaderLen[14+20+12] = 0xF0
	convs, err := Read(pcapFile(binary.LittleEndian, 1, short, arp, vlanCut, ipv6, ipShort, tcpShort, badIHL, tcpHeaderLen))
	if err != nil {
		t.Fatal(err)
	}
	if Count(convs, func(PDU) bool { return true }) != 0 {
		t.Fatalf("PDUs from frames without S7 traffic: %+v", convs)
	}
}

func FuzzRead(f *testing.F) {
	f.Add(pcapFile(binary.LittleEndian, 1, up(1, dt(true, s7)).frame()))
	f.Add(pcapngFile(binary.BigEndian, down(1, dt(true, s7)).frame()))
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = Read(b) })
}
