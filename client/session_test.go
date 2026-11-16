// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-cotp"
	"github.com/otfabric/go-s7comm/model"
	"github.com/otfabric/go-s7comm/wire"
)

// Contract tests of client.Client (TESTING.md, "Lifecycle contract
// matrix"). The peer and the test agree on each step through channels: a
// request that arrived, an answer that is held back, a call that returned.
// Every wait is bounded by waitFor.

// waitFor fails the test when ch does not deliver within the bound.
func waitFor[T any](t *testing.T, ch <-chan T, what string) (v T) {
	t.Helper()
	select {
	case v = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	return v
}

// expiringContext is a context the test expires by hand. After that its Err
// is context.DeadlineExceeded, as for a deadline that has passed, at the
// moment the test chooses and without a timer.
type expiringContext struct {
	context.Context
	done chan struct{}
}

func newExpiringContext() (ctx *expiringContext, expire func()) {
	ctx = &expiringContext{Context: context.Background(), done: make(chan struct{})}
	return ctx, sync.OnceFunc(func() { close(ctx.done) })
}

func (c *expiringContext) Done() <-chan struct{} { return c.done }

func (c *expiringContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// heldPeer is a scripted peer that reports each request on arrived and holds
// its first answer until release is called; later answers are not held.
type heldPeer struct {
	port     int
	arrived  chan struct{}
	release  func()
	requests atomic.Int32
}

// cpuStateRun answers any request with SZL 0x0424 in mode RUN.
func cpuStateRun(ref uint16, _ []byte) []byte {
	return buildSZLReply(ref, wire.SZLCPUState, szlList(20, []byte{0x51, 0x44, 0xff, 0x08}))
}

func startHeldPeer(t *testing.T, answer func(ref uint16, req []byte) []byte) *heldPeer {
	t.Helper()
	p := &heldPeer{arrived: make(chan struct{}, 64)}
	gate := make(chan struct{})
	p.release = sync.OnceFunc(func() { close(gate) })
	p.port = startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
		p.requests.Add(1)
		p.arrived <- struct{}{}
		<-gate
		return answer(ref, req)
	})
	t.Cleanup(p.release) // before the peer's own cleanup, which waits for it
	return p
}

// closeCountingDialer dials TCP and counts how often each connection it
// returned was closed.
type closeCountingDialer struct {
	dials  atomic.Int32
	closes atomic.Int32
}

type closeCountingConn struct {
	net.Conn
	closes *atomic.Int32
}

func (c *closeCountingConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

func (d *closeCountingDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	d.dials.Add(1)
	return &closeCountingConn{Conn: conn, closes: &d.closes}, nil
}

// requireDisconnected checks that c holds no session: the negotiated values
// are zero and an operation returns ErrNotConnected.
func requireDisconnected(t *testing.T, c *Client) {
	t.Helper()
	if info := c.ConnectionInfo(); info.PDUSize != 0 || info.LocalTSAP != 0 || info.RemoteTSAP != 0 || info.Rack != 0 || info.Slot != 0 || info.MaxAmqCalling != 0 || info.MaxAmqCalled != 0 {
		t.Errorf("session state left: %+v", info)
	}
	if _, err := c.GetCPUState(testContext(t)); !errors.Is(err, ErrNotConnected) {
		t.Errorf("next call: err = %v, want ErrNotConnected", err)
	}
}

// L7. An exchange whose context expires after the request was sent ends the
// session. The answer that arrives later completes no other request: the
// next call returns ErrNotConnected, never a reference mismatch, and after
// Connect the client works again.
func TestLateAnswerEndsTheSession(t *testing.T) {
	p := startHeldPeer(t, func(ref uint16, _ []byte) []byte { return readVarAnswer(ref, []byte{0xAB, 0xCD}) })
	c := connectScripted(t, p.port)

	ctx, expire := newExpiringContext()
	first := make(chan *ReadResult, 1)
	go func() {
		res, err := c.ReadDB(ctx, 1, 0, 2)
		if err != nil {
			t.Errorf("ReadDB: %v", err)
		}
		first <- res
	}()
	waitFor(t, p.arrived, "the first request to arrive")
	expire()
	res := waitFor(t, first, "the first read to return")
	if res == nil || res.Status != ReadStatusTimeout || !errors.Is(res.Err(), context.DeadlineExceeded) {
		t.Fatalf("first read = %+v, want status timeout", res)
	}
	p.release() // the answer to the first request is sent now

	res, err := c.ReadDB(testContext(t), 1, 0, 2)
	if err != nil {
		t.Fatalf("second ReadDB: %v", err)
	}
	var mismatch *PDURefMismatchError
	if errors.As(res.Err(), &mismatch) {
		t.Fatalf("second read took the answer to the first: %v", res.Err())
	}
	if res.Status != ReadStatusTransportErr || !errors.Is(res.Err(), ErrNotConnected) {
		t.Fatalf("second read = status %s, %v; want transport-error, ErrNotConnected", res.Status, res.Err())
	}
	requireDisconnected(t, c)
	if n := p.requests.Load(); n != 1 {
		t.Errorf("the peer received %d requests, want 1: a call on an ended session sends nothing", n)
	}

	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect again: %v", err)
	}
	res, err = c.ReadDB(testContext(t), 1, 0, 2)
	if err != nil || !res.OK() || len(res.Data) != 2 || res.Data[0] != 0xAB {
		t.Fatalf("read after Connect = %+v, %v", res, err)
	}
}

