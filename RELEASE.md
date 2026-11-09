# go-s7comm Releases

## v0.15.0

**Date:** 2026-11-09
**Previous release:** v0.14.0

### Summary

`CompareRead` no longer hangs on a peer that goes silent after setup: its `Timeout` now bounds each candidate's read as well as its connect. Also chaos coverage for the scan helpers' dialer added in v0.14.0, more unit tests for it, and documentation catch-up.

### Breaking changes

- `client`: `CompareRead` now times out a candidate whose read takes longer than `CompareReadRequest.Timeout` (default 5s). Before, a slow peer that answered within the caller's `ctx` deadline but after `Timeout` succeeded; it now reports `ReadStatusTimeout`. Raise `Timeout` for such peers.

### Fixed

- `client`: `CompareRead` bounds each candidate's read by `CompareReadRequest.Timeout` (default 5s), as it already did its connect. Before, the read ran on the caller's `ctx` alone, so a peer that accepted setup and then went silent blocked a caller without a deadline indefinitely. A read that runs out of time now reports `ReadStatusTimeout`, wrapping `context.DeadlineExceeded`.

### Tests

- `e2e/scan_dialer_chaos_test.go`: `Discover`, `ProbeRackSlots` (strict) and `CompareRead` through a dialer that redirects an unroutable address to the simulated PLC and gives every connection of a call one seeded transport fault (one-byte reads, a cut after n incoming or outgoing bytes, a flipped byte, a silent peer), 60 calls each, `CompareRead` with no deadline on `ctx`. Each returns within its timeouts, a fragmenting transport finds, confirms and reads, no probe candidate is classed at the TCP stage, a successful read returns the PLC's memory unless a byte was flipped, and every other fault makes some call fail.
- `client/compare_test.go`: `CompareRead` against a peer that accepts setup and never answers the read returns read timeouts within `Timeout` (it hung before the fix).
- `client/scan_dialer_test.go`: a probe dialer returning no connection, or a connection with an error (closed); parent-context cancel during a `Discover` and a `CompareRead` dial.

### Documentation

- README.md, ARCHITECTURE.md, TESTING.md and the package doc describe the scan helpers' dialers.
- API.md, the `CompareRead` doc comment and the `CompareReadRequest.Timeout` field comment: `Timeout` bounds each candidate's connect and its read.
- docs/FEATURE-STATUS.md: status v0.15.0, version rows for v0.13.x, v0.14.x and v0.15.x.

---

## v0.14.0

**Date:** 2026-11-09
**Previous release:** v0.13.0

### Summary

The scan helpers take a `client.Dialer` too, so an allowlist or proxy covers every connection the library opens, not only `Connect`. The defaults are unchanged.

### Added

- `client`: `WithDiscoverDialer` for `Discover`, and a `Dialer` field on `RackSlotProbeRequest` (`ProbeRackSlots`) and `CompareReadRequest` (`CompareRead`). Each attempt's dial goes through it with network `"tcp"`, `host:port` and a context bounded by the helper's timeout; it is called concurrently when the helper runs in parallel. A nil dialer uses `net.Dialer` as before. A dial error is wrapped as `TCP connect: …`: in `RackSlotCandidate.Error` (stage `tcp`, status `unreachable`, or `timeout` on a deadline) and in `ReadResult.Cause` for `CompareRead`; `Discover` counts it as a failed rack/slot attempt. The new fields are tagged `json:"-"`, so results that echo the request still marshal.

### Breaking changes

- `client`: `RackSlotProbeRequest` is no longer comparable with `==`, because it now holds a func field. Code that compares two requests with `==`, or uses one as a map key, no longer compiles; compare the fields it needs instead. `CompareReadRequest` already held a slice and was not comparable before.

### Tests

- `client/scan_dialer_test.go`: for `Discover`, `ProbeRackSlots` and `CompareRead`, a dialer that redirects to a fake server (the requested `host:port`, a deadline on its context, success through it); a failing dialer (call count per attempt, wrapped error, probe status); a probe timeout through the dialer; a nil discover dialer; JSON marshalling of requests holding a dialer. goleak stays green.

