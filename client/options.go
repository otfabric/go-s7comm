// SPDX-License-Identifier: MIT

// Package client provides a high-level S7 client API.
package client

import (
	"context"
	"net"
	"time"
)

// Option configures the S7 client. Options are immutable after construction.
type Option func(*options)

type options struct {
	port           int
	rack           int
	slot           int
	localTSAP      uint16
	remoteTSAP     uint16
	useExplicit    bool
	autoRackSlot   bool
	bruteRackSlot  bool
	timeout        time.Duration
	requestTimeout time.Duration
	rateLimit      time.Duration
	logger         Logger
	maxPDU         int
	maxAmqCalling  int
	maxAmqCalled   int
	dialer         Dialer
}

// Logger is an optional printf-style sink for client diagnostics.
// Nil (the default when WithLogger is omitted) disables logging.
// Methods are invoked synchronously on the request path and must not block.
// Args follow fmt.Sprintf conventions (e.g. Error("Connect failed: %v", err)).
// Use NewSlogLogger to adapt a slog.Handler without changing this interface.
//
// The methods run on the goroutine of the call that logs (Connect, the
// cleanup of UploadBlock), while that call holds the client's turn. They may
// call ConnectionInfo, PDUSize and Close of that client; Close ends the call
// that logs, as it ends any call in flight. They must not call an operation
// or Connect of that client: such a call waits for the call that logs, so it
// returns only when its own context ends, with that context's error. A method
// that does not return normally is not recovered by the client: the
// unwinding reaches the caller of the call that logs, and the client's turn
// is given back, so the client stays usable.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

func defaultOptions() *options {
	return &options{
		port:          102,
		rack:          0,
		slot:          1,
		timeout:       5 * time.Second,
		rateLimit:     0,
		maxPDU:        480,
		maxAmqCalling: 1,
		maxAmqCalled:  1,
	}
}

// WithPort sets the target port
func WithPort(port int) Option {
	return func(o *options) { o.port = port }
}

// WithRackSlot sets rack and slot numbers
func WithRackSlot(rack, slot int) Option {
	return func(o *options) {
		o.rack = rack
		o.slot = slot
	}
}

// WithTSAP sets explicit TSAP values
func WithTSAP(local, remote uint16) Option {
	return func(o *options) {
		o.localTSAP = local
		o.remoteTSAP = remote
		o.useExplicit = true
	}
}

// WithAutoRackSlot enables automatic rack/slot detection during connect.
func WithAutoRackSlot(brute bool) Option {
	return func(o *options) {
		o.autoRackSlot = true
		o.bruteRackSlot = brute
	}
}

// WithTimeout bounds Connect: the dial, the COTP handshake and S7 Setup
// Communication (default 5s; 0 = no bound beyond the context). It does not
// bound the requests after Connect: see WithRequestTimeout.
func WithTimeout(t time.Duration) Option {
	return func(o *options) { o.timeout = t }
}

// WithRequestTimeout bounds each request after Connect: sending one request
// PDU and waiting for its answer. A call of several requests (a read or a
// write larger than the PDU, an SZL list or block list of several packets,
// an upload) applies it to each of them. The call's context still bounds
// the whole call, and the earlier deadline wins. The pauses of
// WithRateLimit and the wait for the client's turn are not requests.
//
// A request that runs out of time returns an error that matches
// context.DeadlineExceeded (a read: status timeout) and, once the request
// was sent, ends the session as a context deadline does (see Connect).
// 0, the default, sets no bound: a call is bounded by its context alone.
// Connect refuses a negative value with ValidationError.
func WithRequestTimeout(d time.Duration) Option {
	return func(o *options) { o.requestTimeout = d }
}

// WithRateLimit sets a minimal delay between sequential protocol operations.
func WithRateLimit(d time.Duration) Option {
	return func(o *options) { o.rateLimit = d }
}

// WithLogger sets an optional Logger. Nil disables logging (same as omitting the option).
func WithLogger(l Logger) Option {
	return func(o *options) { o.logger = l }
}

// Dialer opens the transport connection to addr ("host:port"). It receives
// network "tcp". Like net.Dialer, it returns a non-nil connection exactly when
// the error is nil.
//
// The Dialer of WithDialer runs on the goroutine of Connect, while Connect
// holds the client's turn. It may call ConnectionInfo, PDUSize and Close of
// that client; Close ends the Connect that runs the Dialer, which then
// returns an error that matches ErrClosed. It must not call an operation or
// Connect of that client: such a call waits for the Connect that runs the
// Dialer, so it returns only when its own context ends, with that context's
// error. A Dialer that does not return normally is not recovered by the
// client: the unwinding reaches the caller of Connect, the session in place
// (if any) is kept, and the client's turn is given back.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// WithDialer sets the function that opens the TCP connection of Connect, for
// example to apply an address allowlist or to dial through a proxy. A nil
// dialer (the default) uses net.Dialer with the timeout of WithTimeout; a
// custom dialer is called with a context that already carries that timeout
// and should honor it. The context is cancelled once the dial returns, so the
// connection must not depend on it. For the scan helpers, use
// WithDiscoverDialer, RackSlotProbeRequest.Dialer and CompareReadRequest.Dialer.
func WithDialer(d Dialer) Option {
	return func(o *options) { o.dialer = d }
}

// WithMaxPDU sets the maximum PDU size to request
func WithMaxPDU(size int) Option {
	return func(o *options) { o.maxPDU = size }
}
