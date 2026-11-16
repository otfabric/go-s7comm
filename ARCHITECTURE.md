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

`server` (the in-memory test server) imports `wire` and go-cotp, like `client`, and nothing else of this module.

- **`client`** owns the connection: TCP dial (by the `WithDialer` function when one is set; the scan helpers `Discover`, `ProbeRackSlots` and `CompareRead` take their own: `WithDiscoverDialer`, `RackSlotProbeRequest.Dialer`, `CompareReadRequest.Dialer`), COTP connect through go-cotp, S7 setup communication, and every request/response exchange.
- **`wire`** encodes and parses S7 PDUs and TSAPs. It performs no I/O. It is strict on framing and declared lengths and leaves semantic interpretation to `client` and `model` ([wire/doc.go](wire/doc.go)).
- **`model`** holds transport-agnostic value types. `model/codec` is its only module dependency.
- **`server`** accepts COTP connections through go-cotp and answers jobs with the `wire` server codec from memory it holds. It does not import `client`.
- **`internal/capture`** reads the recorded captures in `testdata/captures` for the tests of `wire`, `client` and `server`; no library package imports it.
- **`interop`** carries the build tag `interop` and is excluded from normal builds. It uses `client`, `model` and `wire` through their public API.

The root package holds documentation only. Applications import the subpackages.

## Concurrency and ownership

A `Client` owns one `*cotp.Conn`. Its options do not change after `New`.

- `reqLock` serialises the calls, operations and `Connect`, so one request/response exchange runs at a time on a client. It is a lock whose wait ends with the caller's context: a call that waits for its turn returns its context's error and sends nothing. A `Logger` and a `Dialer` are called with it held. As a result `RangeProbeRequest.Parallelism` gives no wire-level concurrency.
- `mu` guards the session fields (`conn`, rack, slot, TSAPs, PDU size) for readers such as `ConnectionInfo` and `PDUSize`, and the Close state: a count of the calls of `Close` and the cancel function of the `Connect` or the pause of `WithRateLimit` in flight. A call takes `reqLock` first and `mu` inside it, never the other way round.
- `Close` takes `mu` only, so it never waits for a call, and may be called from a `Logger` or a `Dialer`. It counts itself, cancels the context of a `Connect` or a pause of `WithRateLimit` in flight, and closes the connection, which ends a read or write in flight (go-cotp's `Close` is safe concurrently with them). A call records the count when it starts; one that finds it changed returns `ErrClosed` before it sends, after a failed exchange, and before `Connect` installs its session, so a `Connect` that Close ended leaves no session.
- `WithRequestTimeout` derives a context for each exchange in `sendReceive` from the call's context, so the earlier deadline wins; go-cotp applies it to the read and the write of that exchange.
- A repeated `Connect` replaces the session only after the new handshake succeeds.
- An exchange that is given up after its request was sent (its context ended, or the transport failed) closes the connection and clears the session fields under `mu`. Answers are matched to requests by their order on the connection and checked by PDU reference, so a connection with an answer still to come is not used again.

A `Client` starts no goroutine of its own. Goroutines exist only in the multi-target functions, and each of these waits for its workers before returning:

| Function | Goroutines | Bound |
|---|---|---|
| `Discover` | worker pool over a job channel, one CIDR producer, one closer | `WithDiscoverParallel` |
| `ProbeRackSlots` | one per candidate | semaphore of `Parallelism` |
| `CompareRead` | one per candidate, each with its own `Client` | semaphore of `Parallelism` (default 1) |

Context behaviour, including why cancelling during I/O aborts the session, is in [ERRORS.md](ERRORS.md#context-reconnect-and-concurrency).

A `server.Server` runs one goroutine per accepted connection, bounded by `MaxConnections`, plus the caller's `Serve`. One `sync.RWMutex` guards every area: a Read Var job holds it for reading, a Write Var job for writing, so a multi-item job is one step. `Close` closes listeners and connections and waits for every connection goroutine: a connection is counted in the server's `sync.WaitGroup` under the lock that registers it, so `Close` also waits for one whose goroutine has not started yet.

## Platform backends

There are none: the module is pure Go with no build-constrained I/O code. Sockets come from the standard library through go-cotp.