### Documentation

- SECURITY.md "Restricting Destinations", API.md, LIMITATIONS.md, docs/FEATURE-STATUS.md and the `WithDialer` doc comment: the scan helpers no longer dial around the dialer.

### Interoperability

No wire change: the interop matrix runs unchanged with the default dialer.

---

## v0.13.0

**Date:** 2026-11-09
**Previous release:** v0.12.0

### Summary

`client.WithDialer` lets the caller supply the function that opens the TCP connection of `Connect`: an address allowlist that checks the resolved IP right before connecting (no DNS-rebinding gap), a SOCKS or HTTP proxy, or an in-memory transport in tests. The default is unchanged. Contributed by @umpfel ([#27](https://github.com/otfabric/go-s7comm/pull/27)).

### Added

- `client`: `WithDialer` and the `Dialer` type, `func(ctx, network, addr string) (net.Conn, error)`. The dialer gets network `"tcp"`, `host:port` and a context carrying the `WithTimeout` deadline; its error comes back wrapped as `TCP connect: …`, so `errors.Is` / `errors.As` reach it. A dialer returning no connection and no error fails `Connect`; a connection returned together with an error is closed. `Discover`, `ProbeRackSlots` and `CompareRead` keep dialing directly ([LIMITATIONS.md](LIMITATIONS.md#scope-not-implemented)).

### Changed

- `client`: with `WithAutoRackSlot`, the error returned when every rack/slot pair fails now wraps the last attempt's error (`auto rack/slot connect failed …: <cause>`). The message keeps its prefix; code matching the whole string must allow the suffix.

### Tests

- `client/dialer_test.go`: the dialer's network, address and deadline; wrapped errors; no connection; connection with an error closed; `WithAutoRackSlot` (5 common and 24 brute attempts, and success on the first pair); `WithTSAP`; parent-context cancel; timeout expiry; nil dialer.
- `e2e/dialer_chaos_test.go`: through `WithDialer`, 120 seeded reconnects each get one transport fault in the TCP, COTP and S7 setup exchange (one-byte reads, a cut after n incoming or outgoing bytes, a flipped byte, a silent peer). Each returns within its timeout, a fragmenting transport connects, and a failed reconnect leaves the established session working.

### Documentation

- SECURITY.md: enforce allowlists in the dialer; scans and probes do not use it. LIMITATIONS.md, README.md, API.md, ARCHITECTURE.md, ERRORS.md, TESTING.md and docs/FEATURE-STATUS.md describe the dialer.

### Interoperability

No wire change: the interop matrix runs unchanged with the default dialer.

---

## v0.12.0

**Date:** 2026-11-07
**Previous release:** v0.11.0

### Summary

Package `server` now answers every Setup Communication, Read Var and Write Var job recorded from real S7-300 and S7-400 CPUs ([testdata/captures](testdata/captures/README.md)) byte for byte as the CPU did: 26 setups, 3,411 reads and 2,081 writes. The three differences v0.11.0 pinned are gone: parallel jobs, timers and counters, and the S7-400's DBREAD items.

### Added

- `server`: `AreaTimers` and `AreaCounters`, two bytes per element (element n at byte 2n for the host-side `Read` and `Write`). Clients read them with transport size TIMER `0x1D` / COUNTER `0x1C`, addressed by element number, as OCTET STRING; their writes are refused with `0x03`, as a recorded CPU 315-2 PN/DP refuses them. A TIMER or COUNTER transport size outside its area, or another transport size on timers or counters, gets `0x05`, as Snap7's server checks it.
- `server`: DBREAD items (syntax `0xB0`), which WinCC sends to S7-400 CPUs: read only, one OCTET STRING item holding per range `0xFF` and the bytes, without padding, as the recorded S7-400 answers. A range of a DB not held refuses the whole item with `0x0A`, past the end or of no bytes with `0x05`; the recordings hold no refused range, so that case is this library's choice. In a Write Var job a DBREAD item gets `0x05`.
- `wire`: `ParseReadVarDBRead` and `DBReadArea`; `ParseReadVarRequest` now refuses a malformed DBREAD item (`ErrMalformedRequest`).

### Changed

- `server`: Setup Communication grants up to two parallel jobs (AmQ calling and called), as the recorded CPUs do (WinCC asking 1/3 gets 1/2, an S7-400 asked 2/2 grants 2/2), instead of one. Jobs are still answered in the order they arrive.
- `server`: a TIMER or COUNTER transport size on a DB, inputs, outputs or markers now gets `0x05` instead of `0x06`.

### Tests

- `server/capture_test.go` requires every recorded answer byte for byte. New: DBREAD and timer/counter unit tests, a loopback test with two pipelined jobs over TCP and COTP, a seeded chaos test (20,000 mutated real-client jobs), DBREAD and timer seeds for `FuzzHandle`.
- `wire`: WinCC's 84 DBREAD requests to the S7-400 parse, and every answer holds per range the return code and the requested bytes; frames 74, 1209 and 24 (one, two and three ranges) are byte-exact fixtures and seeds of `FuzzParseReadVarDBRead`.

### Interoperability

DBREAD and timers/counters have no interop peer: neither Snap7 nor python-snap7 implements DBREAD, and snap7-interop's fixtures have no timer or counter areas; Snap7's server maps timer addresses differently (`Start >> 1`) and accepts the writes a real CPU refuses. The recorded traffic is their evidence.

---

## v0.11.0

**Date:** 2026-11-07
**Previous release:** v0.10.2

### Summary

go-s7comm is now tested against 25 recorded captures of real S7-300 and S7-400 CPUs (14,393 S7 PDUs; [testdata/captures](testdata/captures/README.md)). They showed that the client's user data, block and upload requests did not match the protocol real clients and CPUs use, so `Identify`, `GetCPUState`, `GetProtectionLevel`, `ReadDiagBuffer`, `ListBlocks`, `GetBlockInfo` and `UploadBlock` could not work against a real CPU. All are fixed: the requests are now byte for byte those of Snap7 and TIA Portal V13, and the client decodes the recorded answers of a CPU 315-2 PN/DP and an IM151-8 PN/DP CPU to the values Snap7 logged and Wireshark decodes. The server answers every recorded Setup Communication, Read Var and Write Var job byte for byte as the CPUs did, apart from three pinned differences.

### Fixed

- SZL reads (`Identify`, `GetCPUState`, `GetProtectionLevel`, `ReadDiagBuffer*`, and the rack/slot probe's follow-up): the user data parameter lacked the method byte (`00 01 12 04 44 01 00 00` instead of `00 01 12 04 11 44 01 00`), so a CPU read it as a request of another function group. An answer in several packets ("more data follows") is now collected with follow-up requests.
- `Identify` read fixed offsets that ignored the SZL list header; it now reads SZL 0x0011 and 0x001C records by index: order number, firmware (`Vx.y.z` from index 7), and the module, system, plant, copyright, serial, module type and memory card texts.
- `GetCPUState` read the wrong byte; it now reads the mode of SZL 0x0424's record (1–4 STOP, 5–7 STARTUP, 8–9 RUN, 10 HOLD).
- `GetProtectionLevel` asked for SZL 0x0232 index 0 and read the wrong byte; it now asks for index 4 and reads `sch_rel` (Siemens level 1 none, 2 write protected, 3 read/write protected).
- `ReadDiagBuffer` stepped through the list header as if it were records; it now reads the 20-byte records (event ID, class, priority, info, time stamp).
- `ListBlocks` asked for SZL 0x0111; it now uses the block functions (list blocks of a type), collecting every packet. Entries are block number, flags and language.
- `GetBlockInfo` asked for SZL 0x0113; it now uses the block functions (block info) and decodes the block header: language, flags, load memory, local data, MC7 and SSB length, author, family, name, version and checksum. Block numbers 0..99999.
- `UploadBlock`: the file name was `_<type><number>A\0` instead of `_0<type><number>A`; the upload ID is the CPU's 4-byte number, not a text; the segment length is in bytes in the first two data bytes.
- `wire.ParseWriteVarResponse` checked only the first item, so an answer refusing a later item (a real CPU's `FF FF FF 03 03`) passed as success. It now fails on the first refused item.
- `wire.ParseSZLResponse` reported a refusal of 4 bytes as "too short" instead of its return code.

### Added

- `Client.BlockCounts`: the number of blocks of each type.
- `model.DeviceInfo.SystemName` and `MemoryCardSerial`.
- `wire`: `EncodeUserdataRequest`, `EncodeUserdataFollowUp`, `ParseUserdataParam` (`UserdataParam`), `ParseUserdataData`, the `UserdataGroup*`, `UserdataBlock*` and `UserdataType*` constants, `EncodeBlockCountRequest`, `ParseBlockCountResponse` (`BlockCount`), `EncodeBlockInfoRequest`, `ParseWriteVarResponseCodes`, `StartUpload`.
- `testdata/captures`: 25 captures from [gymgit/s7-pcaps](https://github.com/gymgit/s7-pcaps) and the Wireshark wiki, with sources, devices, clients and SHA256 checksums; `internal/capture` reads them. Tests: every PDU decodes; encoders equal recorded requests; the client against recorded answers; the server against the recorded CPUs' answers.

### Changed (breaking, `wire`)

- `ParseStartUploadResponse` returns `StartUpload` (upload ID and announced length) instead of a string; `EncodeUploadRequest` and `EncodeEndUploadRequest` take the `uint32` upload ID.
- `ParseBlockListResponse` and `ParseBlockInfoResponse` take the block functions payload (`ParseUserdataData`); `BlockInfoData` has the fields of the block header.
- `EncodeBlockListRequest` encodes the block functions request instead of an SZL read.
- `ReadDiagBufferRaw` returns the SZL list data including its header (record length and count), all packets of it.

### Documentation

- testdata/captures/README.md; TESTING.md, INTEROPERABILITY.md (recorded traffic), LIMITATIONS.md, ERRORS.md (a real CPU's `0x03`), API.md, docs/FEATURE-STATUS.md, ARCHITECTURE.md, README.md.

### Known differences of the server (follow-up)

The server grants one parallel job (AmQ) where the recorded CPUs grant two, holds no timer or counter areas, and refuses the S7-400's DBREAD (syntax `0xB0`) items. `server/capture_test.go` pins them.

---

## v0.10.2

**Date:** 2026-11-07
**Previous release:** v0.10.1

### Changed

- The interop suite pins the native and python reference images of snap7-interop v0.6.1 (container contract 1.1) instead of v0.3.0 (contract 1.0): `ghcr.io/otfabric/snap7-interop-native@sha256:b2ac7b3d…`, `ghcr.io/otfabric/snap7-interop-python@sha256:7b2d7691…`, in the Makefile, `interop/harness.go` and `interop.yml`. The upstream pins are unchanged (Snap7 SCADACS `f6ff903`, python-snap7 3.2.1), and so are the vendored compiled fixtures, which are byte-identical in both versions. Contract 1.1 documents the existing environment, ports and lifecycle and adds the otfabric image; nothing a consumer sets changed.

### Documentation

- INTEROPERABILITY.md, LIMITATIONS.md, README.md, `interop/testdata/README.md` and docs/FEATURE-STATUS.md (version table now lists v0.10.x) name snap7-interop v0.6.1.

No library code change.

---

## v0.10.1

**Date:** 2026-11-06
**Previous release:** v0.10.0

### Documentation

- ERRORS.md, *Refused access*: item codes `0x03`, `0x05` and `0x0A` with the Siemens meanings (STEP 7 V20, *Use of FETCH/WRITE*), next to header-level refusals of the whole job (`0x81/0x04`, `0x85`, and `0x83/0x04` as an S7-300 sends it), and what the client returns for each. Snap7-based servers send `0x0A` for a refused write, so `0x0A` alone does not mean the DB is missing.
- The server's `0x03` for a write to a read-only area is documented as a decision, with the Siemens reference, in README.md, API.md, LIMITATIONS.md, INTEROPERABILITY.md, TESTING.md, docs/FEATURE-STATUS.md and the `server` package doc. snap7-interop v0.6.1 classifies it as an intentional difference. What a physical CPU sends is not verified yet (#18).

No code change.

---

## v0.10.0

**Date:** 2026-11-06
**Previous release:** v0.9.0

### Fixed

- Read Var answers with data transport size DINTEGER (`0x06`), REAL (`0x07`) or OCTET STRING (`0x09`) were decoded with their length in bits, so a 4-byte REAL came back as 1 byte. `wire.NormalizeResponseDataLength` now follows the Siemens units as Wireshark's s7comm dissector reads them: bits for BYTE/WORD/DWORD (`0x04`) and INTEGER (`0x05`), bytes for `0x06`, `0x07` and `0x09`, one byte for BIT (`0x03`). The client's own reads use only BYTE and BIT and are not affected.

### Changed

- `wire.NormalizeResponseDataLength` refuses transport sizes that are not data transport sizes (`0x01`, `0x02`, `0x08`, `0x0A`..`0x0F`, `0x1C`, `0x1D`, 30..32). They were request transport sizes and S7-200 types copied into the response table; an answer carrying one is now a protocol error instead of being read with a guessed length.
- `ResponseTransportSize.String()` gives the data transport size names (`NULL`, `BIT`, `BYTE/WORD/DWORD`, `INTEGER`, `DINTEGER`, `REAL`, `OCTET STRING`); other values print in hex. The `RespTransportSize*` constants are deprecated (values unchanged) in favour of `DataTransportSize*`.

### Added

- Package `server`: an in-memory S7 server for tests. It holds data blocks, inputs, outputs and markers (`RegisterDB`, `RegisterArea`, optionally read-only) and answers Setup Communication, Read Var and Write Var on ISO-on-TCP through go-cotp, with the item return codes, header errors and data transport sizes Siemens defines (as Wireshark's s7comm dissector lists them). `Serve`, `ListenAndServe`, `Close`, host-side `Read` and `Write`, `Config` with PDU size, connection limit, handshake and idle timeouts and an optional `*slog.Logger`. Other jobs and user data get error `0x8104`; an answer past the PDU gets `0x8500`; a PDU whose header cannot be read closes the connection.
- `wire` server-side codec: `ParseSetupCommRequest`, `EncodeSetupCommResponse`, `ParseReadVarRequest`, `ParseWriteVarRequest`, `EncodeReadVarResponse`, `ReadVarResultSize`, `EncodeWriteVarResponse`, `EncodeAckData`, types `VarSpec`, `WriteVarData`, `ReadVarResult`, `ErrMalformedRequest`, `AckDataHeaderSize` and the data transport sizes `DataTransportSizeByte`, `DataTransportSizeInt`, `DataTransportSizeDInt`, `DataTransportSizeReal`, `DataTransportSizeOctet`.
- Tests: the client against the server on loopback, item codes from hand-built PDUs, hostile sockets, goroutine leak checks. New fuzz targets: `FuzzHandle`, `FuzzParseReadVarRequest`, `FuzzParseWriteVarRequest`, `FuzzParseSetupCommRequest`.

---

## v0.9.0

**Date:** 2026-11-03
**Previous release:** v0.8.1

### Breaking changes

- Go 1.26 minimum (was 1.23). CI tests Go 1.26 and 1.27.
- `wire` request encoders return `([]byte, error)`: `EncodeS7Header`, `EncodeS7Any`, `EncodeS7AnyBit`, `EncodeReadVarRequest`, `EncodeReadVarBitRequest`, `EncodeWriteVarRequest`, `EncodeWriteVarBitRequest`, `EncodeSetupCommRequest`. A value that does not fit its wire field returns the new `wire.ErrOutOfRange`; before, it was truncated without notice.
- The client refuses, with `*ValidationError`, addresses it used to truncate: a DB number outside 0..65535 (for every area, not only DB), a start or bit byte offset past 2097151 (`wire.MaxS7AnyByteOffset`, the last byte a 24-bit S7ANY bit address reaches), a read or write that runs past that byte, a max PDU above 65535 and a port above 65535.

### Fixed

- A DB number above 65535 or an offset past the 24-bit S7ANY start address wrapped to a different address, so `WriteDB(65537, …)` wrote DB 1. Both are now refused.
- `ReadArea` and its helpers accepted a Read Var item that carried more bytes than requested. The extra bytes shifted every later chunk to the wrong offset in `Data`. Such an answer is now `protocol-error`.
- `ProbeRackSlots` and `Discover` panicked when `JitterMS` was `math.MaxInt`. Jitter now comes from `crypto/rand` and is capped at one hour.
- `wire.EncodeStartUploadRequest` built a garbage file name for a negative block number or one above 99999. It now returns `ErrOutOfRange`.
- `wire.BuildTSAP` wrapped a connection type above 255. It now returns an error.
- gosec G115 and G404: all 35 findings fixed in code, with no suppressions.

### Added

- `wire.ErrOutOfRange`, `wire.ValidateS7AnyAddress`, `wire.ValidateS7AnyBitAddress` and the limits `MaxS7AnyDBNumber`, `MaxS7AnyByteOffset`, `MaxS7AnyCount`, `MaxReadVarItems`, `MaxWriteVarBytes` and `MaxUploadBlockNumber`.
- Tests: a hostile-PLC chaos test (`e2e/hostile_test.go`) and boundary tests for every encoder. New fuzz targets: `FuzzEncodeS7Any`, `FuzzEncodeWriteVarRequest`, `FuzzParseSetupCommResponse`, `FuzzParseWriteVarResponse`, `FuzzParseStartUploadResponse`, `FuzzParseBlockResponses`, `FuzzSignedRoundTrip` and `FuzzEncodeString`.

### Changed

- Built on go-cotp v1.2.0 and go-tpkt v1.1.0. Code modernised with `go fix`.

---

## v0.8.1

**Date:** 2026-10-31
**Previous release:** v0.8.0

### Fixed

- `ProbeRackSlots` with `Strict` and no `Confirm` never confirmed a slot, so nothing was valid. It now defaults to `ConfirmAny`.
- Built on go-cotp v1.1.1, go-tpkt v1.0.2.

---

## v0.8.0

**Date:** 2026-10-30
**Previous release:** v0.7.5

### Breaking changes

- `wire.EncodeStartUploadRequest` now returns `([]byte, error)`. An unknown block type returns the new `wire.ErrUnknownBlockType` instead of requesting a DB, and `UploadBlock` returns `*ValidationError` for it without sending a request.

### Fixed

- `Identify` panicked on a 20- or 21-byte module ID SZL payload; such a payload is now reported as too short.

- `WithTimeout` and `RackSlotProbeRequest.Timeout` did not bound the S7 setup or the strict probe follow-up, so a peer that accepted COTP and then went silent blocked `Connect`, `Discover` and `ProbeRackSlots` until the caller's context ended. Such a probe now reports `timeout`.
- `UploadBlock` with `BlockSDB` requested the DB of the same number; it now requests the SDB. No independent peer has exercised this.
- The PDU reference counter could yield 0 after wrapping, which disabled response correlation for that request. 0 is now skipped.

### Dependencies

- `go-cotp` v1.0.4 → v1.1.0.

---

## v0.7.5

**Date:** 2026-07-30
**Previous release:** v0.7.4

## Summary

Patch release: bump go-cotp to v1.0.4 (pulls go-tpkt v1.0.1). No S7 protocol
or result-model changes.

## Changes

### Dependencies

- **go-cotp** — `v1.0.3` → **v1.0.4**
- **go-tpkt** (indirect) — `v1.0.0` → **v1.0.1**

### Unchanged

- No API, wire, or behavioural changes. Import path remains `github.com/otfabric/go-s7comm`.

---

## v0.7.4

**Date:** 2026-07-30
**Previous release:** v0.7.3

## Summary

Patch release: document logger and accurate context-cancel behaviour, add a thin
`slog` adapter, bump go-cotp to v1.0.3, root/`model` package docs, and raise
client test coverage. No S7 protocol or result-model changes.

## Changes

### Added

- **`client.NewSlogLogger`** — adapts `slog.Handler` to the existing printf-style `Logger` (`nil` handler → no-op).
- **OBSERVABILITY.md** — silent-by-default logging, sync callback cost, slog adapter; no metrics surface.
- **`doc.go`** (module root, `package s7comm`) — points pkg.go.dev / importers at `client`, `model`, and `wire`.
- **`model/doc.go`** — package overview for areas, addresses, device/connection info, and codec wrappers.

### Changed

- **Context docs** (README, ERRORS.md, API.md) — cancel without deadline is effective on dial/TSDU via go-cotp but typically aborts the session; `WithTimeout` bounds Connect, not later PDUs; document UploadBlock cleanup context.
- **`Logger` / `WithLogger` godoc** — nil default, printf-style args, synchronous request-path cost.
- **Connect logging** — `Error("Connect failed: %v", err)` so the error is formatted correctly.
- **README** — **Project structure** tree (packages + docs); replaces the late bullet-only package list.
- **README badges** — Added pkg.go.dev; normalized Go `1.23%2B` and Codecov link to `codecov.io/gh/...`.

### Dependencies

- **go-cotp** — `v1.0.2` → **v1.0.3** (docs-only upstream release).

### License & open-source hygiene

- **LICENSE** — Added root MIT license (`Copyright (c) 2026 OT Fabric`).
- **SPDX headers** — Added `// SPDX-License-Identifier: MIT` to all first-party `.go` files (build tags preserved on `interop/`).
- **README** — License section pointing at [LICENSE](./LICENSE).

### Build / tooling

- **Makefile** — exports `GOWORK=off`.
- **Tests** — `client` package coverage raised above 85% (ListBlocks/GetBlockInfo, probeS7, autoConnect, SZL edges, PDU-ref mismatch, range-probe mixed results).

### Unchanged

- No change to `ReadResult` / `ReadStatus`, wire codecs, or cancel implementation (docs corrected to match go-cotp behaviour).

---

## v0.7.3

**Date:** 2026-07-20
**Previous release:** v0.7.2

### Summary

- **Docs**: New [ERRORS.md](ERRORS.md) as the canonical guide for outcome models (`ReadResult` / `ReadStatus`), client sentinels (`ErrNotConnected`, `ValidationError`, …), wire `S7Error` / framing errors, detection patterns, and the CLI exit-code contract. [API.md](API.md) refreshed for v0.7.2+ (bit API, `UnsupportedSyntaxError`, wire sentinels) and points to ERRORS.md for semantics. [README.md](README.md) links API / ERRORS / RELEASE and keeps a short error summary.
- **Dependencies**: Upgrade [go-cotp](https://github.com/otfabric/go-cotp) from `v1.0.0-rc.1` to **v1.0.2** (stable). No public API changes in go-s7comm.

### Breaking changes

- None (docs + dependency bump).

---

## v0.7.2

**Date:** 2026-07-18
**Previous release:** v0.7.1

### Summary

- **wire**: Harden Read Var parsing for non-success items that declare a meaningful nonzero transport and payload. Snap7-style rejects with transport size `0x00` remain header-only (rawLength is preserved but not interpreted as payload length), so address-out-of-range replies keep surfacing as item return codes. Rejected items with a nonzero known/unknown transport and nonzero length strictly validate/skip the declared payload so multi-item responses stay aligned.

### Breaking changes

- None. Snap7 out-of-range / transport-`0x00` rejection handling is preserved; only rejected items with a meaningful declared payload become stricter.

---

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
- **CI and tooling**: Single workflow `test.yml` replaces ci.yml and release.yml. Test job runs on ubuntu and Windows with Go 1.25.x (vet, test, mod verify); coverage job uploads to Codecov and artifacts; lint job runs staticcheck and golangci-lint. Releases are done manually. README badges aligned (Go, License, Go Report Card, CI, Codecov, Release). `.golangci.yml` updated to config version `"2"` for golangci-lint v2 compatibility.

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
