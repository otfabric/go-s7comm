# Feature status

What go-s7comm implements and how far each part has been verified. Status: v0.11.0.

None of this is a conformance claim: nothing is certified, no Siemens test has been run, and this repository holds no test against a physical PLC ([LIMITATIONS.md](../LIMITATIONS.md)).

## Versions

| go-s7comm | Go | snap7-interop | Supported |
|---|---|---|---|
| v0.11.x | 1.26 or later (CI: 1.26 and 1.27) | v0.6.1 (contract 1.1) | latest release ([SECURITY.md](../SECURITY.md)) |
| v0.10.x | 1.26 or later (CI: 1.26 and 1.27) | v0.6.1 (contract 1.1); v0.10.0 and v0.10.1 tested against v0.3.0 (contract 1.0) | no |
| v0.9.x | 1.26 or later (CI: 1.26 and 1.27) | v0.3.0 (contract 1.0) | no |

## Platforms

| Platform | Tests | Notes |
|---|---|---|
| Linux amd64 | unit, fuzz and interop in CI | |
| Linux arm64 | interop run locally, not in CI | snap7-interop images exist for this platform |
| Linux arm, macOS arm64, Windows amd64 | `make cross` builds without cgo; not in CI | |

The module is pure Go; go-cotp and go-tpkt are its only dependencies, apart from goleak in the tests.

## Feature support

✅ supported · ◐ partly (see the notes) · — not implemented. **Fake server**: unit tests in `client/` exercise it against the in-package fake server. **Independent**: exercised against both Snap7 servers in the interop matrix, or against the recorded answers of real CPUs in [testdata/captures](../testdata/captures/README.md) (*recorded*) ([INTEROPERABILITY.md](../INTEROPERABILITY.md)).

| Feature | API | Status | Fake server | Independent |
|---|---|---|---|---|
| Session setup (TCP, COTP TP0, S7 setup) | `Connect`, `Close`, `WithRackSlot`, `WithTSAP` | ✅ | yes | yes (rack/slot options) |
| Reconnect | repeated `Connect`, new client | ✅ | failed attempt only | new client after `Close` (`reconnect-state`) |
| Byte read, DB | `ReadDB`, `ReadArea` | ✅ | yes | yes |
| Byte read, markers, inputs, outputs | `ReadMerkers`, `ReadInputs`, `ReadOutputs`, `ReadArea` | ✅ | yes | yes |
| Byte write, DB, markers | `WriteDB`, `WriteArea` | ✅ | yes | yes |
| Bit read and write | `ReadBit`, `WriteBit`, `ReadDBBit`, `WriteDBBit` | ✅ | yes | yes, on DBs (`ReadBit`, `WriteBit`) |
| Rejected read and write | `ReadResult.Status`, `S7Error`, `ParseWriteVarResponse` | ✅ | yes | yes, item codes `0x05` and `0x0A`; recorded: `0x03` and `0x0A` of a CPU 315-2 PN/DP ([ERRORS.md](../ERRORS.md#refused-access-item-codes-and-header-errors)) |
| Multi-PDU read and write chunking | `ReadArea`, `WriteArea` | ✅ | chunk planning unit test only | no |
| Timer and counter areas | `model.AreaTimer`, `model.AreaCounter` | ◐ area codes defined; no client test uses them | no | no |
| CPU identification | `Identify` | ✅ | yes | recorded: CPU 315-2 PN/DP, IM151-8 PN/DP CPU |
| CPU state, protection level | `GetCPUState`, `GetProtectionLevel` | ✅ | yes | recorded: CPU 315-2 PN/DP, IM151-8 PN/DP CPU (state) |
| Diagnostic buffer | `ReadDiagBuffer`, `ReadDiagBufferRaw` | ✅ | yes | recorded: CPU 315-2 PN/DP, IM151-8 PN/DP CPU |
| Block counts, list and info | `BlockCounts`, `ListBlocks`, `ListAllBlocks`, `GetBlockInfo` | ✅ | yes | recorded: CPU 315-2 PN/DP, IM151-8 PN/DP CPU (counts, lists) |
| Block upload | `UploadBlock` | ✅ | yes | recorded: CPU 315-2 PN/DP (SDB 0) |
| Readable range scan | `ProbeReadableRanges` | ✅ | yes | no |
| Compare read | `CompareRead` | ✅ | yes | no |
| Rack/slot probe | `ProbeRackSlots` | ✅ | yes | no |
| Discovery | `Discover` | ◐ IPv4 only, up to `/12` | validation, CIDR handling and cancellation only | no |
| Block download, PLC start and stop | — | — | n/a | n/a |
| Test server: setup, Read Var, Write Var on DB, I, Q, M | `server.New`, `RegisterDB`, `RegisterArea`, `Serve` | ◐ for tests; no SZL, blocks, timers, counters, DBREAD; AmQ 1 | this module's client (`server/`); recorded jobs of real CPUs, byte for byte | snap7-interop's probe, all 15 fixtures in its Own stack workflow, the read-only code `0x03` an intentional difference from the Snap7 references' `0x0A` ([LIMITATIONS.md](../LIMITATIONS.md#verification)) |
| TLS, password handling | — | — | n/a | n/a |

Wire decoders in `wire/` have fuzz targets; see [TESTING.md](../TESTING.md#fuzzing).
