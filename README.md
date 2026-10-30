# otfabric/go-s7comm - Siemens S7 Protocol Library for Go

[![Go](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-s7comm.svg)](https://pkg.go.dev/github.com/otfabric/go-s7comm)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI](https://github.com/otfabric/go-s7comm/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-s7comm/actions/workflows/ci.yml)
[![Codecov](https://codecov.io/gh/otfabric/go-s7comm/graph/badge.svg)](https://codecov.io/gh/otfabric/go-s7comm)
[![Release](https://img.shields.io/github/v/release/otfabric/go-s7comm?label=release)](https://github.com/otfabric/go-s7comm/releases)

A pure Go implementation of the Siemens S7 communication protocol. It builds on [go-cotp](https://github.com/otfabric/go-cotp) for the TP0 transport service (complete TSDUs). TPKT framing is owned by go-cotp via [go-tpkt](https://github.com/otfabric/go-tpkt) and is not a direct dependency of this module.

The library provides:

- S7 client connection setup (TCP + go-cotp TP0 + S7 setup communication)
- Read/write operations for DB, inputs, outputs, and markers (rich `ReadResult` with explicit status), plus native single-bit `ReadBit`/`WriteBit` (BIT transport, not byte RMW)
- Readable range scan and compare-read across rack/slot candidates
- Device discovery over CIDR ranges with rack/slot probing
- SZL-based identification and diagnostics helpers
- Block listing, block metadata retrieval, and block upload
- Low-level wire parsing/encoding packages for protocol internals

**Docs:** [API.md](API.md) (public API) · [ERRORS.md](ERRORS.md) (error and result semantics) · [OBSERVABILITY.md](OBSERVABILITY.md) (logging) · [ARCHITECTURE.md](ARCHITECTURE.md) (layers, concurrency) · [TESTING.md](TESTING.md) (test tiers) · [INTEROPERABILITY.md](INTEROPERABILITY.md) (peers, results) · [Feature status](docs/FEATURE-STATUS.md) · [LIMITATIONS.md](LIMITATIONS.md) · [RELEASE.md](RELEASE.md) (changelog)

## Table of contents

- [Error and result semantics](#error-and-result-semantics)
- [Install](#install)
- [Project structure](#project-structure)
- [Quickstart](#quickstart)
- [Discovery](#discovery)
- [Rack/Slot Probe](#rackslot-probe)
- [Readable range scan](#readable-range-scan)
- [Compare read](#compare-read)
- [Development](#development)
- [Interop tests](#interop-tests)
- [License](#license)

## Error and result semantics

| Situation | How it is reported |
|-----------|--------------------|
| Validation failure (bad input) | `*ValidationError`; use `errors.As(err, &ValidationError{})` to detect |
| Byte read outcome (success / short / empty / rejected / timeout) | `ReadResult.Status` and `result.Err()` |
| Bit R/W, writes, Connect, Upload, GetCPUState, … | Strict `error` return |
| Metadata / best-effort (Identify, GetBlockInfo, …) | May return `(value, error)` both non-nil |

Full guide (statuses, sentinels, wire `S7Error`, CLI exit-code contract): **[ERRORS.md](ERRORS.md)**. API signatures: **[API.md](API.md)**.

**Context:** cancel without a deadline **is** effective on TCP dial and go-cotp
TSDU I/O (via go-cotp’s deadline watcher), but mid-I/O cancel typically **aborts**
the COTP session — reconnect afterward. Prefer `context.WithTimeout` /
`WithDeadline` for wall-clock bounds. Client `WithTimeout` bounds **Connect**
(and dial), not ongoing PDU reads/writes; without cancel and without a deadline,
open-session I/O can block indefinitely. `UploadBlock` cleanup uses a separate
500ms background context. Details: [ERRORS.md](ERRORS.md#context-reconnect-and-concurrency).

A second `Connect()` on an already-connected client only replaces the session
after the new handshake succeeds, so a failed reconnect does not drop a healthy
session. Logging is silent by default; see [OBSERVABILITY.md](OBSERVABILITY.md).

## Install

```sh
go get github.com/otfabric/go-s7comm
```

Requires Go 1.23 or later.

## Project structure

```
go-s7comm/
├── doc.go             Module-root package docs (import client/model/wire)
├── client/            High-level S7 API (Connect, R/W, discover, probe, SZL, blocks)
├── model/             Areas, addresses, device info, codecs
│   └── codec/         Typed encode/decode helpers
├── wire/              S7 PDU encode/decode and TSAP helpers (no TPKT/COTP)
├── interop/           Build-tagged (`-tags=interop`) Snap7 black-box tests
├── testdata/          Frame fixtures
├── API.md             Public API reference
├── ERRORS.md          Error / ReadResult semantics
├── OBSERVABILITY.md   Logging (silent by default)
└── RELEASE.md         Release history
```

Transport stack: **client** → [go-cotp](https://github.com/otfabric/go-cotp) (`Connect` / `ReadTSDU` / `WriteTSDU`) → go-tpkt (owned by go-cotp). This module does not import go-tpkt directly.

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/otfabric/go-s7comm/client"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := client.New("192.168.0.10", client.WithRackSlot(0, 1))
	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	result, err := c.ReadDB(ctx, 1, 0, 16)
	if err != nil {
		log.Fatal(err)
	}
	if err := result.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("DB1.DBB0..15 = % X\n", result.Data)

	// Native BIT transport (not byte read-modify-write): DB1.DBX10.3
	if err := c.WriteDBBit(ctx, 1, 10, 3, true); err != nil {
		log.Fatal(err)
	}
	bit, err := c.ReadDBBit(ctx, 1, 10, 3)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("DB1.DBX10.3 = %v\n", bit)
}
```

## Discovery

Use conservative CIDR ranges in OT environments (e.g. `/24` or smaller); the library allows up to `/12` but large scans create many connections and load.

```go
results, err := client.Discover(ctx, "192.168.0.0/24",
	client.WithDiscoverParallel(20),
	client.WithDiscoverRackSlotRange(0, 3, 0, 5),
)
```

Each result reports IP/port reachability, detected rack/slot, negotiated PDU size, and TSAP.

## Rack/Slot Probe

Probe a target for accepted rack/slot combinations:

```go
result, err := client.ProbeRackSlots(ctx, client.RackSlotProbeRequest{
	Address:     "192.168.0.10",
	Port:        102,
	RackMin:     0,
	RackMax:     7,
	SlotMin:     0,
	SlotMax:     31,
	Timeout:     2 * time.Second,
	Parallelism: 4,
})
```

Each candidate has a `Status` and `Stage`. Without `Strict`, "valid" means setup was accepted (`setup-only`, `valid-connect`, or `valid-query`). With **strict mode** (`Strict: true`), "valid" means only **valid-query**: setup succeeded and a benign follow-up S7 query (e.g. SZL or CPU state) also succeeded. This avoids false positives from permissive gateways or simulators that accept setup but do not map to a real CPU.

Strict mode with default confirmation (try SZL, then CPU state, then protection):

```go
result, err := client.ProbeRackSlots(ctx, client.RackSlotProbeRequest{
	Address: "192.168.0.10",
	Port:    102,
	Strict:  true,  // equivalent to Confirm: client.ConfirmAny
})
```

Use a specific confirmation strategy:

```go
result, err := client.ProbeRackSlots(ctx, client.RackSlotProbeRequest{
	Address:  "192.168.0.10",
	Strict:   true,
	Confirm:  client.ConfirmSZL,  // or ConfirmCPUState, ConfirmAny
})
```

The result exposes summary counts: **SetupAccepted**, **ConfirmedByQuery**, and **TCPOnly**. In strict mode only candidates with `valid-query` are included in `result.Valid`.

| Status           | Meaning                                                         |
|------------------|------------------------------------------------------------------|
| `valid-query`    | Setup ok and follow-up query succeeded (strongest)               |
| `valid-connect`  | Setup ok; follow-up failed or not attempted                     |
| `setup-only`     | Setup ok; no follow-up (non-strict only)                         |
| `cotp-only`      | COTP ok, S7 setup failed                                         |
| `tcp-only`       | TCP ok, COTP failed                                             |
| `unreachable`    | TCP connect failed                                              |
| `rejected`       | Target rejected (S7 error)                                      |

Use `StopOnFirst: true` to stop after the first valid combination; in strict mode that means the first `valid-query`.

## Readable range scan

Scan an area to discover which byte ranges are readable (client must be connected):

```go
import (
	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/model"
)

result, err := c.ProbeReadableRanges(ctx, client.RangeProbeRequest{
	Area:      model.AreaInputs,
	Start:     0,
	End:       256,
	Step:      8,
	ProbeSize: 8,
	Repeat:    1,
	Retries:   0,
})
// result.Spans = consolidated [Start, End) ranges per status
// result.Summary.ReadableSpans, .EmptySpans, .FailedSpans, .InconclusiveSpans
// result.Probes = raw per-offset observations
```

## Compare read

Run the same read across multiple rack/slot candidates to detect whether the endpoint responds identically (rack/slot-insensitive):

```go
import (
	"github.com/otfabric/go-s7comm/client"
	"github.com/otfabric/go-s7comm/model"
)

result, err := client.CompareRead(ctx, client.CompareReadRequest{
	Address:    "192.168.0.10",
	Port:       102,
	Candidates: []client.RackSlot{{0, 1}, {0, 2}},
	Area:       model.AreaDB,
	DBNumber:   1,
	Offset:     0,
	Size:       32,
})
// result.ByCandidate = one ReadResult per candidate
// result.RackSlotInsensitive = true if all succeeded with identical data
```

For CLI usage see [s7commctl probe rackslot](https://github.com/otfabric/s7commctl):

```sh
s7commctl probe rackslot --ip 192.168.0.10
s7commctl probe rackslot --ip 192.168.0.10 --strict
s7commctl probe rackslot --ip 192.168.0.10 --confirm szl
s7commctl probe rackslot --ip 192.168.0.10 --strict --first-confirmed
```

## Development

```sh
make check
```

Useful targets:

- `make test`
- `make test-race` — tests with race detector
- `make coverage`
- `make coverage-check` — fail below the CI minimum
- `make fuzz` — run every fuzz target (`FUZZTIME`, default 10s)
- `make cross` — cross-compile without cgo
- `make bench` — run benchmarks (discovery, probe, compare, wire parsers)
- `make lint` — staticcheck
- `make lint-ci` — golangci-lint
- `make vuln` — govulncheck
- `make interop` — SNAP7 interop suite (requires Docker; see below)

## Interop tests

Black-box tests against both [snap7-interop](https://github.com/otfabric/snap7-interop) v0.3.0 servers (native Snap7 and pure-Python), using the full canonical fixture matrix. Servers listen on host ports **1102** (native) and **2102** (python); rack/slot is **0/2**.

```sh
make interop
```

This pulls pinned GHCR image digests, starts both containers per fixture, and runs `go test -tags=interop ./interop/...`. `make check` typechecks the interop package (`-tags=interop` vet/lint + compile) but does not start Docker or run the matrix.

| Variable | Purpose |
| --- | --- |
| `SNAP_INTEROP_NATIVE_IMAGE` / `SNAP_INTEROP_PYTHON_IMAGE` | Override pinned image refs |
| `SNAP_INTEROP_MANAGED=0` | Use already-running servers (`SNAP_INTEROP_NATIVE_ADDR` / `SNAP_INTEROP_PYTHON_ADDR`) |
| `SNAP_INTEROP_FIXTURE=<id>` | Run a single fixture (e.g. `basic-db`) |

## License

This project is licensed under the MIT License. See [LICENSE](./LICENSE).
