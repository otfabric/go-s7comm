# Limitations

What go-s7comm does not do, or does only partly. API behaviour is in [API.md](API.md) and [ERRORS.md](ERRORS.md).

## Scope not implemented

- **Classic S7comm only**, with no TLS and no password handling ([SECURITY.md](SECURITY.md)).
- **Client only**: the module has no S7 server.
- **Block download and PLC start/stop**: `wire` names these function codes, but `client` has no operation for them. Blocks can be listed, inspected and uploaded.
- **`Discover`** is IPv4 only and rejects ranges larger than `/12`.

## Behaviour to plan for

- `WithTimeout` bounds `Connect`, not later reads and writes: without a context deadline or cancel, open-session I/O can block indefinitely.
- Cancelling during I/O typically aborts the COTP session; reconnect afterwards.
- Protocol operations are serialised per client; `RangeProbeRequest.Parallelism` gives no wire-level concurrency.

## Verification

- Independently verified against: the native Snap7 and pure-Python servers of [snap7-interop](https://github.com/otfabric/snap7-interop) v0.3.0, pinned by digest in the Makefile. See [README.md](README.md#interop-tests).
- Not verified: this repository holds no test against a physical PLC. Behaviour on a real CPU may differ from these servers.
- Certification: none.
