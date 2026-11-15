// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/binary"
	"net"
	"sync"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/wire"
)

// pipePeers is an in-process peer for the dialer options: every dial returns
// one end of an in-memory pipe, and a scripted peer serves the other end
// (COTP accept, then setup and op as in startScriptedPeer). No socket is
// involved, so a benchmark measures this module and go-cotp, and a fuzz
// target needs no port.
type pipePeers struct {
	setup func(pduRef uint16) []byte             // nil: an accepted setup with PDU size 480
	op    func(pduRef uint16, req []byte) []byte // a nil reply ends the connection

	wg   sync.WaitGroup
	mu   sync.Mutex
	open map[net.Conn]struct{} // client ends whose peer has not ended
}

// dial is a Dialer.
func (p *pipePeers) dial(context.Context, string, string) (net.Conn, error) {
	clientEnd, serverEnd := net.Pipe()
	p.mu.Lock()
	if p.open == nil {
		p.open = make(map[net.Conn]struct{})
	}
	p.open[clientEnd] = struct{}{}
	p.mu.Unlock()
	p.wg.Go(func() {
		p.serve(serverEnd)
		p.mu.Lock()
		delete(p.open, clientEnd)
		p.mu.Unlock()
	})
	return clientEnd, nil
}

// wait closes every connection still open and returns when the peers have ended.
func (p *pipePeers) wait() {
	p.mu.Lock()
	for c := range p.open {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *pipePeers) serve(raw net.Conn) {
	defer func() { _ = raw.Close() }()
	ctx := context.Background()
	srv, err := cotp.Accept(ctx, raw, cotp.ServerConfig{MaxTPDULength: 1024})
	if err != nil {
		return
	}
	defer func() { _ = srv.Close() }()
	req, err := srv.ReadTSDU(ctx)
	if err != nil || len(req) < wire.S7HeaderSize {
		return
	}
	ref := binary.BigEndian.Uint16(req[4:6])
	reply := buildS7SetupResponse(ref, 480)
	if p.setup != nil {
		reply = p.setup(ref)
	}
	if err := srv.WriteTSDU(ctx, reply); err != nil {
		return
	}
	for {
		req, err := srv.ReadTSDU(ctx)
		if err != nil || len(req) < wire.S7HeaderSize {
			return
		}
		reply := p.op(binary.BigEndian.Uint16(req[4:6]), req)
		if reply == nil || srv.WriteTSDU(ctx, reply) != nil {
			return
		}
	}
}

// answerReadsAndSZL answers a Read Var request with the bytes it asks for
// and an SZL request with a one-record list of the SZL ID it names.
func answerReadsAndSZL(ref uint16, req []byte) []byte {
	if wire.ROSCTR(req[1]).IsUserdata() && len(req) >= 24 {
		return buildSZLReply(ref, binary.BigEndian.Uint16(req[22:24]), szlList(20, []byte{0x51, 0x44, 0xff, 0x08}))
	}
	if len(req) >= 18 && req[wire.S7HeaderSize] == wire.FuncReadVar {
		return readVarAnswer(ref, make([]byte, binary.BigEndian.Uint16(req[16:18])))
	}
	return buildAckData(ref, 0x81, 0x04, nil, nil)
}