// L6. Cancelling a call after its request was sent returns context.Canceled
// and ends the session, for a read, a write and a user data request alike.
func TestCancelAfterRequestSentEndsTheSession(t *testing.T) {
	calls := map[string]func(ctx context.Context, c *Client) error{
		"ReadDB": func(ctx context.Context, c *Client) error {
			res, err := c.ReadDB(ctx, 1, 0, 2)
			if err != nil {
				return err
			}
			return res.Err()
		},
		"WriteDB":     func(ctx context.Context, c *Client) error { return c.WriteDB(ctx, 1, 0, []byte{1, 2}) },
		"GetCPUState": func(ctx context.Context, c *Client) error { _, err := c.GetCPUState(ctx); return err },
		"ReadDBBit":   func(ctx context.Context, c *Client) error { _, err := c.ReadDBBit(ctx, 1, 0, 0); return err },
		"UploadBlock": func(ctx context.Context, c *Client) error { _, err := c.UploadBlock(ctx, model.BlockDB, 1); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			p := startHeldPeer(t, cpuStateRun)
			c := connectScripted(t, p.port)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- call(ctx, c) }()
			waitFor(t, p.arrived, "the request to arrive")
			cancel()
			if err := waitFor(t, done, "the cancelled call to return"); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			requireDisconnected(t, c)
			if n := p.requests.Load(); n != 1 {
				t.Errorf("the peer received %d requests, want 1", n)
			}
		})
	}
}

// A context that has already ended sends nothing, so it leaves the session
// as it is: the next call is answered on the same connection.
func TestContextEndedBeforeRequestKeepsTheSession(t *testing.T) {
	var requests atomic.Int32
	port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
		requests.Add(1)
		return cpuStateRun(ref, req)
	})
	var dialer closeCountingDialer
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithDialer(dialer.dial))
	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetCPUState(ended); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := c.ListBlocks(ended, model.BlockDB); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("the peer received %d requests from calls whose context had ended", n)
	}
	if c.PDUSize() != 480 || dialer.closes.Load() != 0 {
		t.Fatalf("session ended: PDU size %d, %d closes", c.PDUSize(), dialer.closes.Load())
	}
	if st, err := c.GetCPUState(testContext(t)); err != nil || st != model.CPUStateRun {
		t.Fatalf("next call = %v, %v; want RUN on the same session", st, err)
	}
	if dialer.dials.Load() != 1 {
		t.Errorf("%d connections dialled, want 1", dialer.dials.Load())
	}
}

// A transport failure during an exchange ends the session: the connection is
// closed, the negotiated values are cleared and the next call returns
// ErrNotConnected.
func TestTransportFailureEndsTheSession(t *testing.T) {
	port := startScriptedPeer(t, nil, func(uint16, []byte) []byte { return nil }) // hangs up on the first request
	var dialer closeCountingDialer
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithDialer(dialer.dial))
	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, err := c.GetCPUState(testContext(t))
	if err == nil || errors.Is(err, ErrProtocolFailure) || errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want the transport's error", err)
	}
	requireDisconnected(t, c)
	if n := dialer.closes.Load(); n != 1 {
		t.Errorf("connection closed %d times, want 1", n)
	}
}

// An answer that arrives and is refused leaves the session open: the stream
// is still aligned, and the next call is answered.
func TestRefusedAnswerKeepsTheSession(t *testing.T) {
	var requests atomic.Int32
	port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
		if requests.Add(1) == 1 {
			return cpuStateRun(ref+1, req) // another reference
		}
		return cpuStateRun(ref, req)
	})
	c := connectScripted(t, port)
	var mismatch *PDURefMismatchError
	if _, err := c.GetCPUState(testContext(t)); !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want PDURefMismatchError", err)
	}
	if st, err := c.GetCPUState(testContext(t)); err != nil || st != model.CPUStateRun {
		t.Fatalf("next call = %v, %v; want RUN on the same session", st, err)
	}
}

