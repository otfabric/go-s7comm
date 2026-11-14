# Interoperability

Evidence that go-s7comm works with S7 implementations it does not contain. Nothing here is a certification or a conformance claim, and both peers are server libraries, not Siemens PLCs. Feature-by-feature status: [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md).

## Peers

Both peers run from [snap7-interop](https://github.com/otfabric/snap7-interop) v0.6.1 (container contract 1.1, fixture schema 1.0.0), as multi-arch images pinned by digest in the [Makefile](Makefile) and [.github/workflows/interop.yml](.github/workflows/interop.yml).

| Peer | Implementation | Image digest |
|---|---|---|
| native | Snap7, SCADACS fork at `f6ff90317ca5d54250f4dcd29209689a74e26d82` | `ghcr.io/otfabric/snap7-interop-native@sha256:b2ac7b3d0d8cb1dc1ac5d7e465a118eb484a2506ea67b10be6ff7579ac5d5a97` |
| python | python-snap7 3.2.1, pure-Python `Server` API, no libsnap7 | `ghcr.io/otfabric/snap7-interop-python@sha256:7b2d7691e6f4ea95af6699025ad31946c542f237af543ccae686f6fe79b5cd5e` |

Upstream versions are from snap7-interop `versions.yaml` at v0.6.1.

## Results

`interop.yml` runs the suite on every pull request. The suite runs all 15 fixtures vendored in [interop/testdata/fixtures](interop/testdata/fixtures) against both peers, at rack 0, slot 2, PDU size 480. Each server is restarted for each fixture.

| Fixture | Exercises | native | python |
|---|---|---|---|
| `basic-db` | DB read | pass | pass |
| `bit-access` | `ReadBit`, `WriteBit` | pass | pass |
| `concurrent-reads` | several clients on separate sessions | pass | pass |
| `floating-point` | IEEE-754 values in a DB | pass | pass |
| `integer-boundaries` | signed and unsigned limits in a DB | pass | pass |
| `large-read` | large single read within the PDU | pass | pass |
| `last-valid-byte` | read at the end of a DB | pass | pass |
| `markers-io` | marker read and write, input and output read | pass | pass |
| `multiple-dbs` | two DBs, write and read | pass | pass |
| `out-of-range` | rejected read and write | pass | pass |
| `read-only-reject` | rejected write | pass | pass |
| `reconnect-state` | memory kept across reconnect | pass | pass |
| `typed-values` | DB and markers with typed initial values | pass | pass |
| `unaligned-range` | unaligned multi-byte reads | pass | pass |
| `write-cycle` | write, read back, final state | pass | pass |

Recorded: `Interop` workflow run for commit `436a28f` on `main`, and a local `make interop` on linux/arm64 images on 2026-10-30. Both passed.

## Differences that the fixtures normalise

- A rejected write to read-only memory is reported by both adapters as item code `0x0A`, normalised to `address-out-of-range`. Stock Snap7 has no code for refused access (`0x03`), so this suite does not exercise it against the peers; the Self-interop workflow does, against go-s7comm's own server.
- The python adapter changes stock python-snap7 behaviour for out-of-range access, read-only access and bit writes so that it matches native Snap7 (snap7-interop `docs/compatibility.md`). Agreement between the peers on these points is therefore by construction, not two independent observations.

## Recorded traffic of real CPUs

[testdata/captures](testdata/captures/README.md): 26 captures between a CPU 315-2 PN/DP (6ES7 315-2EH14-0AB0, V3.2.7), an IM151-8 PN/DP CPU (6ES7 151-8AB01-0AB0, V3.2.6), an S7-400, another S7-300 and a CPU 1214C DC/DC/DC (S7-1200, V4.2), and six clients (Snap7, STEP 7 5.5, TIA Portal V13, WinCC Advanced, libnodave, python-snap7). These are real devices, recorded by others, and replayed; no live PLC is involved ([TESTING.md](TESTING.md#recorded-traffic-of-real-cpus)).

| What | Result |
|---|---|
| Every S7 PDU (14,397) | decodes; counts per file equal Wireshark 4.6.9 |
| Read Var answers of the CPUs (9,068 items) | every successful S7ANY item has the requested length (BIT, BYTE, INT in bits; DINT, REAL, OCTET STRING in bytes) |
| DBREAD requests of WinCC to the S7-400 (84: 43 one-range, 1 two-range, 40 three-range) | all parse (`wire.ParseReadVarDBRead`); every answer holds per range the return code and the requested bytes |
| Requests the encoders build | byte for byte the requests of Snap7, TIA Portal and libnodave: setup, Read Var, SZL, SZL follow-up, block count, list and info, start upload, upload, end upload |
| Client against the CPU 315-2 PN/DP's recorded answers | `Identify`, `GetCPUState`, `BlockCounts`, `UploadBlock` (SDB 0) give the values Snap7 logged in the same session; `ListBlocks` (SFC in two packets), `GetBlockInfo`, `GetProtectionLevel`, `ReadDiagBuffer` give the values Wireshark decodes |
| Client against the IM151-8 PN/DP CPU's recorded answers | `Identify`, `GetCPUState`, `ReadDiagBuffer`, `BlockCounts`, `ListBlocks` |
| Server, every recorded Setup Communication, Read Var and Write Var job | the CPU's answer byte for byte, every one: 27 setups (AmQ as granted), 3,411 reads (including 86 of timers and counters and 84 DBREAD jobs of the S7-400), 2,082 writes (including the refused timer and counter write and the S7-1200's refused write to a write-protected DB) |

A CPU 315-2 PN/DP refuses a write to its timers and counters with item code `0x03` (object access is not allowed) while it reads them; it answers `0x0A` for a DB that does not exist. A CPU 1214C (S7-1200, V4.2) refuses a write to a write-protected DB with `0x03` too, the code package `server` sends for a read-only DB ([ERRORS.md](ERRORS.md#refused-access-item-codes-and-header-errors)).

## Not covered

DBREAD and the server's timers and counters have no interop peer: neither Snap7 nor python-snap7 implements DBREAD, snap7-interop's fixtures have no timer or counter areas, and Snap7's server maps timer addresses differently (`Start >> 1`) and accepts the writes a real CPU refuses. Their evidence is the recorded traffic above.

No independent peer and no recorded traffic covers these; they are tested only against the fake server, if at all: timers and counters through the client, `Discover`, `ProbeRackSlots`, `CompareRead`, `ProbeReadableRanges`, multi-PDU read chunking. snap7-interop lists them as deferred or client-side (`docs/go-s7comm-coverage.md`).

## Our own server (not a peer)

snap7-interop publishes go-s7comm's own `server` package behind the same contract as `ghcr.io/otfabric/snap7-interop-otfabric` (adapter `otfabric`, `independentEngine: false`), built from the latest go-s7comm release. The manual **Self-interop** workflow runs this repository's client against its `:latest` and logs the digest. A pass there shows that the client and a released server of this library agree on the wire; it is not independent verification and adds nothing to the results above. One fixture step differs on purpose: the server answers a write to a read-only area with `0x03` (write-protected, the Siemens code, and what a recorded S7-1200 sends), where both peers send `0x0A`. snap7-interop classifies it as an intentional difference ([COMPATIBILITY.md](https://github.com/otfabric/snap7-interop/blob/main/docs/COMPATIBILITY.md#write-to-a-read-only-area-0x03-and-0x0a)); the codes are compared in [ERRORS.md](ERRORS.md#refused-access-item-codes-and-header-errors).

Last run, 2026-11-07: Self-interop ([run 42349307896](https://github.com/otfabric/go-s7comm/actions/runs/42349307896)), this repository's client at v0.12.0 against `snap7-interop-otfabric:v0.12.0` (`sha256:3a5e98b34f6fcdfa767b5badcc9b46aad4efdb565e72324045e643888f929eaf`), all 15 fixtures passed, with that known difference. snap7-interop's Own stack run of the same image ([run 42349157492](https://github.com/otfabric/snap7-interop/actions/runs/42349157492), harness v0.6.1): 14 pass and 1 known difference against each reference.

## Reproduce

```sh
make interop                                  # all fixtures, both peers
SNAP_INTEROP_FIXTURE=basic-db make interop    # one fixture
```

Requires Docker. Options: [TESTING.md](TESTING.md#interoperability-suite). To refresh fixtures, see [interop/testdata/README.md](interop/testdata/README.md).
