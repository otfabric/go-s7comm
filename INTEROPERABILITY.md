# Interoperability

Evidence that go-s7comm works with S7 implementations it does not contain. Nothing here is a certification or a conformance claim, and both peers are server libraries, not Siemens PLCs. Feature-by-feature status: [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md).

## Peers

Both peers run from [snap7-interop](https://github.com/otfabric/snap7-interop) v0.3.0 (container contract 1.0, fixture schema 1.0.0), as multi-arch images pinned by digest in the [Makefile](Makefile) and [.github/workflows/interop.yml](.github/workflows/interop.yml).

| Peer | Implementation | Image digest |
|---|---|---|
| native | Snap7, SCADACS fork at `f6ff90317ca5d54250f4dcd29209689a74e26d82` | `ghcr.io/otfabric/snap7-interop-native@sha256:66e3826813005bfacb8510ce5de33119315499325b35bf616dcf61de0506a342` |
| python | python-snap7 3.2.1, pure-Python `Server` API, no libsnap7 | `ghcr.io/otfabric/snap7-interop-python@sha256:a53c81d7b6acb042a010fc1c9932f1f92b8c337e2555e3200938f4b0b46e3d54` |

Upstream versions are from snap7-interop `versions.yaml` at v0.3.0.

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

- A rejected write to read-only memory is reported by both adapters as item code `0x0A`, normalised to `address-out-of-range`. Stock Snap7 does not use the distinct write-protected code `0x03`. So this suite does not exercise that code.
- The python adapter changes stock python-snap7 behaviour for out-of-range access, read-only access and bit writes so that it matches native Snap7 (snap7-interop `docs/compatibility.md`). Agreement between the peers on these points is therefore by construction, not two independent observations.

## Not covered

No independent peer has exercised these; they are tested only against the fake server, if at all: SZL, `Identify`, CPU state, protection level, diagnostic buffer, block list, info and upload, timers and counters, `Discover`, `ProbeRackSlots`, `CompareRead`, `ProbeReadableRanges`, multi-PDU read chunking. snap7-interop lists them as deferred or client-side (`docs/go-s7comm-coverage.md`).

## Reproduce

```sh
make interop                                  # all fixtures, both peers
SNAP_INTEROP_FIXTURE=basic-db make interop    # one fixture
```

Requires Docker. Options: [TESTING.md](TESTING.md#interoperability-suite). To refresh fixtures, see [interop/testdata/README.md](interop/testdata/README.md).
