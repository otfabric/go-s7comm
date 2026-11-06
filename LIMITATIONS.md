# Limitations

What go-s7comm does not do, or does only partly. API behaviour is in [API.md](API.md) and [ERRORS.md](ERRORS.md).

## Scope not implemented

- **Classic S7comm only**, with no TLS and no password handling ([SECURITY.md](SECURITY.md)).
- **The server is for tests**: package `server` answers only Setup Communication, Read Var and Write Var on DBs, inputs, outputs and markers. It has no PLC program, timers, counters, SZL, blocks, clock, access protection or password; those requests get error `0x8104` (service not implemented).
- **Block download and PLC start/stop**: `wire` names these function codes, but `client` has no operation for them. Blocks can be listed, inspected and uploaded.
- **`Discover`** is IPv4 only and rejects ranges larger than `/12`.

## Behaviour to plan for

- `WithTimeout` bounds `Connect`, not later reads and writes: without a context deadline or cancel, open-session I/O can block indefinitely.
- Cancelling during I/O typically aborts the COTP session; reconnect afterwards.
- Protocol operations are serialised per client; `RangeProbeRequest.Parallelism` gives no wire-level concurrency.

## Verification

- Independently verified against: the native Snap7 and pure-Python servers of [snap7-interop](https://github.com/otfabric/snap7-interop) v0.3.0, pinned by digest in the Makefile. See [README.md](README.md#interop-tests).
- The server's codes are the Siemens ones (STEP 7 V20, *Use of FETCH/WRITE*, and Wireshark's s7comm dissector); they are not checked against a capture of a real CPU. Besides this module's client, one other client runs against it: snap7-interop's own probe (which does not use go-s7comm), in that harness's Own stack workflow, every canonical fixture next to both reference servers. One step differs on purpose: the server answers a write to a read-only area with `0x03` (object access is not allowed, which the probe reports as `write-protected`), where the `read-only-reject` fixture expects the `0x0A` that both Snap7-based references send. snap7-interop classifies it as an intentional difference (`tests/known-differences.tsv`, [COMPATIBILITY.md](https://github.com/otfabric/snap7-interop/blob/main/docs/COMPATIBILITY.md#write-to-a-read-only-area-0x03-and-0x0a)). Whether a physical CPU sends `0x03` or `0x0A` for a write-protected DB is open ([#18](https://github.com/otfabric/go-s7comm/issues/18)); the server keeps `0x03` until a capture shows otherwise. No third-party S7 client (Snap7, python-snap7, a Siemens HMI) has been run against it.
- Not verified: this repository holds no test against a physical PLC. Behaviour on a real CPU may differ from these servers.
- Certification: none.
