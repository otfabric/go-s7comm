# Contributing to go-s7comm

## Development setup

- Go 1.23 or later (see [go.mod](go.mod)).
- [staticcheck](https://staticcheck.dev/), [golangci-lint](https://golangci-lint.run/) and [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) for `make check`.
- Docker for the interop suite only.

```sh
git clone https://github.com/otfabric/go-s7comm.git
cd go-s7comm
make check
```

Run `make help` for the list of targets.

## Tests

- `make test`: unit tests with the race detector. Tests use a fake server and need no PLC.
- `make coverage`: writes `coverage.out`. CI enforces the minimum in `.github/workflows/ci.yml`; do not lower it.
- `make interop`: the Snap7 suite, see [README.md](README.md#interop-tests).

Add a fuzz target for any new decoder of untrusted input.

## Submitting changes

1. Work on a branch and open a pull request against `main`.
2. Add tests, including error paths.
3. Update [API.md](API.md), [ERRORS.md](ERRORS.md) and [LIMITATIONS.md](LIMITATIONS.md) when behaviour changes, and add a section to [RELEASE.md](RELEASE.md) for user-visible changes.

Report security issues as described in [SECURITY.md](SECURITY.md), not in public issues.
