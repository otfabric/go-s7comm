// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/model"
)

// unroutable is a TEST-NET-1 address: only the dialer reaches the PLC, the
// way a proxy would, so a scan that bypassed the dialer would fail.
const unroutable = "192.0.2.1"

// scanFaultDialer redirects every dial to the PLC and wraps the connection
// with the current fault for its whole life. Each connection counts its own
// bytes, so every rack/slot attempt of a call meets the same fault.
type scanFaultDialer struct {
	target string
	cur    atomic.Pointer[fault]
	dials  atomic.Int32
	wrong  atomic.Int32 // dials to an address other than want
	want   string
}

func newScanFaultDialer(p *plc, want string) *scanFaultDialer {
	d := &scanFaultDialer{target: net.JoinHostPort("127.0.0.1", strconv.Itoa(p.port())), want: want}
	d.cur.Store(&fault{kind: faultNone})
	return d
}

func (d *scanFaultDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.dials.Add(1)
	if addr != d.want {
		d.wrong.Add(1)
	}
	var nd net.Dialer
	raw, err := nd.DialContext(ctx, network, d.target)
	if err != nil {
		return nil, err
	}
	armed := new(atomic.Bool)
	armed.Store(true)
	return &faultyConn{Conn: raw, f: *d.cur.Load(), armed: armed}, nil
}

func (d *scanFaultDialer) check(t *testing.T) {
	t.Helper()
	if d.dials.Load() == 0 {
		t.Fatal("the scan never used the dialer")
	}
	if n := d.wrong.Load(); n > 0 {
		t.Errorf("%d dials asked for another address than %s", n, d.want)
	}
}

// reportFaults logs the outcome per fault kind and fails when a fault other
// than fragmentation never made a call fail: the generator would then not
// reach the exchange.
func reportFaults(t *testing.T, failed, succeeded *[faultCount]int) {
	t.Helper()
	for k := 1; k < faultCount; k++ {
		t.Logf("%-9s failed %3d  succeeded %3d", faultNames[k], failed[k], succeeded[k])
		if k != faultFragment && failed[k] == 0 {
			t.Errorf("fault %s never made a call fail", faultNames[k])
		}
	}
}

// Discover through a dialer whose connections carry one seeded transport
// fault each call. Each call returns within its attempts' timeouts, a
// fragmenting transport still finds the device, and a found device reports
// the negotiated PDU size unless a byte was flipped. goleak (TestMain) checks
// that the workers are gone.
func TestScanDialerFaultsDuringDiscover(t *testing.T) {
	p := startPLC(t, 240)
	d := newScanFaultDialer(p, net.JoinHostPort(unroutable, "102"))
	const timeout = 200 * time.Millisecond
	rng := rand.New(rand.NewPCG(7, 8))
	var failed, succeeded [faultCount]int
	for i := range 60 {
		f := fault{kind: 1 + rng.IntN(faultCount-1), n: rng.IntN(60)}
		d.cur.Store(&f)
		start := time.Now()
		res, err := client.Discover(context.Background(), unroutable+"/32",
			client.WithDiscoverRackSlotRange(0, 0, 1, 2),
			client.WithDiscoverTimeout(int(timeout/time.Millisecond)),
			client.WithDiscoverDialer(d.dial),
		)
		if err != nil {
			t.Fatalf("attempt %d (%s n=%d): Discover: %v", i, faultNames[f.kind], f.n, err)
		}
		if el := time.Since(start); el > 2*timeout+time.Second {
			t.Fatalf("attempt %d (%s n=%d): Discover took %v", i, faultNames[f.kind], f.n, el)
		}
		if len(res) != 1 {
			t.Fatalf("attempt %d: %d results, want 1", i, len(res))
		}
		r := res[0]
		if !r.IsS7 {
			failed[f.kind]++
			if f.kind == faultFragment {
				t.Fatalf("attempt %d: Discover over a fragmenting transport found nothing: %s", i, r.Error)
			}
			continue
		}
		succeeded[f.kind]++
		if f.kind != faultCorrupt && r.PDUSize != 240 {
			t.Errorf("attempt %d (%s n=%d): PDU size %d, want 240", i, faultNames[f.kind], f.n, r.PDUSize)
		}
	}
	d.check(t)
	reportFaults(t, &failed, &succeeded)
}

