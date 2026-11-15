// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// L3, L4. Close waits for the call in flight and does not end it: the call
// ends by its own context, then Close returns nil and the client is
// disconnected. (Ending the call from Close is planned as a behaviour
// change of a later minor release; TESTING.md records the row.)
func TestCloseWaitsForTheCallInFlight(t *testing.T) {
	p := startHeldPeer(t, cpuStateRun)
	var dialer closeCountingDialer
	c := New("127.0.0.1", WithPort(p.port), WithRackSlot(0, 1), WithDialer(dialer.dial))
	if err := c.Connect(testContext(t)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		_, err := c.GetCPUState(ctx)
		callDone <- err
	}()
	waitFor(t, p.arrived, "the request to arrive")

	// The call holds the client's request lock until its context ends, so
	// Close cannot return before cancel is called below.
	type closed struct {
		err          error
		callHadEnded bool
	}
	closeDone := make(chan closed, 1)
	go func() {
		err := c.Close()
		closeDone <- closed{err: err, callHadEnded: ctx.Err() != nil}
	}()
	cancel()
	if err := waitFor(t, callDone, "the call in flight to return"); !errors.Is(err, context.Canceled) {
		t.Errorf("call in flight: err = %v, want context.Canceled", err)
	}
	got := waitFor(t, closeDone, "Close to return")
	if got.err != nil || !got.callHadEnded {
		t.Errorf("Close = %v, returned after the call had ended: %v", got.err, got.callHadEnded)
	}
	if n := dialer.closes.Load(); n != 1 {
		t.Errorf("connection closed %d times, want 1", n)
	}
	requireDisconnected(t, c)
}

// L5. Close during Connect waits for it: Connect completes, then Close ends
// the new session. The client is disconnected and its connection closed.
func TestCloseDuringConnectWaitsAndDisconnects(t *testing.T) {
	setupArrived := make(chan struct{}, 1)
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	port := startScriptedPeer(t, func(ref uint16) []byte {
		setupArrived <- struct{}{}
		<-gate
		return buildS7SetupResponse(ref, 480)
	}, cpuStateRun)
	t.Cleanup(release)

	var dialer closeCountingDialer
	c := New("127.0.0.1", WithPort(port), WithRackSlot(0, 1), WithDialer(dialer.dial))
	connectDone := make(chan error, 1)
	go func() { connectDone <- c.Connect(context.Background()) }()
	waitFor(t, setupArrived, "the setup request to arrive") // Connect holds the request lock from here on

	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	release()
	if err := waitFor(t, connectDone, "Connect to return"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := waitFor(t, closeDone, "Close to return"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if dials, closes := dialer.dials.Load(), dialer.closes.Load(); dials != 1 || closes != 1 {
		t.Errorf("%d connections dialled, %d closed; want 1 and 1", dials, closes)
	}
	requireDisconnected(t, c)
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
