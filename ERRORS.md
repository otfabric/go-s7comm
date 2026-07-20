# Errors and result semantics: otfabric/go-s7comm

This document is the canonical guide to how go-s7comm reports failures.
For method signatures and type layouts, see [API.md](API.md).

Current release: **v0.7.3** (see [RELEASE.md](RELEASE.md)).

## Table of contents

- [Quick reference](#quick-reference)
- [Outcome models](#outcome-models)
- [Structured reads (`ReadResult`)](#structured-reads-readresult)
- [Client sentinels and typed errors](#client-sentinels-and-typed-errors)
- [Wire errors](#wire-errors)
- [Detection patterns](#detection-patterns)
- [CLI contract](#cli-contract)
- [Context, reconnect, and concurrency](#context-reconnect-and-concurrency)

## Quick reference

| Situation | How it is reported |
|-----------|--------------------|
| Invalid caller input (address, options, CIDR, block number, …) | Top-level `error`: `*client.ValidationError` |
| Byte read remote outcome (success / short / empty / rejected / timeout / …) | `(*ReadResult, nil)` — inspect `Status` / `result.Err()` |
| Bit read/write, byte write, Connect, Upload, GetCPUState, … | Strict top-level `error` |
| Best-effort ops (Identify, GetBlockInfo, ListAllBlocks, ReadDiagBuffer) | May return `(value, error)` both non-nil (partial result) |
| Not connected | `client.ErrNotConnected` (on reads: usually inside `ReadResult.Cause`) |
| Request larger than negotiated PDU | `client.ErrRequestExceedsPDU` |
| Malformed protocol framing | Often wraps `client.ErrProtocolFailure` |
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
| `short-read` | `0 < returned < requested` |
| `empty-read` | Requested > 0 but returned 0 |
| `rejected` | Target returned an S7 item/header error (`ReturnCode` set when item-level) |
| `timeout` | Context or network timeout |
| `transport-error` | Connection or send/receive failure (includes not connected) |
| `protocol-error` | COTP/S7 parse, PDU size, or framing error |
| `inconclusive` | Mixed results across retries/repeats (range probe) |

**Stability:** Treat `Status` (and `ReturnCode` when rejected) as the machine-readable contract. `Message` is descriptive only. `Cause` is optional and may be joined; use `errors.Is` / `errors.As`, not exact join shape.

**Snap7 out-of-range:** Non-success Read Var items with transport size `0x00` parse as rejected items (not protocol-failure). Typical item code is address fault (`0x05`).

**Bit vs byte reads:** `ReadBit` / `WriteBit` do **not** return `ReadResult`. They return a strict `error` (`*ValidationError`, `ErrNotConnected`, `*wire.S7Error`, or wrapped `ErrProtocolFailure`).

## Client sentinels and typed errors

| Symbol | Kind | Notes |
|--------|------|--------|
| `ErrNotConnected` | sentinel | No active session |
| `ErrRequestExceedsPDU` | sentinel | Request size > negotiated PDU |
| `ErrProtocolFailure` | sentinel | Advanced/diagnostic; often wrapped on handshake/`sendReceive`/bit decode failures |
| `ErrNilReadResult` | sentinel | `(*ReadResult)(nil).Err()` |
| `ValidationError` | typed | Invalid caller input; `errors.As` |
| `PDURefMismatchError` | typed | Response PDU ref ≠ expected; setup may return bare; request path often `errors.Join(ErrProtocolFailure, …)` |
| `ReadOutcomeError` | typed | From `ReadResult.Err()`; `Unwrap()` → `Cause` |

Stable and documented for application code: `ErrNotConnected`, `ErrRequestExceedsPDU`, `PDURefMismatchError`, `ValidationError`, and `ReadStatus` values. Prefer classifying reads by `Status` over matching every wrapped cause.

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

Helpers: `HeaderErrorString`, `ItemReturnCodeString`, `ErrClassString`, `ParamErrorFromParam`, `ParamErrorCodeString`.

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

COTP/TPKT transport errors come from [go-cotp](https://github.com/otfabric/go-cotp) / [go-tpkt](https://github.com/otfabric/go-tpkt), not this module.

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

- Context cancellation is only strongly effective when the context has a deadline; prefer `context.WithTimeout` / `WithDeadline`.
- A second `Connect()` replaces the session only after the new handshake succeeds; a failed reconnect does not drop a healthy session.
- The client does not auto-reconnect; after `Close` or transport failure, call `Connect` again.
- Concurrent use is safe, but protocol ops are serialized per client (`reqMu`); long ops (e.g. `UploadBlock`) block other requests on that client.
