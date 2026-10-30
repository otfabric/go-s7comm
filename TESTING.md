# Testing

## Tiers

| Tier | Command | Prerequisites | CI |
|---|---|---|---|
| Unit, with race detector | `make test` | none; tests use a fake server, no PLC | `ci.yml` (go-ci), Go 1.23 to 1.26 |
| Coverage gate | `make coverage-check` | none | `ci.yml` (go-ci `coverage-min`) |
| Fuzz | `make fuzz` (`FUZZTIME`, default 10s per target) | none | `ci.yml` (go-ci `fuzz-time: 10s`) |
| Benchmarks | `make bench` | none | not run |
| Cross-compilation | `make cross` | none | not run |
| Interoperability | `make interop` | Docker, ports 1102 and 2102 free | `interop.yml`, every push and pull request |

`make check` runs formatting, both linters, govulncheck, vet (including a compile of `interop/`), `test` and `coverage`. It does not start Docker.

There is no end-to-end tier and no e2e make target: the module has no S7 server, so it cannot run its client against itself. The in-package fake server (`client/fake_server_helpers_test.go`) covers client behaviour in the unit tier, and `make interop` covers real servers.

CI runs on Linux only. See [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md#platforms).

## Interoperability suite

`make interop` runs `go test -tags=interop ./interop/...` against the servers of snap7-interop. Peers, digests and results are in [INTEROPERABILITY.md](INTEROPERABILITY.md).

| Variable | Purpose |
|---|---|
| `SNAP_INTEROP_NATIVE_IMAGE`, `SNAP_INTEROP_PYTHON_IMAGE` | Override the pinned images |
| `SNAP_INTEROP_MANAGED=0` | Use running servers instead of Docker containers |
| `SNAP_INTEROP_NATIVE_ADDR`, `SNAP_INTEROP_PYTHON_ADDR` | Addresses of those servers |
| `SNAP_INTEROP_FIXTURE=<id>` | Run one fixture |

With managed servers and no Docker, the suite skips (`interop/harness.go`). There is no `*_REQUIRE_*` switch that turns this into a failure: check `go test -v` output to confirm that the matrix ran.

## Coverage policy

`make coverage` and the go-ci coverage job measure `./...` with `-covermode=atomic`; no package is excluded in this repository's configuration. The minimum is `coverage-min` in [.github/workflows/ci.yml](.github/workflows/ci.yml), mirrored by `COVER_MIN` in the [Makefile](Makefile). It is 90, the profile floor. Raise both as tests are added; never lower them.

## Fuzzing

Fuzz targets live in `wire/`, next to the decoders. `make fuzz` discovers them; do not list them by name. Every new decoder of untrusted input gets a target.

Regression procedure: when a fuzz run fails, Go writes the input to `testdata/fuzz/<FuzzName>/` in the package. Commit that file with the fix, so `go test` replays it as a seed from then on.

## Not tested here

- No goroutine-leak check.
- No test against a physical PLC ([LIMITATIONS.md](LIMITATIONS.md#verification)).