// silentCountingPeer completes COTP and S7 setup on one connection, then
// reads requests without answering them. requests is final once done is
// closed, which happens when the client closes the connection.
func silentCountingPeer(t *testing.T) (port int, requests *atomic.Int32, done <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	requests = new(atomic.Int32)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = raw.Close() }()
		srv := acceptFakeCOTP(t, raw)
		defer func() { _ = srv.Close() }()
		serveFakeSetup(t, srv, 480)
		for {
			if _, err := srv.ReadTSDU(ctx); err != nil {
				return
			}
			requests.Add(1)
		}
	}()
	t.Cleanup(func() { cancel(); _ = ln.Close(); <-finished })
	return ln.Addr().(*net.TCPAddr).Port, requests, finished
}

// A range probe that times out ends the session, so the remaining offsets
// are reported at once (RangeProbeRequest.Timeout) and nothing more is sent.
func TestProbeReadableRangesStopsAskingAfterATimeout(t *testing.T) {
	port, requests, peerDone := silentCountingPeer(t)
	c := connectScripted(t, port)
	res, err := c.ProbeReadableRanges(context.Background(), RangeProbeRequest{
		Area: model.AreaDB, DBNumber: 1, Start: 0, End: 64, ProbeSize: 8, Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ProbeReadableRanges: %v", err)
	}
	if len(res.Probes) != 8 {
		t.Fatalf("%d probes, want 8", len(res.Probes))
	}
	if first := res.Probes[0].Result; first.Status != ReadStatusTimeout {
		t.Errorf("probe at 0: status %s, want timeout", first.Status)
	}
	for _, p := range res.Probes[1:] {
		if p.Result.Status != ReadStatusTransportErr || !errors.Is(p.Result.Cause, ErrNotConnected) {
			t.Errorf("probe at %d: status %s, cause %v; want transport-error, ErrNotConnected", p.Offset, p.Result.Status, p.Result.Cause)
		}
	}
	requireDisconnected(t, c)
	_ = c.Close()
	waitFor(t, peerDone, "the peer to see the connection closed")
	if n := requests.Load(); n != 1 {
		t.Errorf("the peer received %d requests, want 1", n)
	}
}

// Each segment of an upload is requested once: a failure on a segment ends
// the upload, and the request is not repeated.
func TestUploadBlockAsksForEachSegmentOnce(t *testing.T) {
	var segments atomic.Int32
	port := startScriptedPeer(t, nil, func(ref uint16, req []byte) []byte {
		switch req[wire.S7HeaderSize] {
		case wire.FuncUploadStart:
			return buildStartUploadResponse(ref, 7)
		case wire.FuncUpload:
			segments.Add(1)
		}
		return nil // hangs up
	})
	c := connectScripted(t, port)
	block, err := c.UploadBlock(testContext(t), model.BlockDB, 1)
	if err == nil || block != nil || errors.Is(err, ErrProtocolFailure) {
		t.Fatalf("UploadBlock = %v, %v; want the transport's error", block, err)
	}
	if !strings.Contains(err.Error(), "upload chunk 0") || strings.Contains(err.Error(), "retries") {
		t.Errorf("err = %q, want it to name segment 0 and no retry", err)
	}
	if n := segments.Load(); n != 1 {
		t.Errorf("the peer received %d requests for the segment, want 1", n)
	}
	requireDisconnected(t, c)
}

// L2. Close from several goroutines at once: every call returns nil, the
// connection is closed once and the client ends disconnected.
func TestCloseFromSeveralGoroutines(t *testing.T) {
	port := startScriptedPeer(t, nil, cpuStateRun)
	var dialer closeCountingDialer
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithDialer(dialer.dial))
	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	const callers = 4
	start := make(chan struct{})
	errs := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			errs <- c.Close()
		}()
	}
	close(start)
	for range callers {
		if err := waitFor(t, errs, "a Close to return"); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	if n := dialer.closes.Load(); n != 1 {
		t.Errorf("connection closed %d times, want 1", n)
	}
	requireDisconnected(t, c)
}

