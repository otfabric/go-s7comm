// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-s7comm/model"
)

// Requests bounded in time (WithRequestTimeout). The tests run in a
// testing/synctest bubble: the client talks to an in-process peer over
// in-memory pipes (pipePeers), time is the bubble's clock, so a duration is
// measured exactly, and synctest.Wait returns once every goroutine is
// blocked. Nothing sleeps to let something happen; a peer that takes its
// time sleeps on the bubble's clock.

// oneRequestCalls are calls that send one request to a peer that never
// answers, one of each kind: a read, a write, a bit read, a user data
// request and an upload. A read reports its status; the others report "".
var oneRequestCalls = []struct {
	name string
	call func(ctx context.Context, c *Client) (ReadStatus, error)
}{
	{"ReadDB", func(ctx context.Context, c *Client) (ReadStatus, error) {
		res, err := c.ReadDB(ctx, 1, 0, 2)
		if err != nil {
			return "", err
		}
		return res.Status, res.Err()
	}},
	{"WriteDB", func(ctx context.Context, c *Client) (ReadStatus, error) {
		return "", c.WriteDB(ctx, 1, 0, []byte{1, 2})
	}},
	{"ReadDBBit", func(ctx context.Context, c *Client) (ReadStatus, error) {
		_, err := c.ReadDBBit(ctx, 1, 0, 0)
		return "", err
	}},
	{"GetCPUState", func(ctx context.Context, c *Client) (ReadStatus, error) {
		_, err := c.GetCPUState(ctx)
		return "", err
	}},
	{"ListBlocks", func(ctx context.Context, c *Client) (ReadStatus, error) {
		_, err := c.ListBlocks(ctx, model.BlockDB)
		return "", err
	}},
	{"UploadBlock", func(ctx context.Context, c *Client) (ReadStatus, error) {
		_, err := c.UploadBlock(ctx, model.BlockDB, 1)
		return "", err
	}},
}

// silentPipePeers answers setup and then reads requests without answering
// them: each request waits on the peer's side until the test ends. requests
// counts the requests that arrived.
func silentPipePeers(t *testing.T) (peers *pipePeers, requests *atomic.Int32) {
	t.Helper()
	stop := make(chan struct{})
	requests = new(atomic.Int32)
	peers = &pipePeers{op: func(uint16, []byte) []byte {
		requests.Add(1)
		<-stop
		return nil
	}}
	t.Cleanup(func() { close(stop); peers.wait() })
	return peers, requests
}

// pipeClient connects a client through peers. Its cleanup, which runs before
// the peers' own, closes the client.
func pipeClient(t *testing.T, peers *pipePeers, opts ...Option) *Client {
	t.Helper()
	c := New("plc", append([]Option{WithRackSlot(0, 1), WithDialer(peers.dial)}, opts...)...)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// Each kind of request ends after the request timeout when the peer does not
// answer: the call returns context.DeadlineExceeded (a read: status timeout)
// after exactly that long, and the session ends because the request was sent.
func TestRequestTimeoutBoundsEachKindOfRequest(t *testing.T) {
	for _, tc := range oneRequestCalls {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				peers, requests := silentPipePeers(t)
				c := pipeClient(t, peers, WithRequestTimeout(2*time.Second))
				start := time.Now()
				status, err := tc.call(context.Background(), c)
				if elapsed := time.Since(start); elapsed != 2*time.Second {
					t.Errorf("the call returned after %v, want the request timeout of 2s", elapsed)
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("err = %v, want context.DeadlineExceeded", err)
				}
				if status != "" && status != ReadStatusTimeout {
					t.Errorf("status %s, want timeout", status)
				}
				if n := requests.Load(); n != 1 {
					t.Errorf("the peer received %d requests, want 1", n)
				}
				requireDisconnected(t, c)
			})
		})
	}
}

// The earlier deadline wins: the request timeout, or the deadline of the
// call's context. Without a request timeout (the default) a call to a peer
// that does not answer is bounded by its context alone: here it is still in
// flight after an hour, until Close ends it.
func TestRequestTimeoutAndTheCallsDeadline(t *testing.T) {
	const never = time.Duration(-1)
	cases := []struct {
		name            string
		requestTimeout  time.Duration // 0: the option is not set
		callDeadline    time.Duration // 0: the context has none
		wantReturnAfter time.Duration
	}{
		{"default, no deadline", 0, 0, never},
		{"request timeout", 2 * time.Second, 0, 2 * time.Second},
		{"call deadline earlier", 10 * time.Second, time.Second, time.Second},
		{"request timeout earlier", time.Second, 10 * time.Second, time.Second},
		{"call deadline alone", 0, 3 * time.Second, 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				peers, _ := silentPipePeers(t)
				var opts []Option
				if tc.requestTimeout > 0 {
					opts = append(opts, WithRequestTimeout(tc.requestTimeout))
				}
				c := pipeClient(t, peers, opts...)
				ctx := context.Background()
				if tc.callDeadline > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.callDeadline)
					defer cancel()
				}
				type result struct {
					res     *ReadResult
					elapsed time.Duration
				}
				done := make(chan result, 1)
				start := time.Now()
				go func() {
					res, err := c.ReadDB(ctx, 1, 0, 2)
					if err != nil {
						t.Errorf("ReadDB: %v", err)
					}
					done <- result{res, time.Since(start)}
				}()

				if tc.wantReturnAfter == never {
					time.Sleep(time.Hour)
					synctest.Wait()
					select {
					case r := <-done:
						t.Fatalf("the read returned after %v: %+v; want it in flight", r.elapsed, r.res)
					default:
					}
					if err := c.Close(); err != nil {
						t.Fatalf("Close: %v", err)
					}
					r := <-done
					if r.res.Status != ReadStatusTransportErr || !errors.Is(r.res.Err(), ErrClosed) {
						t.Errorf("after Close: status %s, %v; want transport-error, ErrClosed", r.res.Status, r.res.Err())
					}
					return
				}
				r := <-done
				if r.elapsed != tc.wantReturnAfter {
					t.Errorf("the read returned after %v, want %v", r.elapsed, tc.wantReturnAfter)
				}
				if r.res.Status != ReadStatusTimeout || !errors.Is(r.res.Err(), context.DeadlineExceeded) {
					t.Errorf("status %s, %v; want timeout, context.DeadlineExceeded", r.res.Status, r.res.Err())
				}
			})
		})
	}
}

