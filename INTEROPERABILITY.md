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

`interop.yml` runs the suite on every push and pull request. The suite runs all 15 fixtures vendored in [interop/testdata/fixtures](interop/testdata/fixtures) against both peers, at rack 0, slot 2, PDU size 480. Each server is restarted for each fixture.

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

## Not covered

No independent peer has exercised these; they are tested only against the fake server, if at all: SZL, `Identify`, CPU state, protection level, diagnostic buffer, block list, info and upload, timers and counters, `Discover`, `ProbeRackSlots`, `CompareRead`, `ProbeReadableRanges`, multi-PDU read chunking. snap7-interop lists them as deferred or client-side (`docs/go-s7comm-coverage.md`).

## Our own server (not a peer)

snap7-interop publishes go-s7comm's own `server` package behind the same contract as `ghcr.io/otfabric/snap7-interop-otfabric` (adapter `otfabric`, `independentEngine: false`), built from the latest go-s7comm release. The manual **Self-interop** workflow runs this repository's client against its `:latest` and logs the digest. A pass there shows that the client and a released server of this library agree on the wire; it is not independent verification and adds nothing to the results above. One fixture step differs on purpose: the server answers a write to a read-only area with `0x03` (write-protected, the Siemens code), where both peers send `0x0A`. snap7-interop classifies it as an intentional difference ([COMPATIBILITY.md](https://github.com/otfabric/snap7-interop/blob/main/docs/COMPATIBILITY.md#write-to-a-read-only-area-0x03-and-0x0a)); the codes are compared in [ERRORS.md](ERRORS.md#refused-access-item-codes-and-header-errors).

Last run, 2026-11-07: Self-interop ([run 42339351676](https://github.com/otfabric/go-s7comm/actions/runs/42339351676)), this repository's client at v0.10.2 against `snap7-interop-otfabric:v0.10.2` (`sha256:e08fadb33956a33eb513625298968da40670c7991bab03f12c785809382fd587`), all 15 fixtures passed, with that known difference. snap7-interop's Own stack run of the same image ([run 42339190582](https://github.com/otfabric/snap7-interop/actions/runs/42339190582), harness v0.6.1): 14 pass and 1 known difference against each reference.

## Reproduce

```sh
make interop                                  # all fixtures, both peers
SNAP_INTEROP_FIXTURE=basic-db make interop    # one fixture
```

Requires Docker. Options: [TESTING.md](TESTING.md#interoperability-suite). To refresh fixtures, see [interop/testdata/README.md](interop/testdata/README.md).
