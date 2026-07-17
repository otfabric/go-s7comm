# go-s7comm Releases

## v0.7.1

**Date:** 2026-07-17
**Previous release:** v0.7.0

### Summary

- **Native bit API**: Public `model.BitAddress` plus `client.ReadBit` / `WriteBit` / `ReadDBBit` / `WriteDBBit` using S7 BIT transport (not byte read-modify-write). Wire helpers: `S7AnyBitAddress`, `EncodeS7AnyBit`, `EncodeReadVarBitRequest`, `EncodeWriteVarBitRequest`, `DecodeAsBit`. Existing byte `EncodeS7Any` / ReadArea / WriteArea APIs unchanged. Interop `HasBit` steps use the public bit API.

### Breaking changes

- None (additive).

---

## v0.7.0

**Date:** 2026-07-17
**Previous release:** v0.6.3

## Summary

- **go-cotp TP0 service**: Live client paths use [go-cotp](https://github.com/otfabric/go-cotp) `v1.0.0-rc.1` (`cotp.Connect` / `ReadTSDU` / `WriteTSDU`). S7 PDUs are exchanged as complete TSDUs; TPKT framing and COTP CR/CC/DT segmentation are owned by go-cotp.
- **Removed `transport` package**: Deleted the local TCP/TPKT wrapper. The client dials TCP and completes the TP0 handshake via go-cotp directly (dial helpers live in `client`).
- **Dependencies**: Direct module dependency is `github.com/otfabric/go-cotp` only. `github.com/otfabric/go-tpkt` is transitive via go-cotp (no production or test imports of go-tpkt in this module).
- **wire**: Replaced `InspectFrame` with `InspectTPDU` (COTP TPDU payload, no TPKT header). Removed obsolete COTP encode helpers from the wire surface/docs (`EncodeCOTPCR` / `EncodeCOTPDT`); COTP framing is owned by go-cotp. Non-success Read Var items with transport size `0x00` (Snap7 address faults) parse without error so callers see the item return code.
- **Interop**: New `interop/` suite (`//go:build interop`) runs the full [snap7-interop](https://github.com/otfabric/snap7-interop) v0.1.0 canonical fixture matrix (15 fixtures) against both published servers (native Snap7 and pure-Python) on host ports 1102/2102, rack/slot 0/2. Vendored compiled fixtures under `interop/testdata/fixtures/`. `make interop` / CI workflow `.github/workflows/interop.yml` on every push and PR (pinned GHCR image digests).
- **Docs**: README and API.md aligned with the TSDU-based stack, package layout (no `transport`), tables of contents, interop usage, and v0.7.0.

## Breaking changes

- Package `github.com/otfabric/go-s7comm/transport` removed. Use go-cotp directly for custom TP0 sessions; the high-level `client` package already does.
- `wire.InspectFrame` replaced by `wire.InspectTPDU` (input is a COTP TPDU, not a full TPKT frame). `FrameSummary.TPKTLength` renamed to `TPDULength`.

---

## v0.6.3

**Date:** 2026-03-24
**Previous release:** v0.6.2

## Summary

- **Module rename**: Module path changed from `github.com/otfabric/s7comm` to `github.com/otfabric/go-s7comm` to align with the `go-` prefix naming convention used across otfabric Go libraries (e.g. go-tpkt, go-cotp).
- **Minimum Go version**: Raised from 1.22 to **1.23** in `go.mod`.
- **CI workflow**: Replaced single `test.yml` with dedicated `ci.yml` that calls the shared reusable workflow `otfabric/.github/.github/workflows/go-ci.yml@v2`. Test matrix covers Go 1.23–1.26 on ubuntu and Windows; coverage uploads to Codecov.
- **Release workflow**: New `release.yml` calls the shared reusable workflow `otfabric/.github/.github/workflows/go-package-release.yml@v2` with `workflow_dispatch` for manual patch/minor/major bumps.

## Breaking changes

- **Import path**: All consumers must update their imports from `github.com/otfabric/s7comm/...` to `github.com/otfabric/go-s7comm/...`.

---

## v0.6.2

**Date:** 2026-03-13
**Previous release:** v0.6.1

## Summary

- **Parameter-level error codes**: `wire.ParamErrorFromParam` and `wire.ParamErrorCodeString` for 16-bit S7 parameter error codes; `wire.NewS7ErrorWithParam` uses the param section when present so responses report clearer messages (e.g. "block not found", "invalid request length"). Client and handshake use it when header error and param are available.
- **Response transport sizes**: Extended to DATE, TOD, TIME, S5TIME, DT (9–12, 15), COUNTER/TIMER (28, 29), and IEC counter/timer (30, 31), HS counter (32). `NormalizeResponseDataLength` handles all; `ResponseTransportSize` constants and `String()` for diagnostics.
- **Memory areas**: `AreaDataRecord` (0x01), `AreaS7200AO` (0x07), `AreaIECCounter200` (30), `AreaIECTimer200` (31); `ValidateArea` accepts them.
- **Diagnostic string helpers**: `wire.FunctionCodeString(code)` (e.g. "Read Var", "Upload"), `wire.AreaString(area)` (e.g. "I", "DB", "M"), `wire.SyntaxIDString(syntax)` (e.g. "S7ANY", "DBREAD"), `wire.ErrClassString(class)` (e.g. "Access error"); `ResponseTransportSize.String()` for item-level logging.
- **SZL ID catalog**: `wire.SZLIDString(id)` and expanded `szlIDNames` for diagnostics and logging.
- **Param error and SZL maps**: More 16-bit parameter error codes (D003–D00B, D016–D017, D043–D04A, D061–D063, D081–D082, D0A1–D0AE, D0C1–D0C2, D250–D252, D2A1–D2A4, D401–D402, D601–D602) and additional SZL IDs in the name maps.

## Breaking changes

- None.

---
## v0.6.0

**Date:** 2026-03-13
**Previous release:** v0.5.1

## Summary

- **ReadResult API**: Field `Error` renamed to `Message` (human-readable only; not stable API). Added `Success()` (same as `OK()`). Documented `Status` as canonical outcome, `Err()` as convenience adapter, and `Cause` as optional/non-stable.
- **Package docs**: Outcome models (Strict / Structured / Best-effort) and canonical read pattern (`err` then `res.Err()` then `res.Data`). Stable sentinels: `ErrNotConnected`, `ErrRequestExceedsPDU`, `PDURefMismatchError`, `ValidationError`, `ReadStatus`; `ErrProtocolFailure` documented as advanced/diagnostic.
- **Connect reconnect**: Dial and handshake the new connection first; existing session is replaced only after the new handshake succeeds. Failed reconnect no longer drops a healthy session.
- **ValidationError everywhere**: All caller-input validation now returns `*ValidationError`: `Connect` (port, timeout, max PDU, rack/slot), `UploadBlock`, `GetBlockInfo`, `Discover` (rack/slot, jitter, maxAttempts), `streamCIDR`/`expandCIDR` (CIDR validation). Use `errors.As(err, &ValidationError{})` to detect.
- **Handshake protocol errors**: COTP/S7 decode and shape failures in `performCOTPConnect` and `performS7Setup` wrap `ErrProtocolFailure`; setup PDU ref mismatch returns `PDURefMismatchError`.
- **Request validation**: `CompareReadRequest`: timeout ≥ 0, each candidate rack/slot validated; zero candidates allowed. `RackSlotProbeRequest`: non-empty address, port 0..65535, rack 0..7, slot 0..31 (and min ≤ max).
- **GetBlockInfo**: Documented contract: transport/protocol failure → `(nil, err)`; parse failure after transport success → partial `BlockInfo` (Type, Number) plus `err`.
- **Context cancellation**: README and transport docs note that cancellation is strongly effective only when the context has a deadline.
- **Discover**: README recommends conservative CIDR ranges (e.g. `/24` or smaller) in OT environments.
- **UploadBlock**: End-upload cleanup timeout reduced from 2s to 500ms. Documented: best-effort cleanup before return may add a short delay; cleanup errors not returned.
- **WriteArea**: Documented that the number of bytes written is `len(data)`; `addr.Size` is ignored.
- **UploadBlock tests**: Empty payload error, protocol failure on chunk (no retry), cleanup failure does not change result, context deadline returns promptly.
- **Method docs**: Best-effort first sentence for `Identify`, `GetBlockInfo`, `ListAllBlocks`, `ReadDiagBuffer`; use-case sentence for `CompareRead`.

## Breaking changes

- **ReadResult**: Field `Error` renamed to `Message`. Callers that read or set `result.Error` must switch to `result.Message`.

---
## v0.5.1

**Date:** 2026-03-13
**Previous release:** v0.5.0

## Summary

- **Test coverage**: Raised from ~32% to **≥75%** (patch release focused on tests and one small client fix).
- **model**: Added tests for all `Decode*`/`Encode*` (Byte, Int, DInt, Real, Bool, Word, DWord, etc.), short-buffer behaviour, and full `Area`/`BlockType`/`BlockLang` `String()` branches.
- **wire**: New or extended tests for setup (Encode/Parse SetupComm), errors (`S7Error`, `NewS7Error`, `ReturnCodeError`), SZL (Encode/Parse, error paths), block list (EncodeBlockListRequest, ParseBlockListResponse), upload (EncodeStartUploadRequest, EncodeUploadRequest, EncodeEndUploadRequest, ParseStartUploadResponse invalid), read/write (EncodeWriteVarRequest, ParseWriteVarResponse branches), and S7 header (ParseS7Header ack, too short, payload length error).
- **transport**: Tests for Send, SendContext, Close, LocalAddr, RemoteAddr, SetTracer; SendContext/ReceiveContext with cancelled context; real TCP for addr and close.
- **client**: Fake TCP server tests for Connect, ReadDB/ReadInputs/ReadMerkers/WriteDB, Identify, GetCPUState, GetProtectionLevel, ReadDiagBuffer, ProbeReadableRanges (with Repeat), and rate limit. Standalone tests for ReadArea outcomes: empty, rejected, short-read, protocol error, zero items; not-connected and context-cancelled. CompareRead tests (two candidates, same/different data). Probe tests: setup-only (non-strict), strict with SZL, strict with CPU state. Options tests (WithTSAP, WithAutoRackSlot, WithRateLimit, WithLogger, WithMaxPDU). Discover (options, expandCIDR /31). Read/write error-path tests (not connected, context cancelled).
- **Client fix**: `sendReceive` now checks for nil connection and returns `errors.New("not connected")` instead of panicking when the client is not connected.

## Breaking changes

- None.

---
## v0.5.0

**Date:** 2026-03-13
**Previous release:** v0.4.0

## Summary

- **Adopt go-tpkt and go-cotp**: TPKT framing and COTP encode/decode now use the shared libraries [github.com/otfabric/go-tpkt](https://github.com/otfabric/go-tpkt) and [github.com/otfabric/go-cotp](https://github.com/otfabric/go-cotp). Transport layer uses `tpkt.Reader`/`tpkt.Writer`; Send/Receive operate on TPDU payload (e.g. COTP bytes) with TPKT applied internally. Wire package exposes `EncodeCOTPCR` and `EncodeCOTPDT` (using go-cotp); decoding uses `cotp.Decode` from go-cotp. `InspectFrame` uses `tpkt.Parse` and `cotp.Decode`.
- **go-cotp v0.1.2**: Dependency updated to [go-cotp v0.1.2](https://github.com/otfabric/go-cotp/releases/tag/v0.1.2) (detection parity, error semantics, doc fixes).
- **API.md improvements**: Transport invariant documented (Receive returns one complete COTP TPDU; no raw S7). Wire section clarifies that `EncodeCOTPCR`/`EncodeCOTPDT` return COTP payload only and must be sent via transport.Send without double TPKT framing. Added CLI contract note: when exit code is driven by top-level error vs ReadResult.Status, default treatment of short/empty-read as failure, and recommended behavior for `--strict-read` / `--allow-short`.
- **CI and tooling**: Single workflow [test.yml](.github/workflows/test.yml) replaces ci.yml and release.yml. Test job runs on ubuntu and Windows with Go 1.25.x (vet, test, mod verify); coverage job uploads to Codecov and artifacts; lint job runs staticcheck and golangci-lint. Releases are done manually. README badges aligned (Go, License, Go Report Card, CI, Codecov, Release). `.golangci.yml` updated to config version `"2"` for golangci-lint v2 compatibility.

## Breaking changes

- **wire**: Removed `EncodeTPKT`, `ParseTPKT`, `EncodeCOTPData`, `ParseCOTP`, and the old `COTP`/`TPKT` types. Use `wire.EncodeCOTPCR` / `wire.EncodeCOTPDT` (return `([]byte, error)`), and `cotp.Decode` for parsing. Use `tpkt.Encode`/`tpkt.Parse`/`tpkt.Decode` from go-tpkt when building or parsing TPKT frames.
- **transport**: `Send` now expects TPDU payload (e.g. COTP bytes); it writes one TPKT frame. `Receive` returns the TPKT payload only (no longer the full TPKT frame bytes). Callers that previously built full TPKT frames and passed them to `Send` must now pass only the inner payload.

---
## v0.4.0

**Date:** 2026-03-13
**Previous release:** v0.3.1

## Summary

**Phase 1 – Read result model**

- **Rich read result model**: Read operations now return `*ReadResult` instead of `([]byte, error)`. `ReadResult` includes `Status` (success, short-read, empty-read, rejected, timeout, transport-error, protocol-error), `RequestedLength`, `ReturnedLength`, `Data`, `Warnings`, `Error`, and optional protocol detail (`ItemStatus`, `ReturnCode`).
- **Explicit classification**: Empty reads and short reads are never reported as success. Use `result.OK()` for success, `result.Err()` for a failed read outcome, and `result.Data` for the payload.
- **Read API change**: `ReadArea`, `ReadDB`, `ReadInputs`, `ReadOutputs`, and `ReadMerkers` now return `(*ReadResult, error)`. The second return value is reserved for connection/setup failures; read outcome (including rejection or short/empty) is in `result.Status`.
- **Helpers**: `ReadResult.OK()` and `ReadResult.Err()` for simple checks; `ReadOutcomeError` type for error wrapping.

**Phase 2 – Range scan**

- **ProbeReadableRanges**: `(c *Client) ProbeReadableRanges(ctx, req)` scans an area over [Start, End) by Step, one read of ProbeSize bytes per offset. Client must be connected. Returns `RangeProbeResult` with `Spans` (consolidated adjacent same-status ranges), `Probes` (raw per-offset observations), and `Summary` (ReadableSpans, EmptySpans, FailedSpans, InconclusiveSpans).
- **Options**: Retries (mixed outcomes → Inconclusive), Repeat + Interval (Stable, AllZero heuristics), Parallelism. Read-only.

**Phase 3 – Compare read**

- **CompareRead**: Package-level `CompareRead(ctx, req)` runs the same read for each rack/slot in `Candidates` (new connection per candidate). Returns `CompareReadResult` with `ByCandidate` (one `ReadResult` per candidate) and `RackSlotInsensitive` (true when all succeeded with identical data).
- **RackSlot** type for candidate list.

## Breaking changes

- **Read methods**: All read methods now return `(*ReadResult, error)` instead of `([]byte, error)`. Callers must use `result, err := c.ReadDB(...)`; check `err` for connection failure; then check `result.OK()` or `result.Err()` and use `result.Data` for the payload.

---
## v0.3.1

**Date:** 2026-03-13
**Previous release:** v0.2.1

## Summary

- **Strict rack/slot probe mode**: `Strict: true` on `RackSlotProbeRequest` restricts "valid" to candidates that complete both S7 setup and a benign follow-up query (`valid-query`). Without strict, any setup success (`setup-only`, `valid-connect`, or `valid-query`) is valid.
- **Confirmation strategies**: When strict, follow-up is configurable via `Confirm`: `szl` (SZL module ID), `cpu-state` (SZL CPU state), or `any` (try SZL, then CPU state, then protection). Default when `Strict` is true is `ConfirmAny`.
- **New probe types**: `ProbeStage` (tcp, cotp, setup, query), `ProbeStatus` (unreachable, tcp-only, cotp-only, setup-only, valid-connect, valid-query, rejected, timeout, flaky), `ConfirmationKind`, and `Confidence`.
- **Extended `RackSlotCandidate`**: Each candidate now reports `Stage`, `Status`, `ConfirmedBy`, `Confidence`, and explicitly **`S7SetupOK`** (setup succeeded) and **`SZLQueryOK`** (follow-up query succeeded). Redundant legacy fields `ReachableTCP`, `ReachableCOTP`, `Classification` and legacy `Class*` constants were removed; use `Status` and the new fields instead.
- **Result summary**: `RackSlotProbeResult` includes `SetupAccepted`, `ConfirmedByQuery`, `TCPOnly`, and `Flaky`. In strict mode only `valid-query` candidates appear in `Valid`.
- **Breaking**: Code that relied on `Class*` constants or on `ReachableTCP`/`ReachableCOTP`/`Classification` must switch to `Status`, `S7SetupOK`, and `SZLQueryOK`.

---
## v0.2.1

**Date:** 2026-03-13
**Previous release:** v0.2.0

## Summary

- Fix wrong package namespace.
- Fix linting errors.

---

## v0.2.0

**Date:** 2026-03-13
**Previous release:** v0.1.0

## Summary

- Added host-oriented rack/slot probe API (`ProbeRackSlots`) as a first-class public function in the `client` package.
- New types: `RackSlotProbeRequest`, `RackSlotCandidate`, `RackSlotProbeResult` covering the full connection classification model from TCP reachability through S7 setup success.
- Each candidate is classified as `valid-query`, `valid-connect`, `cotp-failed`, `tcp-only`, `unreachable`, or `rejected`, giving operators precise visibility into which protocol stage succeeded or failed.
- Probe supports bounded parallelism, configurable rack/slot ranges, per-attempt delays, stop-on-first mode, and optional manual TSAP override.
- An optional benign SZL query (SZL 0x0011) is attempted on successful candidates to elevate confidence from `valid-connect` to `valid-query`.
- All probe logic is non-destructive: only connection and setup traffic, plus a read-only SZL request where possible.
- Exit semantics and JSON/table output are documented for `s7commctl probe rackslot` CLI consumers.

---

## v0.1.0

**Date:** 2026-03-13
**Previous release:** v0.0.0

## Summary

- Initial public release of the S7 communication library for Go, with a modular package layout: `client`, `model`, `transport`, and `wire`.
- Added end-to-end S7 client flows for connect/setup, memory read/write operations, device discovery, SZL-based identification/diagnostics, and block upload/listing support.
- Included low-level protocol encoding/decoding for TPKT, COTP, S7 headers, and key PDU message families.
- Expanded and hardened tests around protocol parsing and edge cases, including connection lifecycle cleanup, discovery boundary handling, enum/string behavior, and model encoding safety.
- Fixed reliability issues discovered during review:
	- connection references are now cleared after close/failed handshake paths;
	- defensive bounds handling for string encoding;
	- safe negative-index handling in boolean decoding.
- Added contributor tooling and automation:
	- self-documented Makefile targets for test, coverage, lint, vet, and formatting;
	- GitHub Actions CI workflow (test/lint/coverage);
	- manual release workflow with semantic bumping and GitHub Release creation.

## Known limitations

- Integration coverage is currently strongest for protocol/unit tests; broader multi-model PLC interoperability validation remains in progress.
- Public API documentation is still concise and will be expanded with additional practical recipes (error handling, retry/rate-limit tuning, and block/SZL workflows).
- Transport and wire package coverage can be increased further with more fixture-based table tests for malformed and vendor-variant frames.

---
