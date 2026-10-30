# Architecture

How go-s7comm is structured. Public API: [API.md](API.md). Scope: [LIMITATIONS.md](LIMITATIONS.md).

## Layers

The arrows follow the import graph (`go list -f '{{.Imports}}' ./...`).

```text
             client  (sessions, read/write, SZL, blocks, discover, probe, compare)
             /    \
            v      v
         wire    model  (areas, addresses, device info, enums)
            \        \
             \        v
              \      model/codec  (big-endian value conversion)
               v
             go-cotp  (TP0 TSDUs; also imported directly by client)
               |
               v
             go-tpkt  (TPKT framing; a dependency of go-cotp only)
```

- **`client`** owns the connection: TCP dial, COTP connect through go-cotp, S7 setup communication, and every request/response exchange.
- **`wire`** encodes and parses S7 PDUs and TSAPs. It performs no I/O. It is strict on framing and declared lengths and leaves semantic interpretation to `client` and `model` ([wire/doc.go](wire/doc.go)).
- **`model`** holds transport-agnostic value types. `model/codec` is its only module dependency.
- **`interop`** carries the build tag `interop` and is excluded from normal builds. It uses `client`, `model` and `wire` through their public API.

The root package holds documentation only. Applications import the subpackages.

## Concurrency and ownership

A `Client` owns one `*cotp.Conn`. Its options do not change after `New`.

- `reqMu` serialises protocol operations, so one request/response exchange runs at a time on a client. `Connect` and `Close` take it too. As a result `RangeProbeRequest.Parallelism` gives no wire-level concurrency.
- `mu` guards the session fields (`conn`, rack, slot, TSAPs, PDU size) for readers such as `ConnectionInfo` and `PDUSize`.
- A repeated `Connect` replaces the session only after the new handshake succeeds.

A `Client` starts no goroutine of its own. Goroutines exist only in the multi-target functions, and each of these waits for its workers before returning:

| Function | Goroutines | Bound |
|---|---|---|
| `Discover` | worker pool over a job channel, one CIDR producer, one closer | `WithDiscoverParallel` |
| `ProbeRackSlots` | one per candidate | semaphore of `Parallelism` |
| `CompareRead` | one per candidate, each with its own `Client` | semaphore of `Parallelism` (default 1) |

Context behaviour, including why cancelling during I/O aborts the session, is in [ERRORS.md](ERRORS.md#context-reconnect-and-concurrency).

## Platform backends

There are none: the module is pure Go with no build-constrained I/O code. Sockets come from the standard library through go-cotp.