// L3. Close ends the call in flight and does not wait for it: Close returns
// while the peer still holds the answer, and the call returns an error that
// matches ErrClosed and go-cotp's ErrClosed (a read: status
// transport-error). The connection is closed once and the client ends
// disconnected.
func TestCloseEndsTheCallInFlight(t *testing.T) {
	for _, tc := range oneRequestCalls {
		t.Run(tc.name, func(t *testing.T) {
			p := startHeldPeer(t, cpuStateRun) // the answer is released only by the cleanup
			var dialer closeCountingDialer
			c := New("127.0.0.1", WithPort(p.port), WithRackSlot(0, 1), WithDialer(dialer.dial))
			if err := c.Connect(testContext(t)); err != nil {
				t.Fatalf("Connect: %v", err)
			}
			type result struct {
				status ReadStatus
				err    error
			}
			callDone := make(chan result, 1)
			go func() {
				status, err := tc.call(context.Background(), c)
				callDone <- result{status, err}
			}()
			waitFor(t, p.arrived, "the request to arrive")

			closeDone := make(chan error, 1)
			go func() { closeDone <- c.Close() }()
			if err := waitFor(t, closeDone, "Close to return while the answer is held"); err != nil {
				t.Errorf("Close: %v", err)
			}
			got := waitFor(t, callDone, "the call in flight to return")
			if !errors.Is(got.err, ErrClosed) || !errors.Is(got.err, cotp.ErrClosed) {
				t.Errorf("call in flight: err = %v, want ErrClosed and cotp.ErrClosed", got.err)
			}
			if got.status != "" && got.status != ReadStatusTransportErr {
				t.Errorf("call in flight: status %s, want transport-error", got.status)
			}
			if n := dialer.closes.Load(); n != 1 {
				t.Errorf("connection closed %d times, want 1", n)
			}
			requireDisconnected(t, c)
			if n := p.requests.Load(); n != 1 {
				t.Errorf("the peer received %d requests, want 1", n)
			}
		})
	}
}

// L4. Close ends a write that the peer does not take: the client is blocked
// writing the request into an in-memory pipe that the peer no longer reads.
// The write returns ErrClosed, and no octet of the request reached the peer,
// so no partial PDU is followed by another on the stream.
func TestCloseEndsAWriteInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stop := make(chan struct{})
		peerRead := make(chan error, 1)
		dial := func(context.Context, string, string) (net.Conn, error) {
			clientEnd, serverEnd := net.Pipe()
			go func() {
				defer func() { _ = serverEnd.Close() }()
				ctx := context.Background()
				srv, err := cotp.Accept(ctx, serverEnd, cotp.ServerConfig{MaxTPDULength: 1024})
				if err != nil {
					peerRead <- err
					return
				}
				defer func() { _ = srv.Close() }()
				req, err := srv.ReadTSDU(ctx)
				if err == nil {
					err = srv.WriteTSDU(ctx, buildS7SetupResponse(binary.BigEndian.Uint16(req[4:6]), 480))
				}
				if err != nil {
					peerRead <- err
					return
				}
				<-stop // reads nothing until the client's write has ended
				req, err = srv.ReadTSDU(ctx)
				if err == nil {
					err = fmt.Errorf("the peer received %d octets of a request", len(req))
				}
				peerRead <- err
			}()
			return clientEnd, nil
		}
		c := New("plc", WithRackSlot(0, 1), WithDialer(dial))
		if err := c.Connect(context.Background()); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		writeDone := make(chan error, 1)
		go func() { writeDone <- c.WriteDB(context.Background(), 1, 0, make([]byte, 64)) }()
		synctest.Wait() // the write waits for the peer to read

		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-writeDone; !errors.Is(err, ErrClosed) || !errors.Is(err, cotp.ErrClosed) {
			t.Errorf("write in flight: err = %v, want ErrClosed and cotp.ErrClosed", err)
		}
		close(stop)
		if err := <-peerRead; err == nil || strings.Contains(err.Error(), "octets of a request") {
			t.Errorf("peer: %v; want the end of the stream and no request", err)
		}
		requireDisconnected(t, c)
	})
}

// L5. Close ends a Connect in flight, held at the setup answer, and does not
// wait for it: Connect returns an error that matches ErrClosed, the
// connection it opened is closed, and no session is left, also when Connect
// was to replace a session, which Close ends too.
func TestCloseEndsAConnectInFlight(t *testing.T) {
	for _, replacing := range []bool{false, true} {
		name := "first Connect"
		if replacing {
			name = "Connect replacing a session"
		}
		t.Run(name, func(t *testing.T) {
			var setups atomic.Int32
			setupArrived := make(chan struct{}, 1)
			gate := make(chan struct{})
			release := sync.OnceFunc(func() { close(gate) })
			heldSetup := int32(1)
			if replacing {
				heldSetup = 2
			}
			port := startScriptedPeer(t, func(ref uint16) []byte {
				if setups.Add(1) == heldSetup {
					setupArrived <- struct{}{}
					<-gate
				}
				return buildS7SetupResponse(ref, 480)
			}, cpuStateRun)
			t.Cleanup(release)

			var dialer closeCountingDialer
			c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithDialer(dialer.dial))
			if replacing {
				if err := c.Connect(testContext(t)); err != nil {
					t.Fatalf("first Connect: %v", err)
				}
			}
			connectDone := make(chan error, 1)
			go func() { connectDone <- c.Connect(context.Background()) }()
			waitFor(t, setupArrived, "the setup request to arrive")

			closeDone := make(chan error, 1)
			go func() { closeDone <- c.Close() }()
			if err := waitFor(t, closeDone, "Close to return while the setup answer is held"); err != nil {
				t.Errorf("Close: %v", err)
			}
			if err := waitFor(t, connectDone, "Connect to return"); !errors.Is(err, ErrClosed) {
				t.Errorf("Connect = %v, want ErrClosed", err)
			}
			if dials, closes := dialer.dials.Load(), dialer.closes.Load(); closes != dials {
				t.Errorf("%d connections dialled, %d closed; want every one closed", dials, closes)
			}
			requireDisconnected(t, c)
		})
	}
}

