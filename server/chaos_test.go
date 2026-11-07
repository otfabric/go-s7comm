// SPDX-License-Identifier: MIT

package server

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/otfabric/go-s7comm/wire"
)

// TestChaosRealCPUJobs mutates the jobs real clients send (DBREAD, timers,
// counters, mixed items, setup with AmQ) with a seeded generator: flipped
// bytes, cuts, appended bytes, lying item counts and lengths. Every PDU
// with a readable header must get an Ack-Data answer with its reference,
// never past the PDU, and memory keeps its size and holds no write to
// timers or counters.
func TestChaosRealCPUJobs(t *testing.T) {
	seeds := [][]byte{
		readJob(dbreadItem([3]int{2, 74, 108}, [3]int{4, 76, 404})),
		readJob(any12(transportTimer, 2, 0, wire.AreaTimer, 1), any12(transportCounter, 1, 0, wire.AreaCounter, 0)),
		readJob(dbreadItem([3]int{3, 74, 108}), any12(wire.TransportSizeByte, 2, 76, wire.AreaDB, 404*8)),
		writeJob(any12(transportTimer, 1, 0, wire.AreaTimer, 0), dataItem(wire.DataTransportSizeOctet, 2, 1, 2)),
		must(wire.EncodeSetupCommRequest(1, 1, 3, 480)),
	}
	rng := rand.New(rand.NewPCG(20261107, 1))
	s := realCPUSession(t, 240)
	timers, _ := s.srv.Read(AreaTimers, 0, 0, 8)
	for i := range 20000 {
		req := append([]byte(nil), seeds[rng.IntN(len(seeds))]...)
		switch rng.IntN(6) {
		case 0: // flip bytes
			for range 1 + rng.IntN(4) {
				req[rng.IntN(len(req))] ^= byte(1 + rng.IntN(255))
			}
		case 1: // cut
			req = req[:rng.IntN(len(req))]
		case 2: // trailing bytes
			req = append(req, make([]byte, 1+rng.IntN(8))...)
		case 3: // item count lies
			if len(req) > 11 {
				req[11] = byte(rng.IntN(256))
			}
		case 4: // item or range length lies
			if len(req) > 13 {
				req[13+rng.IntN(len(req)-13)] = byte(rng.IntN(256))
			}
		case 5: // the parameter length lies, kept consistent with the PDU
			if len(req) >= 10 {
				binary.BigEndian.PutUint16(req[6:8], uint16(rng.IntN(len(req)-9)))
				binary.BigEndian.PutUint16(req[8:10], uint16(len(req)-10-int(binary.BigEndian.Uint16(req[6:8]))))
			}
		}
		out, keep := s.handle(req)
		if !keep {
			if out != nil {
				t.Fatalf("mutation %d: answer on a dropped connection", i)
			}
			continue
		}
		h, _, err := wire.ParseS7Header(out)
		if err != nil || h.PDURef != binary.BigEndian.Uint16(req[4:6]) || len(out) > s.pduSize+wire.AckDataHeaderSize {
			t.Fatalf("mutation %d: request % x answer % x (%v)", i, req, out, err)
		}
		if h.ROSCTR != wire.ROSCTRAckData && (h.ROSCTR != wire.ROSCTRUserdata || req[1] != byte(wire.ROSCTRUserdata)) {
			t.Fatalf("mutation %d: answer type %v", i, h.ROSCTR)
		}
	}
	if got, err := s.srv.Read(AreaTimers, 0, 0, 8); err != nil || string(got) != string(timers) {
		t.Fatalf("timers changed by clients: % x, %v", got, err)
	}
	if got, err := s.srv.Read(AreaDB, 76, 0, 410); err != nil || len(got) != 410 {
		t.Fatalf("DB76 lost its size: %v", err)
	}
}