// ProbeRackSlots (strict, so the SZL query runs too) through a faulting
// dialer. Each call returns within its timeouts. The dial itself always
// succeeds, so no candidate may be classified at the TCP stage; a failed
// candidate carries a stage and an error. A fragmenting transport confirms
// every candidate by query.
func TestScanDialerFaultsDuringProbe(t *testing.T) {
	p := startPLC(t, 240)
	d := newScanFaultDialer(p, net.JoinHostPort(unroutable, "102"))
	const timeout = 200 * time.Millisecond
	rng := rand.New(rand.NewPCG(9, 10))
	var failed, succeeded [faultCount]int
	for i := range 60 {
		f := fault{kind: 1 + rng.IntN(faultCount-1), n: rng.IntN(90)}
		d.cur.Store(&f)
		start := time.Now()
		res, err := client.ProbeRackSlots(context.Background(), client.RackSlotProbeRequest{
			Address: unroutable, Port: 102,
			RackMin: 0, RackMax: 0, SlotMin: 1, SlotMax: 2,
			Timeout: timeout, Parallelism: 2, Strict: true,
			Dialer: d.dial,
		})
		if err != nil {
			t.Fatalf("attempt %d (%s n=%d): ProbeRackSlots: %v", i, faultNames[f.kind], f.n, err)
		}
		// Per candidate: COTP, setup and query each get the timeout.
		if el := time.Since(start); el > 3*timeout+time.Second {
			t.Fatalf("attempt %d (%s n=%d): ProbeRackSlots took %v", i, faultNames[f.kind], f.n, el)
		}
		if len(res.Candidates) != 2 {
			t.Fatalf("attempt %d: %d candidates, want 2", i, len(res.Candidates))
		}
		ok := true
		for _, c := range res.Candidates {
			if c.Status == client.StatusValidQuery {
				continue
			}
			ok = false
			if f.kind == faultFragment {
				t.Fatalf("attempt %d: fragmenting transport: candidate %+v", i, c)
			}
			if c.Stage == client.ProbeStageTCP || c.Stage == "" {
				t.Errorf("attempt %d (%s n=%d): candidate stage %q, want cotp, setup or query", i, faultNames[f.kind], f.n, c.Stage)
			}
			if c.Error == "" {
				t.Errorf("attempt %d (%s n=%d): failed candidate %+v has no error", i, faultNames[f.kind], f.n, c)
			}
		}
		if ok {
			succeeded[f.kind]++
		} else {
			failed[f.kind]++
		}
	}
	d.check(t)
	reportFaults(t, &failed, &succeeded)
}

// CompareRead through a faulting dialer, with no deadline on ctx: Timeout
// bounds each candidate's connect and its read, so every call must return
// within twice the timeout even when the peer goes silent. A candidate that succeeds returns the PLC
// memory unless a byte was flipped (S7 has no checksum), and
// RackSlotInsensitive is never true when a candidate failed.
func TestScanDialerFaultsDuringCompareRead(t *testing.T) {
	p := startPLC(t, 240)
	want := []byte("scan dialer test")
	p.mu.Lock()
	copy(p.dbs[1][32:], want)
	p.mu.Unlock()
	d := newScanFaultDialer(p, net.JoinHostPort(unroutable, "102"))
	const timeout = 200 * time.Millisecond
	rng := rand.New(rand.NewPCG(11, 12))
	var failed, succeeded [faultCount]int
	for i := range 60 {
		f := fault{kind: 1 + rng.IntN(faultCount-1), n: rng.IntN(110)}
		d.cur.Store(&f)
		start := time.Now()
		res, err := client.CompareRead(context.Background(), client.CompareReadRequest{
			Address:    unroutable,
			Candidates: []client.RackSlot{{Rack: 0, Slot: 1}, {Rack: 0, Slot: 2}},
			Area:       model.AreaDB, DBNumber: 1, Offset: 32, Size: len(want),
			Timeout: timeout, Parallelism: 2,
			Dialer: d.dial,
		})
		if err != nil {
			t.Fatalf("attempt %d (%s n=%d): CompareRead: %v", i, faultNames[f.kind], f.n, err)
		}
		if el := time.Since(start); el > 2*timeout+time.Second {
			t.Fatalf("attempt %d (%s n=%d): CompareRead took %v", i, faultNames[f.kind], f.n, el)
		}
		ok := true
		for _, c := range res.ByCandidate {
			if c.Result.Status != client.ReadStatusSuccess {
				ok = false
				if f.kind == faultFragment {
					t.Fatalf("attempt %d: fragmenting transport: candidate %d/%d: %s", i, c.Rack, c.Slot, c.Result.Message)
				}
				continue
			}
			if f.kind != faultCorrupt && !bytes.Equal(c.Result.Data, want) {
				t.Errorf("attempt %d (%s n=%d): candidate %d/%d read %q, want %q", i, faultNames[f.kind], f.n, c.Rack, c.Slot, c.Result.Data, want)
			}
		}
		if !ok && res.RackSlotInsensitive {
			t.Errorf("attempt %d: RackSlotInsensitive with a failed candidate", i)
		}
		if ok {
			succeeded[f.kind]++
		} else {
			failed[f.kind]++
		}
	}
	d.check(t)
	reportFaults(t, &failed, &succeeded)
}
