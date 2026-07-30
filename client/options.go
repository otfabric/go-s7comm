// SPDX-License-Identifier: MIT

// Package client provides a high-level S7 client API.
package client

import "time"

// Option configures the S7 client. Options are immutable after construction.
type Option func(*options)

type options struct {
	port          int
	rack          int
	slot          int
	localTSAP     uint16
	remoteTSAP    uint16
	useExplicit   bool
	autoRackSlot  bool
	bruteRackSlot bool
	timeout       time.Duration
	rateLimit     time.Duration
	logger        Logger
	maxPDU        int
	maxAmqCalling int
	maxAmqCalled  int
}

// Logger is an optional printf-style sink for client diagnostics.
// Nil (the default when WithLogger is omitted) disables logging.
// Methods are invoked synchronously on the request path and must not block.
// Args follow fmt.Sprintf conventions (e.g. Error("Connect failed: %v", err)).
// Use NewSlogLogger to adapt a slog.Handler without changing this interface.
type Logger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
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

// WithTimeout sets the connection/operation timeout
func WithTimeout(t time.Duration) Option {
	return func(o *options) { o.timeout = t }
}

// WithRateLimit sets a minimal delay between sequential protocol operations.
func WithRateLimit(d time.Duration) Option {
	return func(o *options) { o.rateLimit = d }
}

// WithLogger sets an optional Logger. Nil disables logging (same as omitting the option).
func WithLogger(l Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithMaxPDU sets the maximum PDU size to request
func WithMaxPDU(size int) Option {
	return func(o *options) { o.maxPDU = size }
}
