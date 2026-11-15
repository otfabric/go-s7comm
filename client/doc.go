// SPDX-License-Identifier: MIT

// Package client provides an S7 protocol client for connecting to Siemens PLCs,
// reading and writing memory areas, and probing rack/slot and discovery.
//
// Outcome models: Strict (Connect, Close, WriteArea, WriteDB, UploadBlock, GetCPUState, GetProtectionLevel)
// return error on failure. Structured (ReadArea, ReadDB, etc.)—validation as error; remote outcomes in
// ReadResult.Status; use result.Err() for flow. Best-effort (Identify, GetBlockInfo, ListAllBlocks, ReadDiagBuffer)
// may return partial result with non-nil error.
//
// # Connection and “connected” state
//
// The client is “connected” after Connect() returns nil. Then the client holds
// an active TCP + COTP + S7 setup session. ReadArea, WriteArea, Identify, and
// other operations require the client to be connected; otherwise they return
// ErrNotConnected. A second Connect() replaces the session only after the new handshake succeeds. Use Close() to disconnect.
//
// # Read outcomes and “success”
//
// For reads, prefer: if err != nil { return err }; if err := res.Err(); err != nil { return err }; then use res.Data.
// Status is the canonical outcome; Err() is the convenience adapter; Message is descriptive only. ReadStatus values:
//
//   - success: returned length equals requested.
//   - short-read: 0 < returned < requested.
//   - empty-read: requested > 0 but returned 0.
//   - rejected: target returned an S7 error/return code.
//   - timeout: context or network timeout.
//   - transport-error: connection or send/receive failure.
//   - protocol-error: COTP/S7 parse or length error.
//   - inconclusive: repeated reads gave mixed results (e.g. range probe).
//
// # Probe statuses (rack/slot probing)
//
// ProbeRackSlots returns candidates with ProbeStatus:
//
//   - unreachable: TCP connect failed.
//   - tcp-only: TCP ok, COTP failed.
//   - cotp-only: COTP ok, S7 setup failed.
//   - setup-only: S7 setup ok, no follow-up (non-strict mode).
//   - valid-connect: Setup ok, follow-up query failed or not run.
//   - valid-query: Setup ok and a follow-up SZL/query succeeded (strict mode).
//   - rejected: target returned S7 error.
//   - timeout: any stage timed out.
//   - flaky: retries gave mixed results.
//
// In non-strict mode, “valid” means setup-only or valid-connect. In strict
// mode, only valid-query is considered valid.
//
// # Partial results
//
// Identify, GetBlockInfo, ListAllBlocks, and ReadDiagBuffer may return partial data with
// a non-nil error (one of two SZL lists failed, an answer could not be decoded, a block
// type could not be listed). Use result.Status and errors to distinguish full success from
// partial results. SZL, block and upload requests are those Snap7 and TIA Portal send, and
// answers are decoded by SZL record index, as recorded from real CPUs in testdata/captures.
// Serialization (e.g. JSON) of result types is the responsibility of the caller; this
// package does not define wire or format contracts.
//
// # Errors and sentinels
//
// Stable and documented: ErrNotConnected, ErrRequestExceedsPDU, PDURefMismatchError,
// ValidationError, ReadStatus values. Use errors.As(err, &ValidationError{}) for
// caller-input validation; errors.As(err, *PDURefMismatchError) for response correlation.
// ErrProtocolFailure is advanced/diagnostic (malformed protocol framing); handshake and
// request path wrap it for classification. Returned errors may wrap multiple underlying
// errors; use errors.Is and errors.As; do not rely on exact joined structure.
//
// # Validation vs read outcome
//
// Caller/input validation errors (e.g. negative start/size, invalid range) are
// returned as a non-nil error. Remote/read outcome errors (timeout, rejected,
// short read, etc.) are represented in ReadResult.Status and result.Err().
//
// # Options
//
// Options passed to New are immutable after construction.
//
// # Discovery and probing (connectionless helpers)
//
// Discover, ProbeRackSlots, and CompareRead are connectionless discovery/probe helpers,
// distinct from connected client operations (ReadArea, WriteArea, etc.). They create
// one or more TCP connections
// (Discover and ProbeRackSlots per host or per candidate; CompareRead per candidate).
// On large networks, use bounded CIDR ranges, timeouts, and parallelism options to
// avoid excessive connection churn. Safe defaults: limit discovery to /24 or smaller
// where possible, set WithDiscoverTimeout/WithTimeout, and use moderate Parallelism.
// SafetyMode (conservative/normal/aggressive), optional jitter, and max attempts per host
// are supported for ProbeRackSlots and Discover to reduce connection churn on sensitive OT networks.
// Each helper dials through a Dialer when one is set (WithDiscoverDialer,
// RackSlotProbeRequest.Dialer, CompareReadRequest.Dialer), as Connect does with WithDialer.
//
// ProbeReadableRanges runs over a single client connection; probes are serialized.
// RangeProbeRequest.Parallelism is retained for API consistency but does not increase
// wire-level concurrency—only one probe runs at a time. Do not rely on it for throughput.
//
// # Retry and resilience
//
// No request is repeated by the client: ReadArea, WriteArea, CompareRead and
// UploadBlock (one request per segment) return the first failure. Range probes
// support Retries and Repeat for stability. Callers can wrap calls in their own
// backoff/retry if needed. A call that is given up after its request was sent
// (its context ended, or the transport failed) ends the session, so a retry
// starts with Connect.
//
// # Concurrency
//
// Concurrent use is safe, but all protocol operations are serialized per client instance.
// Only one request or connection transition is in flight at a time; long operations
// (e.g. UploadBlock) block other reads and writes on that client. Typical use:
// Connect once, perform operations, then Close. Calling methods after Close
// returns ErrNotConnected. Close is safe to call more than once and from
// multiple goroutines; it waits for the operation in flight and does not
// interrupt it. A Logger and a Dialer run while an operation is in flight:
// they may call ConnectionInfo and PDUSize, and must not call an operation,
// Connect or Close of the same client.
//
// # Reconnection
//
// Reconnecting after transport failure or intentional Close is supported: call
// Connect(ctx) again. The client does not auto-reconnect. A transport failure,
// and a call whose context ends after its request was sent, end the session
// (see Connect): calls then return ErrNotConnected until Connect succeeds.
//
// # Negotiated PDU size
//
// ConnectionInfo() is the canonical source for negotiated connection state; ConnectionInfo().PDUSize
// is the negotiated maximum S7 PDU payload length (bytes). PDUSize() is a shorthand for it.
// All request sizing and chunk planning use this value; sendReceive rejects any request whose
// S7 payload length exceeds it, and any answer longer than it. It is never above the size
// proposed with WithMaxPDU. SECURITY.md lists every limit on what a peer can send.
package client
