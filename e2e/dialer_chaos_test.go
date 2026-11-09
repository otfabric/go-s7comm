// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"io"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-s7comm/client"
)

// Transport faults a client.Dialer can inject into the connection of Connect.
const (
	faultNone     = iota
	faultFragment // hands incoming bytes over one at a time
	faultCut      // closes the connection after n incoming bytes
	faultCorrupt  // flips incoming byte n
	faultSwallow  // drops every incoming byte from n on, so the peer seems silent
	faultCutWrite // closes the connection after n outgoing bytes
	faultCount
)

var faultNames = [faultCount]string{"none", "fragment", "cut", "corrupt", "swallow", "cut-write"}

type fault struct {
	kind int
	n    int
}

// faultyConn applies one fault to a real TCP connection while armed is set,
// that is while Connect runs. Reads and writes keep the deadlines go-cotp
// sets, because they go to the underlying connection.
type faultyConn struct {
	net.Conn
	f     fault
	armed *atomic.Bool

	mu      sync.Mutex
	read    int
	written int
}

func (c *faultyConn) Read(b []byte) (int, error) {
	if !c.armed.Load() {
		return c.Conn.Read(b)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.f.kind {
	case faultFragment:
		if len(b) > 1 {
			b = b[:1]
		}
	case faultCut:
		if c.read >= c.f.n {
			_ = c.Close()
			return 0, io.EOF
		}
		if rest := c.f.n - c.read; len(b) > rest {
			b = b[:rest]
		}
	case faultSwallow:
		for c.read >= c.f.n {
			// Discard until the deadline or close ends the read.
			if _, err := c.Conn.Read(b); err != nil {
				return 0, err
			}
		}
		if rest := c.f.n - c.read; len(b) > rest {
			b = b[:rest]
		}
	}
	n, err := c.Conn.Read(b)
	if c.f.kind == faultCorrupt && c.read <= c.f.n && c.f.n < c.read+n {
		b[c.f.n-c.read] ^= 0xA5
	}
	c.read += n
	return n, err
}

func (c *faultyConn) Write(b []byte) (int, error) {
	if !c.armed.Load() {
		return c.Conn.Write(b)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f.kind == faultCutWrite {
		rest := c.f.n - c.written
		if rest <= 0 {
			_ = c.Close()
			return 0, io.ErrClosedPipe
		}
		if len(b) > rest {
			n, _ := c.Conn.Write(b[:rest])
			c.written += n
			_ = c.Close()
			return n, io.ErrClosedPipe
		}
	}
	n, err := c.Conn.Write(b)
	c.written += n
	return n, err
}

// Every Connect goes through a dialer that injects a seeded transport fault
// into the TCP, COTP and S7 setup exchange. Each Connect must come back within
// its timeout, never panic, and a failed reconnect must leave the established
// session usable (API.md: a prior healthy session is left intact until a
// successful swap). goleak (TestMain) checks that no goroutine is left behind.
func TestDialerTransportFaultsDuringConnect(t *testing.T) {
	p := startPLC(t, 240)

	var next atomic.Pointer[fault]
	next.Store(&fault{kind: faultNone})
	// Faults apply only inside Connect: a fault offset past the setup exchange
	// must not reach the session afterwards.
	var armed atomic.Bool
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		raw, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &faultyConn{Conn: raw, f: *next.Load(), armed: &armed}, nil
	}
	const timeout = 200 * time.Millisecond
	c := client.New("127.0.0.1", client.WithPort(p.port()), client.WithRackSlot(0, 1),
		client.WithTimeout(timeout), client.WithDialer(dial))
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("clean Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	readOK := func(i int, what string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		res, err := c.ReadDB(ctx, 1, 0, 16)
		if err != nil || res.Status != client.ReadStatusSuccess {
			t.Fatalf("attempt %d (%s): ReadDB on the session: err=%v res=%+v", i, what, err, res)
		}
	}

	rng := rand.New(rand.NewPCG(5, 6)) // deterministic: a failure replays
	var failed, succeeded [faultCount]int
	for i := range 120 {
		f := fault{kind: 1 + rng.IntN(faultCount-1), n: rng.IntN(80)}
		name := faultNames[f.kind]
		next.Store(&f)
		armed.Store(true)
		start := time.Now()
		err := c.Connect(context.Background())
		armed.Store(false)
		next.Store(&fault{kind: faultNone})
		if d := time.Since(start); d > timeout+time.Second {
			t.Fatalf("attempt %d (%s n=%d): Connect took %v", i, name, f.n, d)
		}
		switch {
		case err != nil:
			failed[f.kind]++
			if f.kind == faultFragment {
				t.Fatalf("attempt %d: Connect over a fragmenting transport: %v", i, err)
			}
			// The failed attempt must not have replaced the established session.
			readOK(i, name)
		case f.kind == faultCorrupt:
			// The flipped byte may lie in a field the client ignores. The session
			// may still be unusable: replace it with a clean one.
			succeeded[f.kind]++
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, rerr := c.ReadDB(ctx, 1, 0, 16)
			cancel()
			if rerr != nil {
				if cerr := c.Connect(context.Background()); cerr != nil {
					t.Fatalf("attempt %d: clean reconnect: %v", i, cerr)
				}
			}
		default:
			// A fault past the end of the setup exchange, or one that only
			// fragments, leaves a working session.
			succeeded[f.kind]++
			readOK(i, name)
		}
	}
	for k := 1; k < faultCount; k++ {
		t.Logf("%-9s failed %3d  connected %3d", faultNames[k], failed[k], succeeded[k])
		if k != faultFragment && failed[k] == 0 {
			t.Errorf("fault %s never made Connect fail: the generator does not reach the handshake", faultNames[k])
		}
	}
}
