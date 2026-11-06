# Testing

## Tiers

| Tier | Command | Prerequisites | CI |
|---|---|---|---|
| Unit, with race detector | `make test` | none; tests use a fake server, no PLC | `ci.yml` (go-ci), Go 1.26 and 1.27 |
| End-to-end | `make e2e` | none; also part of `make test` | `ci.yml` (go-ci) |
| Coverage gate | `make coverage-check` | none | `ci.yml` (go-ci `coverage-min`) |
| Per-package coverage floor | `make coverage-pkg-check` | none | `ci.yml` (`Per-package coverage`) |
| Fuzz | `make fuzz` (`FUZZTIME`, default 10s per target) | none | `ci.yml` (go-ci `fuzz-time: 10s`); `extended.yml`, nightly, 5 minutes per target |
| Benchmarks | `make bench` | none | not run |
| Cross-compilation | `make cross` | none | not run |
| Interoperability | `make interop` | Docker, ports 1102 and 2102 free | `interop.yml`, every push and pull request |

`make check` runs formatting, both linters, govulncheck, vet (including a compile of `interop/`), `test` and `coverage-pkg-check`. It does not start Docker.

CI runs on Linux only. See [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md#platforms).

## End-to-end suite

`e2e/` holds a simulated PLC for its tests (`e2e/plc_test.go`): it keeps memory and answers by decoding the requests, over TCP loopback and the real COTP and TPKT stack. The suite uses the public API only: connect and PDU negotiation, write then read back in the DB and other areas, bits, transfers larger than the PDU, CPU state, refused addresses, a silent peer, a peer that hangs up followed by a reconnect, a foreign response, concurrent use of a client, and strict rack/slot probing. `e2e/hostile_test.go` corrupts every answer to a job with a seeded generator (flipped bytes, cuts, trailing bytes, lying parameter, data, item count and item length fields, overlong items) over 400 mixed calls: each call must return within its deadline, never panic, never return more bytes than asked, and leave no goroutine behind. It is written from this library's reading of the protocol, so it proves the client's behaviour and not conformance: that is what `make interop` is for. The scripted fake server in `client/fake_server_helpers_test.go` covers malformed answers in the unit tier.

## Server tests

`server/` tests the server three ways. Through this module's client on loopback: every area, bits, refusals, a read larger than the PDU, PDU negotiation, eight concurrent clients, reconnect with memory kept, the connection limit and the idle timeout. With hand-built PDUs: the exact item codes and transport sizes of the table in [API.md](API.md#server), 25 items, answers past the PDU (`0x8500`), jobs and user data not implemented (`0x8104`) and malformed PDUs. On raw sockets: garbage before the handshake, a malformed PDU, an unsupported job, and `Close` with connections open. `FuzzHandle` feeds arbitrary PDUs to a session and checks that every PDU with a readable header gets an answer with its PDU reference.

## Goroutine leaks

`client`, `e2e` and `server` run `goleak.VerifyTestMain`: a test that leaves a goroutine behind fails the package. `go.uber.org/goleak` is a test-only dependency.

## Interoperability suite

`make interop` runs `go test -tags=interop ./interop/...` against the servers of snap7-interop. Peers, digests and results are in [INTEROPERABILITY.md](INTEROPERABILITY.md).

| Variable | Purpose |
|---|---|
| `SNAP_INTEROP_NATIVE_IMAGE`, `SNAP_INTEROP_PYTHON_IMAGE` | Override the pinned images |
| `SNAP_INTEROP_MANAGED=0` | Use running servers instead of Docker containers |
| `SNAP_INTEROP_NATIVE_ADDR`, `SNAP_INTEROP_PYTHON_ADDR`, `SNAP_INTEROP_OTFABRIC_ADDR` | Addresses of those servers |
| `SNAP_INTEROP_FIXTURE=<id>` | Run one fixture |
| `SNAP_INTEROP_OTFABRIC_IMAGE` | Adds `otfabric`, go-s7comm's own server as snap7-interop publishes it (`ghcr.io/otfabric/snap7-interop-otfabric`). Never set by default; not a reference |
| `SNAP_INTEROP_SERVERS=otfabric` | Run only the named servers (comma-separated) |

The **Self-interop** workflow (manual, never required) runs this commit's client against `snap7-interop-otfabric:latest` with `SNAP_INTEROP_SERVERS=otfabric` and logs the image digest. It finds a wire change that breaks an earlier release; it is not independent verification. One step is a known difference (`knownDifferences` in `interop/runner.go`): that image answers a write to a read-only area with `0x03` (write-protected, the Siemens code), the references with `0x0A`; snap7-interop lists it in `tests/known-differences.tsv` as an intentional difference.

With managed servers and no Docker, the suite skips (`interop/harness.go`). There is no `*_REQUIRE_*` switch that turns this into a failure: check `go test -v` output to confirm that the matrix ran.

## Coverage policy

`make coverage` and the go-ci coverage job measure `./...` with `-covermode=atomic`; no package is excluded in this repository's configuration. The minimum is `coverage-min` in [.github/workflows/ci.yml](.github/workflows/ci.yml), mirrored by `COVER_MIN` in the [Makefile](Makefile). It is 90, the profile floor. Each package also has to reach `COVER_PKG_MIN` (90), checked by `make coverage-pkg-check`. Raise them as tests are added; never lower them.

## Fuzzing

Fuzz targets live in `wire/`, `model/codec/` and `server/`, next to the decoders and encoders. Encoder targets check that every input is either refused with `ErrOutOfRange` or encoded with exactly its fields and consistent lengths. `make fuzz` discovers them; do not list them by name. Every new decoder of untrusted input gets a target.

Regression procedure: when a fuzz run fails, Go writes the input to `testdata/fuzz/<FuzzName>/` in the package. Commit that file with the fix, so `go test` replays it as a seed from then on.

## Not tested here

- No test against a physical PLC ([LIMITATIONS.md](LIMITATIONS.md#verification)).
