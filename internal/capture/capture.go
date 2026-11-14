// SPDX-License-Identifier: MIT

// Package capture reads recorded S7comm traffic for tests: classic pcap and
// pcapng files with Ethernet frames, IPv4 and TCP on port 102. It
// reassembles each TCP direction (retransmitted and duplicate segments are
// dropped), splits the byte stream into TPKT packets (RFC 1006) and joins
// COTP data TPDUs up to the end-of-TSDU mark into whole S7 PDUs.
//
// Some recordings keep only part of a connection's segments (the gymgit
// captures were filtered after recording), so a sequence gap is not an
// error: the bytes on each side of it are split separately, and a TPKT
// packet or COTP sequence cut by the gap is dropped and counted in
// Conversation.Dropped.
//
// It is test infrastructure for the captures in testdata/captures and is not
// part of the public API. It reads files from this repository, not
// untrusted input, but it still checks every length it reads.
package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// S7Port is the ISO-on-TCP port.
const S7Port = 102

// PDU is one S7 PDU (starting with protocol ID 0x32) in a conversation.
type PDU struct {
	FromClient bool   // sent by the side that is not on port 102
	Frame      int    // 1-based number of the frame that completed the PDU
	Data       []byte // the S7 PDU, header included
}

// Conversation is one TCP connection to port 102.
type Conversation struct {
	Client, Server string // "ip:port"
	// COTP connection management seen in either direction.
	ConnectRequests, ConnectConfirms int
	// Gaps is the number of sequence gaps in both directions; Dropped the
	// TPKT packets or COTP sequences lost to them.
	Gaps, Dropped int
	// PDUs in the order they completed. Each direction is ordered by TCP
	// sequence; the two directions are merged by completing frame.
	PDUs []PDU
}

// ErrFormat reports a file that is neither pcap nor pcapng, or is cut short.
var ErrFormat = errors.New("capture: unsupported or malformed capture file")

const (
	linkEthernet = 1
	etherIPv4    = 0x0800
	etherVLAN    = 0x8100
	protoTCP     = 6
)

// ReadFile reads a capture file and returns its S7 conversations, ordered by
// the frame that opened them.
func ReadFile(path string) ([]Conversation, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	return Read(b)
}

// Read parses a capture held in memory.
func Read(b []byte) ([]Conversation, error) {
	frames, err := packets(b)
	if err != nil {
		return nil, err
	}
	a := newAssembler()
	for i, f := range frames {
		if err := a.frame(i+1, f); err != nil {
			return nil, fmt.Errorf("frame %d: %w", i+1, err)
		}
	}
	return a.conversations()
}

// packet is one link-layer frame.
type packet struct {
	link uint32
	data []byte
}

// packets returns the frames of a pcap or pcapng file.
func packets(b []byte) ([]packet, error) {
	if len(b) < 4 {
		return nil, ErrFormat
	}
	switch binary.LittleEndian.Uint32(b) {
	case 0xa1b2c3d4, 0xa1b23c4d:
		return pcapPackets(b, binary.LittleEndian)
	case 0xd4c3b2a1, 0x4d3cb2a1:
		return pcapPackets(b, binary.BigEndian)
	case 0x0a0d0d0a:
		return pcapngPackets(b)
	}
	return nil, ErrFormat
}

func pcapPackets(b []byte, bo binary.ByteOrder) ([]packet, error) {
	if len(b) < 24 {
		return nil, ErrFormat
	}
	link := bo.Uint32(b[20:24]) & 0x0fffffff
	var out []packet
	for off := 24; off < len(b); {
		if len(b)-off < 16 {
			return nil, ErrFormat
		}
		n := int(bo.Uint32(b[off+8 : off+12]))
		off += 16
		if n < 0 || n > len(b)-off {
			return nil, ErrFormat
		}
		out = append(out, packet{link: link, data: b[off : off+n]})
		off += n
	}
	return out, nil
}

func pcapngPackets(b []byte) ([]packet, error) {
	var (
		bo    binary.ByteOrder = binary.LittleEndian
		links []uint32
		out   []packet
	)
	for off := 0; off < len(b); {
		if len(b)-off < 12 {
			return nil, ErrFormat
		}
		typ := bo.Uint32(b[off:])
		if typ == 0x0a0d0d0a { // section header: byte order from its magic
			switch binary.LittleEndian.Uint32(b[off+8:]) {
			case 0x1a2b3c4d:
				bo = binary.LittleEndian
			case 0x4d3c2b1a:
				bo = binary.BigEndian
			default:
				return nil, ErrFormat
			}
			links = links[:0]
		}
		n := int(bo.Uint32(b[off+4:]))
		if n < 12 || n%4 != 0 || n > len(b)-off {
			return nil, ErrFormat
		}
		body := b[off+8 : off+n-4]
		switch typ {
		case 1: // interface description
			if len(body) < 2 {
				return nil, ErrFormat
			}
			links = append(links, uint32(bo.Uint16(body)))
		case 6: // enhanced packet
			if len(body) < 20 {
				return nil, ErrFormat
			}
			ifc := int(bo.Uint32(body))
			capLen := int(bo.Uint32(body[12:]))
			if ifc < 0 || ifc >= len(links) || capLen < 0 || capLen > len(body)-20 {
				return nil, ErrFormat
			}
			out = append(out, packet{link: links[ifc], data: body[20 : 20+capLen]})
		case 3: // simple packet, interface 0
			if len(body) < 4 || len(links) == 0 {
				return nil, ErrFormat
			}
			capLen := min(int(bo.Uint32(body)), len(body)-4)
			if capLen < 0 {
				return nil, ErrFormat
			}
			out = append(out, packet{link: links[0], data: body[4 : 4+capLen]})
		}
		off += n
	}
	return out, nil
}