// Close also ends a call that waits for its turn behind the call in flight:
// every call in flight when Close is called returns ErrClosed, the waiting
// read and Connect without sending anything.
func TestCloseEndsACallWaitingForItsTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peers, requests := silentPipePeers(t)
		c := pipeClient(t, peers)
		errs := make(chan error, 3)
		go func() { _, err := c.GetCPUState(context.Background()); errs <- err }()
		synctest.Wait() // the first call waits for its answer
		go func() {
			res, err := c.ReadDB(context.Background(), 1, 0, 2)
			if err == nil {
				err = res.Err()
			}
			errs <- err
		}()
		go func() { errs <- c.Connect(context.Background()) }()
		synctest.Wait() // the read and Connect wait for their turn

		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		for range 3 {
			if err := <-errs; !errors.Is(err, ErrClosed) {
				t.Errorf("call in flight: err = %v, want ErrClosed", err)
			}
		}
		if n := requests.Load(); n != 1 {
			t.Errorf("the peer received %d requests, want 1", n)
		}
		requireDisconnected(t, c)
	})
}

// twoChunkCalls are a read and a write of 500 bytes: two chunks each with the
// PDU size of 480 that pipePeers grants, so WithRateLimit pauses between them.
// A read reports its status; a write reports "".
var twoChunkCalls = []struct {
	name string
	call func(ctx context.Context, c *Client) (ReadStatus, error)
}{
	{"ReadDB", func(ctx context.Context, c *Client) (ReadStatus, error) {
		res, err := c.ReadDB(ctx, 1, 0, 500)
		if err != nil {
			return "", err
		}
		return res.Status, res.Err()
	}},
	{"WriteDB", func(ctx context.Context, c *Client) (ReadStatus, error) {
		return "", c.WriteDB(ctx, 1, 0, make([]byte, 500))
	}},
}

// readWritePipePeers answers every Read Var and Write Var request in full and
// counts the requests that arrived.
func readWritePipePeers(t *testing.T) (peers *pipePeers, requests *atomic.Int32) {
	t.Helper()
	requests = new(atomic.Int32)
	peers = &pipePeers{op: func(ref uint16, req []byte) []byte {
		requests.Add(1)
		if req[wire.S7HeaderSize] == wire.FuncWriteVar {
			return buildWriteVarResponse(ref, wire.RetCodeSuccess)
		}
		return answerReadsAndSZL(ref, req)
	}}
	t.Cleanup(peers.wait)
	return peers, requests
}

// Close ends a pause of WithRateLimit at once: a read or a write in the pause
// between its two chunks returns ErrClosed (a read: status transport-error)
// when Close is called, with no time passed on the bubble's clock, and does
// not send its second chunk.
func TestCloseEndsAPauseOfTheRateLimit(t *testing.T) {
	for _, tc := range twoChunkCalls {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				peers, requests := readWritePipePeers(t)
				c := pipeClient(t, peers, WithRateLimit(time.Hour))
				type result struct {
					status ReadStatus
					err    error
				}
				done := make(chan result, 1)
				start := time.Now()
				go func() {
					status, err := tc.call(context.Background(), c)
					done <- result{status, err}
				}()
				synctest.Wait() // the first chunk is answered; the call pauses
				if n := requests.Load(); n != 1 {
					t.Fatalf("the peer received %d requests before the pause, want 1", n)
				}

				if err := c.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
				synctest.Wait()
				var r result
				select {
				case r = <-done:
				default:
					t.Error("the call in a pause of WithRateLimit did not return when Close was called")
					r = <-done // when the pause is over on the bubble's clock
				}
				if elapsed := time.Since(start); elapsed != 0 {
					t.Errorf("the call returned after %v, want 0", elapsed)
				}
				if !errors.Is(r.err, ErrClosed) {
					t.Errorf("err = %v, want ErrClosed", r.err)
				}
				if r.status != "" && r.status != ReadStatusTransportErr {
					t.Errorf("status %s, want transport-error", r.status)
				}
				if n := requests.Load(); n != 1 {
					t.Errorf("the peer received %d requests, want 1", n)
				}
				requireDisconnected(t, c)
			})
		})
	}
}

