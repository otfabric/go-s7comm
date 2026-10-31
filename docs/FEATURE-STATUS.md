# Feature status

What go-s7comm implements and how far each part has been verified. Status: `main` after v0.7.5.

None of this is a conformance claim: nothing is certified, no Siemens test has been run, and this repository holds no test against a physical PLC ([LIMITATIONS.md](../LIMITATIONS.md)).

## Versions

| go-s7comm | Go | snap7-interop | Supported |
|---|---|---|---|
| v0.7.x | 1.23 or later (CI: 1.23 to 1.26) | v0.3.0 (contract 1.0) | latest release ([SECURITY.md](../SECURITY.md)) |

## Platforms

| Platform | Tests | Notes |
|---|---|---|
| Linux amd64 | unit, fuzz and interop in CI | |
| Linux arm64 | interop run locally, not in CI | snap7-interop images exist for this platform |
| Linux arm, macOS arm64, Windows amd64 | `make cross` builds without cgo; not in CI | |

The module is pure Go; go-cotp and go-tpkt are its only dependencies, apart from goleak in the tests.

## Feature support

✅ supported · ◐ partly (see the notes) · — not implemented. **Fake server**: unit tests in `client/` exercise it against the in-package fake server. **Independent**: exercised against both Snap7 servers in the interop matrix ([INTEROPERABILITY.md](../INTEROPERABILITY.md)).

| Feature | API | Status | Fake server | Independent |
|---|---|---|---|---|
| Session setup (TCP, COTP TP0, S7 setup) | `Connect`, `Close`, `WithRackSlot`, `WithTSAP` | ✅ | yes | yes (rack/slot options) |
| Reconnect | repeated `Connect`, new client | ✅ | failed attempt only | new client after `Close` (`reconnect-state`) |
| Byte read, DB | `ReadDB`, `ReadArea` | ✅ | yes | yes |
| Byte read, markers, inputs, outputs | `ReadMerkers`, `ReadInputs`, `ReadOutputs`, `ReadArea` | ✅ | yes | yes |
| Byte write, DB, markers | `WriteDB`, `WriteArea` | ✅ | yes | yes |
| Bit read and write | `ReadBit`, `WriteBit`, `ReadDBBit`, `WriteDBBit` | ✅ | yes | yes, on DBs (`ReadBit`, `WriteBit`) |
| Rejected read and write | `ReadResult.Status`, `S7Error` | ✅ | yes | yes, item codes `0x05` and `0x0A` |
| Multi-PDU read and write chunking | `ReadArea`, `WriteArea` | ✅ | chunk planning unit test only | no |
| Timer and counter areas | `model.AreaTimer`, `model.AreaCounter` | ◐ area codes defined; no client test uses them | no | no |
| CPU identification | `Identify` | ✅ best-effort | yes | no |
| CPU state, protection level | `GetCPUState`, `GetProtectionLevel` | ✅ | yes | no |
| Diagnostic buffer | `ReadDiagBuffer`, `ReadDiagBufferRaw` | ✅ best-effort | yes | no |
| Block list and info | `ListBlocks`, `ListAllBlocks`, `GetBlockInfo` | ✅ | yes | no |
| Block upload | `UploadBlock` | ✅ | yes | no |
| Readable range scan | `ProbeReadableRanges` | ✅ | yes | no |
| Compare read | `CompareRead` | ✅ | yes | no |
| Rack/slot probe | `ProbeRackSlots` | ✅ | yes | no |
| Discovery | `Discover` | ◐ IPv4 only, up to `/12` | validation, CIDR handling and cancellation only | no |
| Block download, PLC start and stop | — | — | n/a | n/a |
| S7 server | — | — | n/a | n/a |
| TLS, password handling | — | — | n/a | n/a |

Wire decoders in `wire/` have fuzz targets; see [TESTING.md](../TESTING.md#fuzzing).