// segment is the TCP payload of one frame.
type segment struct {
	frame   int
	seq     uint32
	payload []byte
}

// flow is one direction of a TCP connection.
type flow struct {
	src, dst string
	syn      bool
	isn      uint32
	segs     []segment
}

type connKey struct{ client, server string }

type conn struct {
	key        connKey
	firstFrame int
	up, down   *flow // client to server, server to client
}

type assembler struct {
	conns map[connKey]*conn
}

func newAssembler() *assembler { return &assembler{conns: map[connKey]*conn{}} }

// frame takes one link-layer frame; frames that are not IPv4/TCP to or from
// port 102 are ignored.
func (a *assembler) frame(num int, p packet) error {
	if p.link != linkEthernet {
		return fmt.Errorf("link type %d: only Ethernet is supported", p.link)
	}
	d := p.data
	if len(d) < 14 {
		return nil
	}
	etherType := binary.BigEndian.Uint16(d[12:14])
	d = d[14:]
	if etherType == etherVLAN {
		if len(d) < 4 {
			return nil
		}
		etherType = binary.BigEndian.Uint16(d[2:4])
		d = d[4:]
	}
	if etherType != etherIPv4 || len(d) < 20 || d[0]>>4 != 4 {
		return nil
	}
	ihl := int(d[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(d[2:4]))
	if ihl < 20 || total < ihl || total > len(d) || d[9] != protoTCP {
		return nil
	}
	if binary.BigEndian.Uint16(d[6:8])&0x3fff != 0 {
		return fmt.Errorf("fragmented IPv4 is not supported")
	}
	src := fmt.Sprintf("%d.%d.%d.%d", d[12], d[13], d[14], d[15])
	dst := fmt.Sprintf("%d.%d.%d.%d", d[16], d[17], d[18], d[19])
	t := d[ihl:total]
	if len(t) < 20 {
		return nil
	}
	sport, dport := binary.BigEndian.Uint16(t[0:2]), binary.BigEndian.Uint16(t[2:4])
	thl := int(t[12]>>4) * 4
	if thl < 20 || thl > len(t) {
		return nil
	}
	var key connKey
	var toServer bool
	switch {
	case dport == S7Port:
		key, toServer = connKey{fmt.Sprintf("%s:%d", src, sport), fmt.Sprintf("%s:%d", dst, dport)}, true
	case sport == S7Port:
		key = connKey{fmt.Sprintf("%s:%d", dst, dport), fmt.Sprintf("%s:%d", src, sport)}
	default:
		return nil
	}
	c := a.conns[key]
	if c == nil {
		c = &conn{key: key, firstFrame: num, up: &flow{src: key.client, dst: key.server}, down: &flow{src: key.server, dst: key.client}}
		a.conns[key] = c
	}
	f := c.down
	if toServer {
		f = c.up
	}
	seq := binary.BigEndian.Uint32(t[4:8])
	if t[13]&0x02 != 0 { // SYN: data starts at ISN+1; a new SYN starts the flow again
		f.syn, f.isn, f.segs = true, seq, nil
		return nil
	}
	if payload := t[thl:]; len(payload) > 0 {
		f.segs = append(f.segs, segment{frame: num, seq: seq, payload: payload})
	}
	return nil
}

// chunk is reassembled flow data with the frame that delivered each byte.
type chunk struct {
	data   []byte
	frames []int
}

// seqDiff is a - b in TCP sequence space (RFC 1982 serial arithmetic):
// negative when a lies before b.
func seqDiff(a, b uint32) int64 {
	d := int64(a - b)
	if d >= 1<<31 {
		d -= 1 << 32
	}
	return d
}

// stream reassembles a flow in sequence order into runs of contiguous
// bytes. Bytes already delivered (retransmissions, overlaps) are dropped; a
// gap starts a new run.
func (f *flow) stream() []chunk {
	if len(f.segs) == 0 {
		return nil
	}
	base := f.segs[0].seq
	if f.syn {
		base = f.isn + 1
	}
	segs := append([]segment(nil), f.segs...)
	sort.SliceStable(segs, func(i, j int) bool { return seqDiff(segs[i].seq, base) < seqDiff(segs[j].seq, base) })
	out := []chunk{{}}
	var next int64 // offset from base of the next byte expected
	for _, s := range segs {
		start := seqDiff(s.seq, base)
		end := start + int64(len(s.payload))
		if end <= next {
			continue // already delivered
		}
		p := s.payload
		if start > next { // gap: data missing from the capture
			out = append(out, chunk{})
		} else {
			p = p[next-start:]
		}
		cur := &out[len(out)-1]
		cur.data = append(cur.data, p...)
		for range p {
			cur.frames = append(cur.frames, s.frame)
		}
		next = end
	}
	return out
}

// pdus splits a reassembled flow into TPKT packets, counts COTP CR/CC and
// joins DT TPDUs into S7 PDUs.
func pdus(c chunk, fromClient bool) (out []PDU, cr, cc, dropped int, err error) {
	var tsdu []byte
	for off := 0; off < len(c.data); {
		if len(c.data)-off < 4 || int(binary.BigEndian.Uint16(c.data[off+2:])) > len(c.data)-off {
			dropped++ // cut by a gap or the end of the capture
			if len(tsdu) > 0 {
				dropped++ // and the COTP sequence it belonged to
			}
			return out, cr, cc, dropped, nil
		}
		if c.data[off] != 3 {
			return nil, 0, 0, 0, fmt.Errorf("TPKT version %d at stream offset %d", c.data[off], off)
		}
		n := int(binary.BigEndian.Uint16(c.data[off+2:]))
		if n < 7 {
			return nil, 0, 0, 0, fmt.Errorf("TPKT length %d at stream offset %d", n, off)
		}
		tpdu := c.data[off+4 : off+n]
		li := int(tpdu[0])
		if li+1 > len(tpdu) || li < 1 {
			return nil, 0, 0, 0, fmt.Errorf("COTP length indicator %d at stream offset %d", li, off)
		}
		switch tpdu[1] & 0xf0 {
		case 0xe0:
			cr++
		case 0xd0:
			cc++
		case 0xf0:
			if li < 2 {
				return nil, 0, 0, 0, fmt.Errorf("COTP DT length indicator %d at stream offset %d", li, off)
			}
			tsdu = append(tsdu, tpdu[li+1:]...)
			if tpdu[2]&0x80 != 0 { // end of TSDU
				if len(tsdu) > 0 && tsdu[0] == 0x32 {
					out = append(out, PDU{FromClient: fromClient, Frame: c.frames[off+n-1], Data: tsdu})
				}
				tsdu = nil
			}
		}
		off += n
	}
	if len(tsdu) > 0 {
		dropped++ // COTP sequence without its last TPDU
	}
	return out, cr, cc, dropped, nil
}

// flowPDUs runs pdus over every contiguous run of a flow.
func flowPDUs(f *flow, fromClient bool) (out []PDU, cr, cc, gaps, dropped int, err error) {
	runs := f.stream()
	if len(runs) > 0 {
		gaps = len(runs) - 1
	}
	for _, r := range runs {
		p, a, b, d, err := pdus(r, fromClient)
		if err != nil {
			return nil, 0, 0, 0, 0, err
		}
		out, cr, cc, dropped = append(out, p...), cr+a, cc+b, dropped+d
	}
	return out, cr, cc, gaps, dropped, nil
}

func (a *assembler) conversations() ([]Conversation, error) {
	conns := make([]*conn, 0, len(a.conns))
	for _, c := range a.conns {
		conns = append(conns, c)
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].firstFrame < conns[j].firstFrame })
	var out []Conversation
	for _, c := range conns {
		up, cr1, cc1, g1, d1, err := flowPDUs(c.up, true)
		if err != nil {
			return nil, fmt.Errorf("%s -> %s: %w", c.key.client, c.key.server, err)
		}
		down, cr2, cc2, g2, d2, err := flowPDUs(c.down, false)
		if err != nil {
			return nil, fmt.Errorf("%s -> %s: %w", c.key.server, c.key.client, err)
		}
		all := append(up, down...)
		sort.SliceStable(all, func(i, j int) bool { return all[i].Frame < all[j].Frame })
		out = append(out, Conversation{
			Client: c.key.client, Server: c.key.server,
			ConnectRequests: cr1 + cr2, ConnectConfirms: cc1 + cc2,
			Gaps: g1 + g2, Dropped: d1 + d2, PDUs: all,
		})
	}
	return out, nil
}

// Count returns how many PDUs of all conversations satisfy keep.
func Count(convs []Conversation, keep func(PDU) bool) int {
	n := 0
	for _, c := range convs {
		for _, p := range c.PDUs {
			if keep(p) {
				n++
			}
		}
	}
	return n
}