// A call whose context ends in a pause of WithRateLimit returns that
// context's error (a read: status timeout) when it ends, does not send its
// second chunk and leaves the session open.
func TestAPauseOfTheRateLimitEndsWithItsContext(t *testing.T) {
	for _, tc := range twoChunkCalls {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				peers, requests := readWritePipePeers(t)
				c := pipeClient(t, peers, WithRateLimit(time.Hour))
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				start := time.Now()
				status, err := tc.call(ctx, c)
				if elapsed := time.Since(start); elapsed != time.Second {
					t.Errorf("the call returned after %v, want the deadline of 1s", elapsed)
				}
				if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrClosed) {
					t.Errorf("err = %v, want context.DeadlineExceeded alone", err)
				}
				if status != "" && status != ReadStatusTimeout {
					t.Errorf("status %s, want timeout", status)
				}
				if n := requests.Load(); n != 1 {
					t.Errorf("the peer received %d requests, want 1", n)
				}
				if res, err := c.ReadDB(context.Background(), 1, 0, 2); err != nil || !res.OK() {
					t.Errorf("next read: %v, %v; want the session open", res, err)
				}
			})
		})
	}
}

// A call that waits for its turn behind the call in flight returns its
// context's error when that context ends, for every operation and Connect,
// and sends nothing. The call in flight and its session are not disturbed.
func TestCallWaitingForItsTurnEndsWithItsContext(t *testing.T) {
	p := startHeldPeer(t, cpuStateRun)
	c := connectScripted(t, p.port)
	t.Cleanup(p.release) // before the client's Close, should the test stop early
	first := make(chan error, 1)
	go func() {
		st, err := c.GetCPUState(context.Background())
		if err == nil && st != model.CPUStateRun {
			err = fmt.Errorf("state %v", st)
		}
		first <- err
	}()
	waitFor(t, p.arrived, "the first request to arrive")

	waiting := map[string]func(ctx context.Context) error{
		"ReadDB": func(ctx context.Context) error {
			res, err := c.ReadDB(ctx, 1, 0, 2)
			if err != nil {
				return err
			}
			if res.Status != ReadStatusTimeout {
				return fmt.Errorf("status %s, want timeout", res.Status)
			}
			return res.Err()
		},
		"WriteDB":            func(ctx context.Context) error { return c.WriteDB(ctx, 1, 0, []byte{1}) },
		"ReadDBBit":          func(ctx context.Context) error { _, err := c.ReadDBBit(ctx, 1, 0, 0); return err },
		"WriteDBBit":         func(ctx context.Context) error { return c.WriteDBBit(ctx, 1, 0, 0, true) },
		"GetCPUState":        func(ctx context.Context) error { _, err := c.GetCPUState(ctx); return err },
		"GetProtectionLevel": func(ctx context.Context) error { _, err := c.GetProtectionLevel(ctx); return err },
		"Identify":           func(ctx context.Context) error { _, err := c.Identify(ctx); return err },
		"ReadDiagBufferRaw":  func(ctx context.Context) error { _, err := c.ReadDiagBufferRaw(ctx); return err },
		"ListBlocks":         func(ctx context.Context) error { _, err := c.ListBlocks(ctx, model.BlockDB); return err },
		"BlockCounts":        func(ctx context.Context) error { _, err := c.BlockCounts(ctx); return err },
		"GetBlockInfo":       func(ctx context.Context) error { _, err := c.GetBlockInfo(ctx, model.BlockDB, 1); return err },
		"UploadBlock":        func(ctx context.Context) error { _, err := c.UploadBlock(ctx, model.BlockDB, 1); return err },
		"Connect":            func(ctx context.Context) error { return c.Connect(ctx) },
	}
	for name, call := range waiting {
		ctx, expire := newExpiringContext()
		done := make(chan error, 1)
		go func() { done <- call(ctx) }()
		expire()
		if err := waitFor(t, done, name+" waiting for its turn to return"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: err = %v, want context.DeadlineExceeded", name, err)
		}
	}

	p.release()
	if err := waitFor(t, first, "the call in flight to return"); err != nil {
		t.Errorf("call in flight: %v", err)
	}
	if n := p.requests.Load(); n != 1 {
		t.Errorf("the peer received %d requests, want 1: a call that waited sent nothing", n)
	}
	if c.PDUSize() != 480 {
		t.Errorf("PDU size %d after the waiting calls, want the session of 480 in place", c.PDUSize())
	}
}

// snapshotLogger records what ConnectionInfo and PDUSize return when they
// are called from inside a Logger method.
type snapshotLogger struct {
	c     *Client
	calls atomic.Int32
	pdu   atomic.Int32
	info  atomic.Pointer[model.ConnectionInfo]
}