// slowReadPeers answers each Read Var with the bytes asked for, after delay
// on the bubble's clock. requests counts the requests.
func slowReadPeers(t *testing.T, delay time.Duration) (*pipePeers, *atomic.Int32) {
	t.Helper()
	requests := new(atomic.Int32)
	peers := &pipePeers{op: func(ref uint16, req []byte) []byte {
		requests.Add(1)
		time.Sleep(delay)
		return readVarAnswer(ref, make([]byte, binary.BigEndian.Uint16(req[16:18])))
	}}
	t.Cleanup(peers.wait)
	return peers, requests
}

// The request timeout bounds each request of a call, not the call: a read of
// three chunks whose answers take 0.9 s each succeeds after 2.7 s with a
// request timeout of 1 s. The call's context still bounds the whole call.
func TestRequestTimeoutIsPerRequest(t *testing.T) {
	const size = 1000 // three chunks at the PDU size of 480
	t.Run("each request within the timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			peers, requests := slowReadPeers(t, 900*time.Millisecond)
			c := pipeClient(t, peers, WithRequestTimeout(time.Second))
			start := time.Now()
			res, err := c.ReadDB(context.Background(), 1, 0, size)
			if err != nil || !res.OK() || len(res.Data) != size {
				t.Fatalf("ReadDB = %+v, %v; want %d bytes", res, err, size)
			}
			if elapsed := time.Since(start); elapsed != 2700*time.Millisecond {
				t.Errorf("the read took %v, want 2.7s", elapsed)
			}
			if n := requests.Load(); n != 3 {
				t.Errorf("%d requests, want 3", n)
			}
		})
	})
	t.Run("the call's deadline ends the call", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			peers, requests := slowReadPeers(t, 900*time.Millisecond)
			c := pipeClient(t, peers, WithRequestTimeout(time.Second))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			start := time.Now()
			res, err := c.ReadDB(ctx, 1, 0, size)
			if err != nil || res.Status != ReadStatusTimeout {
				t.Fatalf("ReadDB = %+v, %v; want status timeout", res, err)
			}
			if elapsed := time.Since(start); elapsed != 2*time.Second {
				t.Errorf("the read returned after %v, want the call's deadline of 2s", elapsed)
			}
			if n := requests.Load(); n != 3 {
				t.Errorf("%d requests, want 3", n)
			}
		})
	})
}

// WithRequestTimeout does not bound Connect, which WithTimeout bounds: a
// setup answer that takes 3 s is taken with a request timeout of 1 s.
func TestRequestTimeoutDoesNotBoundConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peers := &pipePeers{
			setup: func(ref uint16) []byte {
				time.Sleep(3 * time.Second)
				return buildS7SetupResponse(ref, 480)
			},
			op: func(ref uint16, req []byte) []byte { return cpuStateRun(ref, req) },
		}
		t.Cleanup(peers.wait)
		start := time.Now()
		c := pipeClient(t, peers, WithRequestTimeout(time.Second), WithTimeout(5*time.Second))
		if elapsed := time.Since(start); elapsed != 3*time.Second {
			t.Errorf("Connect took %v, want 3s", elapsed)
		}
		if st, err := c.GetCPUState(context.Background()); err != nil || st != model.CPUStateRun {
			t.Errorf("GetCPUState = %v, %v", st, err)
		}
	})
}

// Connect refuses a negative request timeout with ValidationError and dials
// nothing; zero (no bound) and any positive value are taken, up to the
// largest duration.
func TestRequestTimeoutValidation(t *testing.T) {
	cases := []struct {
		d       time.Duration
		refused bool
		call    bool // a request is answered within it, deterministically
	}{
		{-time.Hour, true, false},
		{-time.Nanosecond, true, false},
		{0, false, true},
		{time.Nanosecond, false, false},
		{time.Hour, false, true},
		{time.Duration(math.MaxInt64), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.d.String(), func(t *testing.T) {
			var dials atomic.Int32
			peers := &pipePeers{op: cpuStateRun}
			t.Cleanup(peers.wait)
			c := New("plc", WithRackSlot(0, 1), WithRequestTimeout(tc.d),
				WithDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
					dials.Add(1)
					return peers.dial(ctx, network, addr)
				}))
			t.Cleanup(func() { _ = c.Close() })
			err := c.Connect(testContext(t))
			var verr *ValidationError
			if tc.refused {
				if !errors.As(err, &verr) || dials.Load() != 0 {
					t.Fatalf("Connect = %v after %d dials, want *ValidationError and no dial", err, dials.Load())
				}
				return
			}
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			if tc.call {
				if st, err := c.GetCPUState(testContext(t)); err != nil || st != model.CPUStateRun {
					t.Errorf("GetCPUState = %v, %v", st, err)
				}
			}
		})
	}
}
