// SPDX-License-Identifier: MIT

package client

import "errors"

// ErrNilReadResult is returned by ReadResult.Err() when the receiver is nil.
var ErrNilReadResult = errors.New("read result is nil")

// ReadStatus classifies the outcome of a read request.
type ReadStatus string

const (
	ReadStatusSuccess      ReadStatus = "success"    // returned length == requested
	ReadStatusShortRead    ReadStatus = "short-read" // 0 < returned < requested
	ReadStatusEmptyRead    ReadStatus = "empty-read" // requested > 0, returned == 0
	ReadStatusRejected     ReadStatus = "rejected"   // target/S7 explicitly rejected
	ReadStatusTimeout      ReadStatus = "timeout"    // transport/context timeout
	ReadStatusTransportErr ReadStatus = "transport-error"
	ReadStatusProtocolErr  ReadStatus = "protocol-error"
	ReadStatusInconclusive ReadStatus = "inconclusive" // retries gave mixed results
)

// ReadResult holds the full outcome of a read (single logical read, possibly chunked).
// Status is the canonical machine-readable outcome; use it to branch on outcome.
// Err() is a convenience adapter for idiomatic Go flow ("if err := res.Err(); err != nil").
// Message is a human-readable description and is not part of the stable API contract.
// Cause may be nil even for failed outcomes; callers should not rely on exact wrapped
// error types except documented sentinels (e.g. ErrNotConnected); Status is more stable than Cause.
type ReadResult struct {
	Status          ReadStatus
	RequestedLength int
	ReturnedLength  int
	Data            []byte
	Warnings        []string
	Message         string // human-readable outcome; descriptive, not stable API

	// Cause is the underlying error when available (e.g. for errors.Is); optional and non-stable.
	Cause error

	// Protocol detail (per-item return code when available)
	ItemStatus string
	ReturnCode byte
}

// OK returns true if Status == ReadStatusSuccess.
func (r *ReadResult) OK() bool {
	return r != nil && r.Status == ReadStatusSuccess
}

// Success returns true if the read succeeded (same as OK). Prefer "if err := res.Err(); err != nil" for flow.
func (r *ReadResult) Success() bool {
	return r.OK()
}

// Err returns a non-nil error when Status is not success, for use in "if err := result.Err(); err != nil".
// Returns ErrNilReadResult when the receiver is nil so callers can use errors.Is(err, ErrNilReadResult).
func (r *ReadResult) Err() error {
	if r == nil {
		return ErrNilReadResult
	}
	if r.Status == ReadStatusSuccess {
		return nil
	}
	msg := string(r.Status)
	if r.Message != "" {
		msg = r.Message
	}
	return &ReadOutcomeError{Result: r, message: msg, cause: r.Cause}
}

// ReadOutcomeError wraps a failed read result for use as error.
// It implements Unwrap() so callers can use errors.Is/As on the underlying cause.
type ReadOutcomeError struct {
	Result  *ReadResult
	message string
	cause   error
}

func (e *ReadOutcomeError) Error() string {
	return e.message
}

// Unwrap returns the underlying error (e.g. context.DeadlineExceeded, ErrNotConnected) when present.
func (e *ReadOutcomeError) Unwrap() error {
	return e.cause
}