func (l *snapshotLogger) Debug(string, ...any) { l.snapshot() }
func (l *snapshotLogger) Info(string, ...any)  { l.snapshot() }
func (l *snapshotLogger) Error(string, ...any) { l.snapshot() }

func (l *snapshotLogger) snapshot() {
	l.calls.Add(1)
	l.pdu.Store(int32(l.c.PDUSize()))
	info := l.c.ConnectionInfo()
	l.info.Store(&info)
}

// L9. A Logger and a Dialer run while the client holds its request lock.
// They may call ConnectionInfo and PDUSize, which do not take it, and get
// the session as it is at that moment: here the session a failed second
// Connect left in place.
func TestCallbacksMayReadConnectionInfo(t *testing.T) {
	var setups atomic.Int32
	port := startScriptedPeer(t, func(ref uint16) []byte {
		if setups.Add(1) == 2 {
			return buildAckData(ref, wire.ErrClassResource, 0x02, nil, nil)
		}
		return buildS7SetupResponse(ref, 480)
	}, cpuStateRun)

	logger := &snapshotLogger{}
	var dialerSaw []int
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithLogger(logger),
		WithDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialerSaw = append(dialerSaw, logger.c.ConnectionInfo().PDUSize)
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}))
	logger.c = c
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Connect(testContext(t)); err == nil {
		t.Fatal("the second Connect succeeded")
	}
	if logger.calls.Load() == 0 {
		t.Fatal("the logger was not called for the failed Connect")
	}
	if got := logger.pdu.Load(); got != 480 {
		t.Errorf("PDUSize from the logger = %d, want the 480 of the session still in place", got)
	}
	if info := logger.info.Load(); info == nil || info.PDUSize != 480 || info.Rack != 0 || info.Slot != 1 {
		t.Errorf("ConnectionInfo from the logger = %+v", info)
	}
	if len(dialerSaw) != 2 || dialerSaw[0] != 0 || dialerSaw[1] != 480 {
		t.Errorf("PDU size the dialer saw = %v, want [0 480]", dialerSaw)
	}
}

// closingLogger calls Close of its client from the method named in on, once,
// and reports what Close returned on closed.
type closingLogger struct {
	c      *Client
	on     string
	closed chan error
}

func (l *closingLogger) log(method string) {
	if method == l.on && l.c != nil {
		c := l.c
		l.c = nil
		l.closed <- c.Close()
	}
}

func (l *closingLogger) Debug(string, ...any) { l.log("Debug") }
func (l *closingLogger) Info(string, ...any)  { l.log("Info") }
func (l *closingLogger) Error(string, ...any) { l.log("Error") }

// uploadPeer answers an upload of one segment and its end.
func uploadPeer(ref uint16, req []byte) []byte {
	switch req[wire.S7HeaderSize] {
	case wire.FuncUploadStart:
		return buildStartUploadResponse(ref, 7)
	case wire.FuncUpload:
		return buildUploadChunkResponse(ref, []byte{1, 2, 3, 4}, true)
	case wire.FuncUploadEnd:
		return buildAckData(ref, 0, 0, []byte{wire.FuncUploadEnd}, nil)
	}
	return cpuStateRun(ref, req)
}

