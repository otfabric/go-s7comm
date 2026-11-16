# Errors and result semantics: otfabric/go-s7comm

This document is the canonical guide to how go-s7comm reports failures.
For method signatures and type layouts, see [API.md](API.md).

Current release: **v0.7.5** (see [RELEASE.md](RELEASE.md)).

## Table of contents

- [Quick reference](#quick-reference)
- [Outcome models](#outcome-models)
- [Structured reads (`ReadResult`)](#structured-reads-readresult)
- [Client sentinels and typed errors](#client-sentinels-and-typed-errors)
- [Wire errors](#wire-errors)
- [Server errors](#server-errors)
- [Detection patterns](#detection-patterns)
- [CLI contract](#cli-contract)
- [Context, reconnect, and concurrency](#context-reconnect-and-concurrency)

## Quick reference

| Situation | How it is reported |
|-----------|--------------------|
| Invalid caller input (address, options, CIDR, block number, range of a scan, …) | Top-level `error`: `*client.ValidationError` |
| Byte read remote outcome (success / short / empty / rejected / timeout / …) | `(*ReadResult, nil)` — inspect `Status` / `result.Err()` |
| Bit read/write, byte write, Connect, Upload, GetCPUState, … | Strict top-level `error` |
| Best-effort ops (Identify, GetBlockInfo, ListAllBlocks, ReadDiagBuffer) | May return `(value, error)` both non-nil (partial result) |
| Not connected | `client.ErrNotConnected` (on reads: usually inside `ReadResult.Cause`) |
| Request larger than negotiated PDU | `client.ErrRequestExceedsPDU` |
| Malformed protocol framing | Often wraps `client.ErrProtocolFailure` |
| An answer longer than the negotiated PDU size, of a message type that does not answer the request, of another user data function, or of another SZL list | Wraps `client.ErrProtocolFailure`; the session stays open |
| A call given up after its request was sent (context ended, request timeout, transport failed) | The context or transport error; the session ends and later calls return `client.ErrNotConnected` |
| A call in flight when `Close` is called (also one waiting for its turn, and `Connect`) | Wraps `client.ErrClosed` (on reads: `ReadStatusTransportErr`); later calls return `client.ErrNotConnected` |
| A call whose context ends while it waits for its turn behind another call | The context error (on reads: `ReadStatusTimeout` for a deadline); nothing is sent and the session stays open |
| PLC header / item reject | `*wire.S7Error` (on reads: `ReadStatusRejected` + `ReturnCode`) |

Prefer:

```go
res, err := c.ReadDB(ctx, 1, 0, 16)
if err != nil {
    return err // validation / rare setup
}
if err := res.Err(); err != nil {
    return err // remote / transport / protocol outcome
}
// use res.Data
```

## Outcome models

Three patterns appear across the public API:

1. **Strict** — `Connect`, `Close`, `WriteArea` / `WriteDB`, `ReadBit` / `WriteBit`, `UploadBlock`, `GetCPUState`, `GetProtectionLevel`, …  
   Failure is a non-nil `error`. There is no `ReadResult`.

2. **Structured reads** — `ReadArea`, `ReadDB`, `ReadInputs`, `ReadOutputs`, `ReadMerkers`  
   - Top-level `error`: caller/input validation only (`*ValidationError`).  
   - Remote outcomes (including not-connected, timeout, rejection, short/empty): `(*ReadResult, nil)` with `Status` / `Cause` / `Message`.

3. **Best-effort** — `Identify`, `GetBlockInfo`, `ListAllBlocks`, `ReadDiagBuffer`  
   May return a partial value together with a non-nil `error`. Use both.

Connectionless helpers (`Discover`, `ProbeRackSlots`, `CompareRead`) create their own sessions and return their own result types; failures are mostly top-level `error` or per-candidate statuses (see [API.md](API.md)).

## Structured reads (`ReadResult`)

```go
type ReadStatus string

const (
    ReadStatusSuccess      ReadStatus = "success"
    ReadStatusShortRead    ReadStatus = "short-read"
    ReadStatusEmptyRead    ReadStatus = "empty-read"
    ReadStatusRejected     ReadStatus = "rejected"
    ReadStatusTimeout      ReadStatus = "timeout"
    ReadStatusTransportErr ReadStatus = "transport-error"
    ReadStatusProtocolErr  ReadStatus = "protocol-error"
    ReadStatusInconclusive ReadStatus = "inconclusive" // range-probe mixed results
)

type ReadResult struct {
    Status          ReadStatus
    RequestedLength int
    ReturnedLength  int
    Data            []byte
    Warnings        []string
    Message         string // human-readable; not a stable API string
    Cause           error  // optional; for errors.Is via ReadOutcomeError.Unwrap
    ItemStatus      string
    ReturnCode      byte   // S7 item return code when Status == rejected
}

func (r *ReadResult) OK() bool
func (r *ReadResult) Success() bool // same as OK()
func (r *ReadResult) Err() error    // nil | ErrNilReadResult | *ReadOutcomeError
```

| Status | Meaning |
|--------|---------|
| `success` | Returned length equals requested |
| `short-read` | `0 < returned < requested`; `Data` holds the bytes from `Start` up to the first short chunk |
| `empty-read` | Requested > 0 but returned 0 |
| `rejected` | Target returned an S7 item/header error (`ReturnCode` set when item-level) |
| `timeout` | Context or network timeout |
| `transport-error` | Connection or send/receive failure (includes not connected) |
| `protocol-error` | COTP/S7 parse, PDU size, or framing error; also a Read Var item with more bytes than requested |
| `inconclusive` | Mixed results across retries/repeats (range probe) |

**Stability:** Treat `Status` (and `ReturnCode` when rejected) as the machine-readable contract. `Message` is descriptive only. `Cause` is optional and may be joined; use `errors.Is` / `errors.As`, not exact join shape.

**Snap7 out-of-range:** Non-success Read Var items with transport size `0x00` parse as rejected items (not protocol-failure). Typical item code is address fault (`0x05`).

**Bit vs byte reads:** `ReadBit` / `WriteBit` do **not** return `ReadResult`. They return a strict `error` (`*ValidationError`, `ErrNotConnected`, `*wire.S7Error`, or wrapped `ErrProtocolFailure`).

## Client sentinels and typed errors

| Symbol | Kind | Notes |
|--------|------|--------|
| `ErrNotConnected` | sentinel | No active session: before `Connect`, after `Close`, and after a call that ended the session (see [Context, reconnect, and concurrency](#context-reconnect-and-concurrency)) |
| `ErrClosed` | sentinel | The call was in flight when `Close` was called (running, waiting for its turn, or `Connect`); a call made after `Close` returns `ErrNotConnected` instead. A call that was waiting for the peer also matches go-cotp's `cotp.ErrClosed` |
| `ErrRequestExceedsPDU` | sentinel | Request size > negotiated PDU |
| `ErrProtocolFailure` | sentinel | Advanced/diagnostic; often wrapped on handshake/`sendReceive`/bit decode failures |
| `ErrNilReadResult` | sentinel | `(*ReadResult)(nil).Err()` |
| `ValidationError` | typed | Invalid caller input; `errors.As` |
| `PDURefMismatchError` | typed | Response PDU ref ≠ expected; setup may return bare; request path often `errors.Join(ErrProtocolFailure, …)` |
| `ReadOutcomeError` | typed | From `ReadResult.Err()`; `Unwrap()` → `Cause` |

Stable and documented for application code: `ErrNotConnected`, `ErrClosed`, `ErrRequestExceedsPDU`, `PDURefMismatchError`, `ValidationError`, and `ReadStatus` values. Prefer classifying reads by `Status` over matching every wrapped cause.

## Wire errors

Useful when inspecting PLC responses or building custom PDUs. The high-level client already maps most of these into `ReadResult` statuses or wrapped errors.

### `S7Error`

```go
type S7Error struct {
    Class   byte
    Code    byte
    Message string
}
```

- Header errors: `Class` / `Code` from the S7 ack header (`NewS7Error`, `NewS7ErrorWithParam`).
- Item rejects: often `Code` = item return code via `ReturnCodeError` (e.g. address fault `0x05`).
- Some parse failures use Message-only `S7Error` (no Class/Code).
- User data refusals (SZL reads, block functions): `Code` is the item return code of the data section, as for a read (`0x0A` when the list or block does not exist). When the parameter section carries an error code as well, the message names it: a recorded CPU 315-2 PN/DP refuses an SZL list with `0xD402` and a block with `0xD209`, each with return code `0x0A`, and `GetBlockInfo` for that block returns `Code` `0x0A` with the message `object not available (parameter error 0xD209: one of the given blocks not found on module)`. An error code on an answer whose data section does not report a refusal is returned with `Class` its high byte and `Code` its low byte, and the data is not used.

Helpers: `HeaderErrorString`, `ItemReturnCodeString`, `ErrClassString`, `ParamErrorFromParam`, `ParamErrorCodeString`.

### Refused access: item codes and header errors

A PLC can refuse access in two different places. Keep them apart in error handling; one does not imply the other.

| Where | Codes | What it means | In this library |
|---|---|---|---|
| Item return code of one Read Var or Write Var item | `0x03` | Object access is not allowed, e.g. a write to a write-protected DB | `S7Error` with `Class` 0, `Code` `0x03` ("access denied"); reads: `ReadStatusRejected`, `ReturnCode` `0x03` |
| | `0x05` | Invalid address (syntax ID, range, type, bit number) | `Code` `0x05` |
| | `0x0A` | The object does not exist, or the range limit was exceeded | `Code` `0x0A` |
| Header error class and code of the whole job | `0x81/0x04`, `0x85/xx` | The job is refused, e.g. PUT/GET access is disabled on an S7-1200/1500 | `S7Error` with `Class` and `Code` |
| | `0x83/0x04` | The same refusal in the form an S7-300 sends ([Apache PLC4X, 2026-08-16](https://www.mail-archive.com/commits@plc4x.apache.org/msg24099.html)); `HeaderErrorString` gives the generic Siemens text for this code ("no further parallel upload possible") | `S7Error` `Class` `0x83`, `Code` `0x04` |

The item codes are the Siemens ones: STEP 7 V20, [Use of FETCH/WRITE (S7-300, S7-400)](https://docs.tia.siemens.cloud/r/en-us/v20/editing-devices-and-networks/configure-networks/communication-via-connections/connection-types-s7-300-s7-400-s7-1500/fetch/write-mode-s7-300-s7-400/use-of-fetch/write-s7-300-s7-400), diagnostic-buffer codes 03H "Object access is not allowed", 05H "Invalid address", 0AH "The object does not exist, or the range limit was exceeded"; Wireshark's s7comm dissector and the PLC4X S7 model name them the same way. Snap7-based servers send `0x0A` for every refused write, including a write to a read-only area, because Snap7 has no code for refused access ([snap7-interop COMPATIBILITY.md](https://github.com/otfabric/snap7-interop/blob/main/docs/COMPATIBILITY.md#write-to-a-read-only-area-0x03-and-0x0a)); a client must therefore not read `0x0A` as "the DB does not exist" without more context.

Recorded evidence ([testdata/captures](testdata/captures/README.md)): a CPU 315-2 PN/DP (firmware V3.2.7) answers one Write Var of M, I, Q, a timer and a counter with `FF FF FF 03 03`, refusing the writes to the existing timer and counter as object access not allowed, while it reads all five; it answers a read of a DB that does not exist with `0x0A`. `ParseWriteVarResponse` reports the first refused item of such an answer; package `server` refuses writes to its timers and counters the same way. A CPU 1214C DC/DC/DC (S7-1200, firmware V4.2) with DB 1 write-protected and PUT/GET enabled answers a python-snap7 write to DB1.DBB0 with item code `0x03` and no header error ([python-snap7#976](https://github.com/gijzelaerr/python-snap7/issues/976#issuecomment-6800464989); `wire` `TestCapturesWriteProtectedDBIsAccessDenied`): the code package `server` sends for a read-only DB, and not the `0x0A` of Snap7-based servers ([snap7-interop#19](https://github.com/otfabric/snap7-interop/issues/19)). No capture holds an S7-300, S7-400 or S7-1500 answering this case.

### Other wire types and sentinels

| Symbol | Notes |
|--------|--------|
| `UnsupportedSyntaxError` | From `ValidateRequestSyntax` when syntax ≠ S7ANY |
| `ErrShortS7Header` | Framing |
| `ErrInvalidS7ProtocolID` | Framing |
| `ErrShortS7AckHeader` | Framing |
| `ErrS7PayloadLength` | Framing |
| `ErrTruncatedItemHeader` | Read Var item parse |
| `ErrTruncatedItemPayload` | Read Var item parse |
| `ErrInvalidParamLength` | Reserved: exported, but no function in this module returns it today |
| `ErrInvalidDataLength` | Reserved: exported, but no function in this module returns it today |
| `ErrUnknownBlockType` | `EncodeStartUploadRequest` input |
| `ErrMalformedRequest` | Server-side decoders (`ParseSetupCommRequest`, `ParseReadVarRequest`, `ParseReadVarDBRead`, `ParseWriteVarRequest`): a job's parameter or data section does not match its declared layout |
| `ErrOutOfRange` | Request encoder input that does not fit its wire field (`EncodeS7Header`, `EncodeS7Any`, `EncodeS7AnyBit`, Read/Write Var and Setup Communication encoders, `EncodeStartUploadRequest` block number). The client checks the same limits first and returns `*ValidationError` |

COTP/TPKT transport errors come from [go-cotp](https://github.com/otfabric/go-cotp) / [go-tpkt](https://github.com/otfabric/go-tpkt), not this module.

## Server errors

Package `server` (the in-memory test server) returns plain errors that wrap these sentinels; match them with `errors.Is`.

| Sentinel | Returned by | Meaning |
|----------|-------------|---------|
| `server.ErrServerClosed` | `Serve`, `ListenAndServe` | The server was closed with `Close` (`ctx.Err()` is returned instead when `ctx` ends) |
| `server.ErrUnknownArea` | `RegisterArea`, `RegisterDB`, `Read`, `Write` | The area or DB number is not registered, or not one the server holds; `RegisterArea(AreaDB, …)` also returns it (use `RegisterDB`) |
| `server.ErrAreaExists` | `RegisterArea`, `RegisterDB` | The area or DB number is already registered |
| `server.ErrOutOfRange` | `RegisterArea`, `RegisterDB`, `Read`, `Write` | The area size is not in 1..`MaxAreaSize`, or the span is not inside the area |

## Detection patterns

```go
import (
    "errors"

    "github.com/otfabric/go-s7comm/client"
    "github.com/otfabric/go-s7comm/wire"
)

// Validation
var ve *client.ValidationError
if errors.As(err, &ve) { /* bad input */ }

// Not connected (strict APIs or ReadResult.Cause)
if errors.Is(err, client.ErrNotConnected) { /* … */ }

// Ended by Close while in flight
if errors.Is(err, client.ErrClosed) { /* … */ }

// PDU size
if errors.Is(err, client.ErrRequestExceedsPDU) { /* … */ }

// PDU reference mismatch
var pref *client.PDURefMismatchError
if errors.As(err, &pref) { /* … */ }

// PLC / item reject
var s7err *wire.S7Error
if errors.As(err, &s7err) { /* s7err.Code, s7err.Class */ }

// Structured read
if res, err := c.ReadArea(ctx, addr); err != nil {
    // validation
} else if err := res.Err(); err != nil {
    switch res.Status {
    case client.ReadStatusRejected:
        // res.ReturnCode
    case client.ReadStatusTimeout:
        // …
    }
}
```

## CLI contract

For CLIs (e.g. s7commctl) that must map library outcomes to exit codes:

- **Top-level `error`**: connection/setup/validation failure → non-zero exit; do not treat as success.
- **`ReadResult.Status`**: if `err == nil` but `!result.OK()`, treat as failure unless the CLI explicitly allows it (e.g. `--allow-short`).
- **Default**: only `success` is success; `short-read`, `empty-read`, `rejected`, and other non-success statuses fail.
- **`--strict-read`** (if implemented): fail when status ≠ `success` or `ReturnedLength != RequestedLength`.
- **`--allow-short`** (if implemented): may treat short/empty as success for exploration while still printing the real status.

## Context, reconnect, and concurrency

- **Cancel without deadline** is effective on TCP dial (with a custom `Dialer`,
  only if it honors `ctx`) and go-cotp `WriteTSDU` /
  `ReadTSDU` (go-cotp arms a watcher that forces a past deadline on `ctx.Done`).
- **A call that is given up after its request was sent ends the session.** When
  the context of a call ends (cancel or deadline) once the request has been
  sent, or the transport fails during the exchange, the client closes the
  connection and clears the session. The call returns its context or transport
  error; `ConnectionInfo()` and `PDUSize()` then report zero values; every
  later call returns `ErrNotConnected` (reads: status `transport-error`) and
  sends nothing, until `Connect` succeeds again. The reason is the protocol:
  answers come back in order on the one connection, so the answer to the
  request that was given up would be the first thing the next call reads.
  go-cotp itself keeps a connection open when a context ends a read before
  any octet arrived, which is why the client ends the session.
- A context that has already ended when a call starts sends nothing and leaves
  the session as it is. An answer that arrives and is refused (another PDU
  reference, a message type that does not answer the request, a length above
  the negotiated PDU size, content that does not decode) also leaves it open:
  the stream is still aligned, and the next call is answered.
- Prefer `context.WithTimeout` / `WithDeadline` for wall-clock bounds on an
  operation. Client option `WithTimeout` applies to **Connect**/dial (and is
  wrapped into the COTP and S7 setup contexts); it does **not** bound later
  calls. `WithRequestTimeout(d)` (default 0: none) bounds each request after
  Connect: sending one request PDU and waiting for its answer, each request
  of a call of several (chunks, follow-up packets, upload segments)
  separately. The earlier of it and the call's context deadline wins. A
  request that runs out of time returns `context.DeadlineExceeded` (reads:
  status `timeout`) and, once the request was sent, ends the session like a
  context deadline. It does not bound the pauses of `WithRateLimit`, the
  wait for the client's turn, or Connect.
- A call waits for its turn while another call on the same client is in
  flight. When its context ends while it waits, it returns that context's
  error, sends nothing and leaves the session as it is.
- A `Dialer` error is returned wrapped as `TCP connect: …`, so `errors.Is` /
  `errors.As` reach it. With `WithAutoRackSlot`, the final
  `auto rack/slot connect failed …` error wraps the last attempt's error.
  The scan helpers report it per attempt: `CompareRead` in
  `ReadResult.Cause`, `ProbeRackSlots` as the text of
  `RackSlotCandidate.Error` (stage `tcp`); `Discover` counts it as a failed
  rack/slot attempt.
- Without cancel, a deadline **or** a request timeout, open-session TSDU I/O can block
  indefinitely, until the transport fails or `Close` ends the call.
- Rate-limit waits, multi-chunk loops, discover/probe between jobs, and
  auto rack/slot attempts honor `ctx.Err()` / `ctx.Done()`.
- `UploadBlock` end-upload cleanup uses `context.WithTimeout(context.Background(), 500ms)`
  and ignores the caller context (best-effort; cleanup errors are logged only).
  When the upload ended the session, the cleanup sends nothing.
- `UploadBlock` asks for each segment once. A failure on a segment ends the
  upload; the request is not repeated, because the CPU answers each request
  with the next segment.
- A second `Connect()` replaces the session only after the new handshake succeeds;
  a failed reconnect does not drop a healthy session.
- The client does not auto-reconnect; after `Close`, a call that ended the
  session or a transport failure, call `Connect` again.
- Concurrent use is safe, but protocol ops are serialized per client (`reqLock`);
  long ops (e.g. `UploadBlock`) block other requests on that client, which wait
  for their turn until their context ends.
- `Close` is safe to call more than once and from multiple goroutines. It
  ends the call in flight and does not wait for it: it closes the connection,
  so a call waiting for the peer returns at once, and it ends a `Connect` in
  flight (its dial, handshake or setup). Every call in flight when `Close` is
  called, also one waiting for its turn, returns an error that matches
  `ErrClosed` (reads: status `transport-error`); one that was waiting for the
  peer also matches `cotp.ErrClosed`. A call in a pause of `WithRateLimit`
  returns at once too, and sends nothing more. A `Connect` that `Close`
  ended leaves no session, also when it was to replace one. Calls made after
  `Close` return `ErrNotConnected` until `Connect` succeeds again.
- A `Logger` and a `Dialer` run on the goroutine of the call that invokes
  them, while that call holds the client's turn. They may call
  `ConnectionInfo`, `PDUSize` and `Close`; `Close` ends that call as any call
  in flight. An operation or `Connect` of the same client called from one of
  them waits for that call, so it returns only when its own context ends,
  with that context's error. A callback that does not return normally is not
  recovered by the client: the unwinding reaches the caller of the call,
  which gives the client's turn back; the session in place is kept.
