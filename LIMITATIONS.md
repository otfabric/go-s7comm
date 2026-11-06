# Limitations

What go-s7comm does not do, or does only partly. API behaviour is in [API.md](API.md) and [ERRORS.md](ERRORS.md).

## Scope not implemented

- **Classic S7comm only**, with no TLS and no password handling ([SECURITY.md](SECURITY.md)).
- **The server is for tests**: package `server` answers only Setup Communication, Read Var and Write Var on DBs, inputs, outputs and markers. It has no PLC program, timers, counters, SZL, blocks, clock, access protection or password, and it gives no answer to those requests.
- **Block download and PLC start/stop**: `wire` names these function codes, but `client` has no operation for them. Blocks can be listed, inspected and uploaded.
- **`Discover`** is IPv4 only and rejects ranges larger than `/12`.

## Behaviour to plan for

- `WithTimeout` bounds `Connect`, not later reads and writes: without a context deadline or cancel, open-session I/O can block indefinitely.
- Cancelling during I/O typically aborts the COTP session; reconnect afterwards.
- Protocol operations are serialised per client; `RangeProbeRequest.Parallelism` gives no wire-level concurrency.

## Verification

- Independently verified against: the native Snap7 and pure-Python servers of [snap7-interop](https://github.com/otfabric/snap7-interop) v0.3.0, pinned by digest in the Makefile. See [README.md](README.md#interop-tests).
- The server's answers follow the Snap7 server source, not a capture of a real CPU. Besides this module's client, one other client has been run against it: snap7-interop's own probe (v0.4.2, which does not use go-s7comm) passed all 15 canonical fixtures, 84 steps, in a local run on 2026-11-06. No third-party S7 client (Snap7, python-snap7, a Siemens HMI) has been run against it.
- Not verified: this repository holds no test against a physical PLC. Behaviour on a real CPU may differ from these servers.
- Certification: none.