// L10. A Logger and a Dialer may call Close of their client: Close returns,
// and so does the call that runs the callback. Close ends that call as it
// ends any call in flight; the client ends disconnected with every
// connection closed.
func TestCallbacksMayCloseTheClient(t *testing.T) {
	t.Run("Logger during UploadBlock", func(t *testing.T) {
		port := startScriptedPeer(t, nil, uploadPeer)
		logger := &closingLogger{on: "Debug", closed: make(chan error, 1)}
		var dialer closeCountingDialer
		c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithLogger(logger), WithDialer(dialer.dial))
		if err := c.Connect(testContext(t)); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		logger.c = c
		type upload struct {
			block *model.BlockData
			err   error
		}
		done := make(chan upload, 1)
		go func() {
			block, err := c.UploadBlock(testContext(t), model.BlockDB, 1)
			done <- upload{block, err}
		}()
		if err := waitFor(t, logger.closed, "Close called from the Logger to return"); err != nil {
			t.Errorf("Close from the Logger: %v", err)
		}
		// The Logger runs in the cleanup, after the block was received.
		if got := waitFor(t, done, "UploadBlock to return"); got.err != nil || got.block == nil || len(got.block.Data) != 4 {
			t.Errorf("UploadBlock = %+v, %v; want the block of 4 bytes", got.block, got.err)
		}
		if n := dialer.closes.Load(); n != 1 {
			t.Errorf("connection closed %d times, want 1", n)
		}
		requireDisconnected(t, c)
	})

	t.Run("Logger on a failed Connect", func(t *testing.T) {
		var setups atomic.Int32
		port := startScriptedPeer(t, func(ref uint16) []byte {
			if setups.Add(1) == 2 {
				return buildAckData(ref, wire.ErrClassResource, 0x02, nil, nil)
			}
			return buildS7SetupResponse(ref, 480)
		}, cpuStateRun)
		logger := &closingLogger{on: "Error", closed: make(chan error, 1)}
		var dialer closeCountingDialer
		c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithLogger(logger), WithDialer(dialer.dial))
		if err := c.Connect(testContext(t)); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		logger.c = c
		done := make(chan error, 1)
		go func() { done <- c.Connect(testContext(t)) }()
		if err := waitFor(t, logger.closed, "Close called from the Logger to return"); err != nil {
			t.Errorf("Close from the Logger: %v", err)
		}
		// The Logger runs after the attempt failed: Connect returns that failure.
		if err := waitFor(t, done, "Connect to return"); err == nil || errors.Is(err, ErrClosed) {
			t.Errorf("Connect = %v, want the setup refusal", err)
		}
		if dials, closes := dialer.dials.Load(), dialer.closes.Load(); dials != 2 || closes != 2 {
			t.Errorf("%d connections dialled, %d closed; want 2 and 2", dials, closes)
		}
		requireDisconnected(t, c)
	})

	t.Run("Dialer", func(t *testing.T) {
		port := startScriptedPeer(t, nil, cpuStateRun)
		var c *Client
		var counting closeCountingDialer
		dialerClosed := make(chan error, 1)
		c = New("127.0.0.1", WithPort(port), WithRackSlot(0, 1),
			WithDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
				if counting.dials.Load() == 1 {
					dialerClosed <- c.Close()
					// Dial without the context, which Close has ended: Connect
					// must close a connection that a Dialer returns all the same.
					ctx = context.Background()
				}
				return counting.dial(ctx, network, addr)
			}))
		if err := c.Connect(testContext(t)); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- c.Connect(testContext(t)) }()
		if err := waitFor(t, dialerClosed, "Close called from the Dialer to return"); err != nil {
			t.Errorf("Close from the Dialer: %v", err)
		}
		if err := waitFor(t, done, "Connect to return"); !errors.Is(err, ErrClosed) {
			t.Errorf("Connect = %v, want ErrClosed", err)
		}
		if dials, closes := counting.dials.Load(), counting.closes.Load(); dials != 2 || closes != 2 {
			t.Errorf("%d connections dialled, %d closed; want 2 and 2", dials, closes)
		}
		requireDisconnected(t, c)
	})
}

// A Dialer or a Logger that does not return normally is not recovered by the
// client: the unwinding reaches the caller of Connect. The session in place
// is kept and the client's turn is given back, so the next call is answered.
func TestCallbackUnwindingReachesTheCaller(t *testing.T) {
	connectRecovering := func(c *Client) (recovered any) {
		defer func() { recovered = recover() }()
		_ = c.Connect(testContext(t))
		return nil
	}
	t.Run("Dialer", func(t *testing.T) {
		port := startScriptedPeer(t, nil, cpuStateRun)
		var dials atomic.Int32
		c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1),
			WithDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
				if dials.Add(1) == 2 {
					panic("dialer")
				}
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			}))
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Connect(testContext(t)); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		if got := connectRecovering(c); got != "dialer" {
			t.Fatalf("recovered %v, want the value the Dialer raised", got)
		}
		if st, err := c.GetCPUState(testContext(t)); err != nil || st != model.CPUStateRun {
			t.Errorf("next call = %v, %v; want RUN on the session in place", st, err)
		}
	})
	t.Run("Logger", func(t *testing.T) {
		var setups atomic.Int32
		port := startScriptedPeer(t, func(ref uint16) []byte {
			if setups.Add(1) == 2 {
				return buildAckData(ref, wire.ErrClassResource, 0x02, nil, nil)
			}
			return buildS7SetupResponse(ref, 480)
		}, cpuStateRun)
		c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithLogger(unwindingLogger{}))
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Connect(testContext(t)); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		if got := connectRecovering(c); got != "logger" {
			t.Fatalf("recovered %v, want the value the Logger raised", got)
		}
		if st, err := c.GetCPUState(testContext(t)); err != nil || st != model.CPUStateRun {
			t.Errorf("next call = %v, %v; want RUN on the session in place", st, err)
		}
	})
}

type unwindingLogger struct{}

func (unwindingLogger) Debug(string, ...any) { panic("logger") }
func (unwindingLogger) Info(string, ...any)  { panic("logger") }
func (unwindingLogger) Error(string, ...any) { panic("logger") }
