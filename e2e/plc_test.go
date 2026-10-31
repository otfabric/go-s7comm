// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/wire"
)

// plc is a simulated PLC: it holds memory and answers S7 requests by decoding
// them, so that a value read back has travelled through both directions of
// the client's codec and the real transport. Addresses it does not hold are
// refused with an item return code, as a PLC does.
type plc struct {
	t       *testing.T
	ln      net.Listener
	pduSize int

	// mode changes what the PLC does with the next job.
	mode atomic.Int32

	mu   sync.Mutex
	dbs  map[int][]byte
	area map[byte][]byte

	wg    sync.WaitGroup
	conns sync.Map // net.Conn -> struct{}
}

const (
	modeAnswer  = iota
	modeSilent  // reads jobs and never answers
	modeHangUp  // closes the connection on a job
	modeGarbage // answers a job with a PDU of another protocol
)

func startPLC(t *testing.T, pduSize int) *plc {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &plc{
		t: t, ln: ln, pduSize: pduSize,
		dbs:  map[int][]byte{1: make([]byte, 1024)},
		area: map[byte][]byte{wire.AreaMerkers: make([]byte, 256), wire.AreaInputs: make([]byte, 16), wire.AreaOutputs: make([]byte, 16)},
	}
	p.wg.Add(1)
	go p.accept()
	t.Cleanup(p.stop)
	return p
}

func (p *plc) port() int { return p.ln.Addr().(*net.TCPAddr).Port }

func (p *plc) stop() {
	_ = p.ln.Close()
	p.conns.Range(func(k, _ any) bool {
		_ = k.(net.Conn).Close()
		return true
	})
	p.wg.Wait()
}

func (p *plc) accept() {
	defer p.wg.Done()
	for {
		raw, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.conns.Store(raw, struct{}{})
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer p.conns.Delete(raw)
			defer func() { _ = raw.Close() }()
			p.serve(raw)
		}()
	}
}

func (p *plc) serve(raw net.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := cotp.Accept(ctx, raw, cotp.ServerConfig{MaxTPDULength: 1024})
	cancel()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	ctx = context.Background()
	for {
		req, err := conn.ReadTSDU(ctx)
		if err != nil {
			return
		}
		h, body, err := wire.ParseS7Header(req)
		if err != nil {
			return
		}
		if body[0] != wire.FuncSetupComm {
			switch p.mode.Load() {
			case modeSilent:
				continue
			case modeHangUp:
				return
			case modeGarbage:
				if conn.WriteTSDU(ctx, []byte{0x72, 0x01, 0x00, 0x00}) != nil {
					return
				}
				continue
			}
		}
		if conn.WriteTSDU(ctx, p.answer(h, body)) != nil {
			return
		}
	}
}

func (p *plc) answer(h *wire.S7Header, body []byte) []byte {
	param, data := body[:h.ParamLength], body[h.ParamLength:]
	if h.ROSCTR == wire.ROSCTRUserdata {
		return p.szl(h.PDURef, data)
	}
	switch param[0] {
	case wire.FuncSetupComm:
		size := int(binary.BigEndian.Uint16(param[6:8]))
		if size > p.pduSize {
			size = p.pduSize
		}
		out := append([]byte(nil), param...)
		binary.BigEndian.PutUint16(out[6:8], uint16(size))
		return ack(h.PDURef, out, nil)
	case wire.FuncReadVar:
		return p.read(h.PDURef, param)
	case wire.FuncWriteVar:
		return p.write(h.PDURef, param, data)
	}
	return ack(h.PDURef, param[:2], []byte{wire.RetCodeNotAvailable})
}

// item is a decoded S7ANY specification.
type item struct {
	bit      bool
	count    int
	db       int
	area     byte
	startBit int
}

func decodeItem(b []byte) item {
	return item{
		bit:      b[3] == wire.TransportSizeBit,
		count:    int(binary.BigEndian.Uint16(b[4:6])),
		db:       int(binary.BigEndian.Uint16(b[6:8])),
		area:     b[8],
		startBit: int(b[9])<<16 | int(b[10])<<8 | int(b[11]),
	}
}

// memory returns what the address refers to, or the return code that refuses it.
func (p *plc) memory(it item) ([]byte, byte) {
	mem, ok := p.area[it.area]
	if it.area == wire.AreaDB {
		mem, ok = p.dbs[it.db]
	}
	if !ok {
		return nil, wire.RetCodeNotAvailable
	}
	end := it.startBit/8 + it.count
	if it.bit {
		end = it.startBit/8 + 1
	}
	if end > len(mem) {
		return nil, wire.RetCodeAddressFault
	}
	return mem, wire.RetCodeSuccess
}

func (p *plc) read(ref uint16, param []byte) []byte {
	it := decodeItem(param[2:14])
	p.mu.Lock()
	defer p.mu.Unlock()
	mem, code := p.memory(it)
	if code != wire.RetCodeSuccess {
		return ack(ref, param[:2], []byte{code, 0x00, 0x00, 0x00})
	}
	if it.bit {
		v := mem[it.startBit/8] >> (it.startBit % 8) & 1
		return ack(ref, param[:2], []byte{wire.RetCodeSuccess, wire.DataTransportSizeBit, 0x00, 0x01, v, 0x00})
	}
	start := it.startBit / 8
	out := make([]byte, 4, 4+it.count)
	out[0], out[1] = wire.RetCodeSuccess, byte(wire.RespTransportSizeWord)
	binary.BigEndian.PutUint16(out[2:4], uint16(it.count*8))
	return ack(ref, param[:2], append(out, mem[start:start+it.count]...))
}

func (p *plc) write(ref uint16, param, data []byte) []byte {
	it := decodeItem(param[2:14])
	p.mu.Lock()
	defer p.mu.Unlock()
	mem, code := p.memory(it)
	if code == wire.RetCodeSuccess {
		if it.bit {
			mask := byte(1) << (it.startBit % 8)
			mem[it.startBit/8] &^= mask
			if data[4] != 0 {
				mem[it.startBit/8] |= mask
			}
		} else {
			copy(mem[it.startBit/8:], data[4:4+it.count])
		}
	}
	return ack(ref, param[:2], []byte{code})
}

// szl answers an SZL read with the CPU state list: RUN.
func (p *plc) szl(ref uint16, data []byte) []byte {
	id := binary.BigEndian.Uint16(data[4:6])
	payload := []byte{0x00, 0x00, 0x08, 0x00}
	out := make([]byte, 8, 8+len(payload))
	out[0], out[1] = wire.RetCodeSuccess, 0x09
	binary.BigEndian.PutUint16(out[2:4], uint16(4+len(payload)))
	binary.BigEndian.PutUint16(out[4:6], id)
	out = append(out, payload...)

	param := []byte{0x00, 0x01, 0x12, 0x08, 0x12, 0x84, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}
	hdr := wire.EncodeS7Header(wire.ROSCTRUserdata, ref, len(param), len(out))
	return append(append(hdr, param...), out...)
}

// ack builds an Ack-Data PDU without an error.
func ack(ref uint16, param, data []byte) []byte {
	out := make([]byte, 12, 12+len(param)+len(data))
	out[0], out[1] = 0x32, byte(wire.ROSCTRAckData)
	binary.BigEndian.PutUint16(out[4:6], ref)
	binary.BigEndian.PutUint16(out[6:8], uint16(len(param)))
	binary.BigEndian.PutUint16(out[8:10], uint16(len(data)))
	return append(append(out, param...), data...)
}
