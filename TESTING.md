# Testing

## Tiers

| Tier | Command | Prerequisites | CI |
|---|---|---|---|
| Unit, with race detector | `make test` | none; tests use a fake server, no PLC | `ci.yml` (go-ci), Go 1.26 and 1.27 |
| End-to-end | `make e2e` | none; also part of `make test` | `ci.yml` (go-ci) |
| Coverage gate | `make coverage-check` | none | `ci.yml` (go-ci `coverage-min`) |
| Per-package coverage floor | `make coverage-pkg-check` | none | `ci.yml` (`Per-package coverage`) |
| Fuzz | `make fuzz` (`FUZZTIME`, default 10s per target) | none | `ci.yml` (go-ci `fuzz-time: 10s`); `extended.yml`, nightly, 5 minutes per target, one job per target |
| Benchmarks | `make bench`; `make bench-smoke` runs each once | none | `extended.yml` (`Benchmarks run once`, nightly): `make bench-smoke`, so a benchmark that no longer runs is seen the next night. Timings are not compared |
| Cross-compilation | `make cross` | none | not run |
| 32-bit architecture | `GOARCH=386 go test -count=1 ./...` | a host that runs linux/386 binaries (linux/amd64 does) | `extended.yml`, nightly (`Tests on a 32-bit architecture`), without the race detector, which 386 does not have |
| Interoperability | `make interop` | Docker, ports 1102 and 2102 free; without Docker it fails (`SNAP_INTEROP_REQUIRE_DOCKER=1`) | `interop.yml`, every pull request |
| Recorded traffic of real CPUs | part of `make test` | none; the captures are in `testdata/captures` | `ci.yml` (go-ci) |

`make check` runs formatting, both linters, govulncheck, vet (including a compile of `interop/`), `test` and `coverage-pkg-check`. It does not start Docker.

CI runs on Linux only. See [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md#platforms).

## End-to-end suite

`e2e/` holds a simulated PLC for its tests (`e2e/plc_test.go`): it keeps memory and answers by decoding the requests, over TCP loopback and the real COTP and TPKT stack. The suite uses the public API only: connect and PDU negotiation, write then read back in the DB and other areas, bits, transfers larger than the PDU, CPU state, refused addresses, a silent peer, a peer that hangs up followed by a reconnect, a foreign response, concurrent use of a client, and strict rack/slot probing. `e2e/hostile_test.go` corrupts every answer to a job with a seeded generator (flipped bytes, cuts, trailing bytes, lying parameter, data, item count and item length fields, overlong items) over 400 mixed calls: each call must return within its deadline, never panic, never return more bytes than asked, and leave no goroutine behind. `e2e/dialer_chaos_test.go` passes `Connect` a connection through `WithDialer` that injects one seeded transport fault into the TCP, COTP and S7 setup exchange (one-byte reads, a cut after n incoming or outgoing bytes, a flipped byte, a peer that goes silent) over 120 reconnects: each must return within its timeout, a fragmenting transport must connect, and a failed reconnect must leave the established session working. `e2e/scan_dialer_chaos_test.go` does the same for `Discover`, `ProbeRackSlots` (strict, so the SZL query runs) and `CompareRead`: their dialer redirects an unroutable address to the simulated PLC and gives every connection of a call one seeded fault, over 60 calls each. Each call must return within its timeouts; a fragmenting transport must still find, confirm and read; no probe candidate may be classed at the TCP stage, since the dial itself succeeds; a read that succeeds returns the PLC's memory unless a byte was flipped; and every other fault must make some call fail. `client/scan_dialer_test.go` covers the dialers of the scan helpers in the unit tier. It is written from this library's reading of the protocol, so it proves the client's behaviour and not conformance: that is what `make interop` is for. The scripted fake server in `client/fake_server_helpers_test.go` covers malformed answers in the unit tier.

## Server tests

`server/` tests the server three ways. Through this module's client on loopback: every area, bits, refusals, a read larger than the PDU, PDU negotiation, eight concurrent clients, reconnect with memory kept, the connection limit and the idle timeout. With hand-built PDUs: the exact item codes and transport sizes of the table in [API.md](API.md#server), 25 items, answers past the PDU (`0x8500`), jobs and user data not implemented (`0x8104`) and malformed PDUs. On raw sockets: garbage before the handshake, a malformed PDU, an unsupported job, and `Close` with connections open. `FuzzHandle` feeds arbitrary PDUs to a session and checks that every PDU with a readable header gets an answer with its PDU reference. `TestChaosRealCPUJobs` mutates the jobs real clients send (DBREAD, timers, counters, mixed items, setup) 20,000 times with a seeded generator: every answer keeps its reference and the PDU size, and no client write reaches timers or counters. `TestPipelinedDBReadAndTimerJobs` sends two jobs over TCP and COTP before reading, as AmQ 2 allows, and gets both answers in order.

## Recorded traffic of real CPUs

[testdata/captures](testdata/captures/README.md) holds 26 captures (14,397 S7 PDUs) between real S7-300/S7-400/S7-1200 CPUs and Snap7, STEP 7 5.5, TIA Portal V13, WinCC Advanced, libnodave and python-snap7, with their sources, devices and checksums. `internal/capture` reads them (pcap and pcapng, TCP reassembly, TPKT and COTP); its counts per file equal Wireshark 4.6.9's.

- `wire`: every PDU decodes; every Read Var, Write Var and Setup Communication decodes both ways, every successful item has the requested length, and every user data request has the parameter layout the encoders produce. `TestCapturesEncodersMatchRecordedRequests` checks that the encoders produce, byte for byte, requests the real clients sent.
- `client`: `TestCaptureReplay*` replays a capture as a server. A request is answered only if a real client sent the same bytes, so a request built differently fails the test. The results (Identify, CPU state, protection, diagnostic buffer, block counts, lists and info, an SDB upload) are compared with Snap7's log of the session and Wireshark's decode.
- `server`: `TestCaptureReplayMatchesRealCPUs` sends every recorded Setup Communication, Read Var and Write Var job to a server holding the memory the CPU reported (read-only where the CPU refused a write to a DB with `0x03`), and compares the answers byte for byte; all are identical. `wire` parses WinCC's 84 DBREAD requests to the S7-400, and three of them are parser fixtures and fuzz seeds.

## Lifecycle contract matrix

The rows are those of the organisation's lifecycle standard (otfabric/engineering `standards/lifecycle.md`, L1 to L16), for the two long-lived types of this module. `client.Client` owns one go-cotp connection and starts no goroutine; it has no reconnect loop, no queue and no event callbacks. `server.Server` owns listeners and one goroutine per connection. A row that cannot apply says n/a with the reason. The client tests are in `client/session_test.go`, the server tests in `server/lifecycle_test.go`, unless another file is named.

| Row | `client.Client` | `server.Server` |
|---|---|---|
| L1 Close twice | `TestClose_IsIdempotentAndEndsSession`, `TestCloseClearsConnection`: the second `Close` returns nil, the session is cleared, the next call returns `ErrNotConnected` | `TestServeAfterClose`: a second `Close` returns nil, and `Serve` on the closed server returns `ErrServerClosed` and closes its listener |
| L2 Two goroutines call Close | `TestCloseFromSeveralGoroutines`: four calls return nil and the connection is closed once | `TestCloseFromSeveralGoroutines`: four calls return nil after the server has stopped |
| L3 Close during a blocked read | `TestCloseEndsTheCallInFlight` (read, write, bit read, user data, upload): `Close` returns nil while the peer still holds the answer, and the call returns an error that matches `ErrClosed` and `cotp.ErrClosed` (reads: status `transport-error`); the connection is closed once and the client is disconnected. `TestCloseEndsACallWaitingForItsTurn` (synctest): a read and a `Connect` waiting behind the call in flight return `ErrClosed` too and send nothing. `TestRequestTimeoutAndTheCallsDeadline` (synctest): without a deadline the read is still in flight after an hour, until `Close` ends it | `TestCloseDuringABlockedRead`: `Close` returns with the connection's goroutine gone, and the client's read fails. `TestCloseEndsOpenConnections` does the same over TCP |
| L4 Close during a write | `TestCloseEndsAWriteInFlight` (synctest, in-memory pipe): the client writes a request the peer does not read; `Close` ends the write with `ErrClosed` and `cotp.ErrClosed`, and the peer then reads the end of the stream and no octet of the request, so no partial PDU is followed by another | `TestCloseDuringAWriteTheClientDoesNotTake`: `Close` ends a write that nobody reads. `TestIdleTimeoutCoversUnreadAnswers` covers `IdleTimeout` |
| L5 Close during handshake | `TestCloseEndsAConnectInFlight` (a first `Connect`, and one replacing a session): `Close` returns while the setup answer is held, `Connect` returns an error that matches `ErrClosed`, every connection dialled is closed and no session is left | `TestCloseDuringTheHandshake`: `Close` ends a connection that is still in its COTP handshake, without waiting for `HandshakeTimeout` |
| L6 Cancel a pending request | `TestCancelAfterRequestSentEndsTheSession` (read, write, bit read, user data, upload): the call returns `context.Canceled`. The documented outcome is that the session ends ([ERRORS.md](ERRORS.md#context-reconnect-and-concurrency)): the next call returns `ErrNotConnected` and sends nothing. `TestContextEndedBeforeRequestKeepsTheSession`: a context that had already ended sends nothing, and the next call is answered on the same session | n/a: the server sends no requests |
| L7 Response after the request timed out | `TestLateAnswerEndsTheSession`: the session ended with the timeout, so the late answer reaches a closed connection and completes no request; the next call returns `ErrNotConnected`, never `PDURefMismatchError`, and after `Connect` the client reads again. `TestProbeReadableRangesStopsAskingAfterATimeout` shows the same for a range scan. Late answers are not counted: the client has no metrics surface ([OBSERVABILITY.md](OBSERVABILITY.md)) | n/a |
| L8 Response after reconnect | n/a as a loop: there is no automatic reconnect. A repeated `Connect` opens a new connection and closes the old one (`TestConnect_FailedReconnectKeepsSession`, e2e `TestReconnectAfterConnectionLoss`), so an answer on the old connection cannot reach the new session | n/a |
| L9 Callback calls a public method | `TestCallbacksMayReadConnectionInfo`: a `Logger` and a `Dialer` call `ConnectionInfo` and `PDUSize` while the call that runs them holds the client's turn, and get the session that is in place. An operation or `Connect` called from a callback waits for that call and returns when its own context ends (`TestCallWaitingForItsTurnEndsWithItsContext` shows that wait for every operation). `TestCallbackUnwindingReachesTheCaller`: a `Dialer` or a `Logger` that does not return normally unwinds to the caller of `Connect`, and the next call is answered on the session in place | n/a: the server has no callbacks (`Config.Logger` is a `*slog.Logger`) |
| L10 Callback calls Close | `TestCallbacksMayCloseTheClient`: `Close` called from a `Logger` (in the cleanup of `UploadBlock`, and on a failed `Connect`) and from a `Dialer` returns nil, and so does the call that runs the callback: `UploadBlock` with its block, `Connect` with its own failure, or with `ErrClosed` when `Close` came from its `Dialer`. Every connection is closed and the client is disconnected | n/a |
| L11 Slow callback | n/a: no event or report callbacks; a `Logger` is synchronous by its documented contract | n/a |
| L12 Queue at capacity | n/a: no queue | `TestConnectionLimitRefusesExtraClients` (`server/server_test.go`): a connection past `MaxConnections` is closed on accept and logged; there is no counter |
| L13 Concurrent sessions | `TestReadAreaConcurrent`, e2e `TestConcurrentUseOfOneClient`: one client, its calls serialised, each gets its own answer | `TestConcurrentClientsAndReconnect` (`server/server_test.go`): eight clients |
| L14 Failure during construction | `TestConnectOnceFailureClearsConnection`, `TestConnect_SetupFailures`, `TestSetupAnswerMessageType`, `TestWithDialerConnAndErrorClosesConn`, e2e `TestDialerTransportFaultsDuringConnect`: a failed `Connect` leaves no session and no open connection; goleak | `TestConfigValidation`, `TestListenAndServeBadAddress`: nothing is started |
| L15 Shutdown(ctx) | n/a: no `Shutdown` | n/a: no `Shutdown`; `Serve` ends with its context (`TestServeEndsWithTheContext`) |
| L16 Close racing reconnect | n/a: no reconnect loop; `Close` against `Connect` is L5 | n/a |

How these tests wait: the client tests run against a scripted peer on loopback and agree with it through channels (a request that arrived, an answer that is held back, a call that returned); a deadline is a context the test expires by hand, except in the range scan test, where the probe timeout of 200 ms is what is tested. The client tests marked synctest, the request timeout tests (`client/request_timeout_test.go`: each kind of request ends after exactly the request timeout, the earlier of it and the call's deadline wins, the bound is per request and not per call, it does not bound `Connect`) and the server tests run in a `testing/synctest` bubble over in-memory connections, where `synctest.Wait` returns once every goroutine is blocked and a duration on the bubble's clock is exact. No test sleeps to let something happen, and every wait is bounded. `TestCloseWaitsForAConnectionNotYetServed` uses the one test seam of the module, the unexported `Server.afterAddConn`, to stop `Serve` between registering a connection and starting its goroutine.

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

With managed servers and no Docker, the suite skips (`interop/harness.go`), unless `SNAP_INTEROP_REQUIRE_DOCKER=1`: then it fails. `make interop` and the Interop workflow set that variable, so neither can pass without running the matrix; a plain `go test -tags=interop ./interop/...` still skips.

## Coverage policy

`make coverage` and the go-ci coverage job measure `./...` with `-covermode=atomic`; no package is excluded in this repository's configuration. The minimum is `coverage-min` in [.github/workflows/ci.yml](.github/workflows/ci.yml), mirrored by `COVER_MIN` in the [Makefile](Makefile). It is 90, the profile floor. Each package also has to reach `COVER_PKG_MIN` (90), checked by `make coverage-pkg-check`. Raise them as tests are added; never lower them.

## Fuzzing

Fuzz targets live in `wire/`, `client/`, `model/codec/`, `server/` and `internal/capture/`, next to the decoders and encoders. Encoder targets check that every input is either refused with `ErrOutOfRange` or encoded with exactly its fields and consistent lengths. `make fuzz` discovers them; do not list them by name. Every new decoder of untrusted input gets a target.

A decoder target asserts what an accepted input means, not only that decoding returns: the result holds the fields of the input; every slice it returns is a view of the input, inside it and without capacity beyond its length; where an encoder exists, the result encodes to an answer that decodes to the same result; the input cut by one octet is refused; and a refusal returns nothing. The targets take seeds from the recorded traffic in `testdata/captures`. In `client`, one target feeds SZL list data to the client's record decoders, and one runs the calls that collect a user data answer against an in-memory peer that grants any PDU size and repeats a fuzzed packet: the result of a call stays within the limits of [SECURITY.md](SECURITY.md#limits-on-what-a-peer-can-send).

## Limits and ownership

Each limit of [SECURITY.md](SECURITY.md#limits-on-what-a-peer-can-send) has a test in `client/answer_limits_test.go` that sends one answer at the limit and one past it; where the limit bounds memory, the test also measures the bytes the call allocates (`runtime.MemStats.TotalAlloc` around the call, which includes the scripted peer in the same process) against a budget. `wire/ownership_test.go` pins that the decoders return borrowed views without spare capacity; `client` `TestResultsAreTheCallers` and `server` `TestMemoryIsNotSharedWithTheCaller` pin that results and registered memory are copies.

Regression procedure: when a fuzz run fails, Go writes the input to `testdata/fuzz/<FuzzName>/` in the package. Commit that file with the fix, so `go test` replays it as a seed from then on.

## Not tested here

- No test against a live PLC: the captures are recorded traffic, and only the requests in them are covered ([LIMITATIONS.md](LIMITATIONS.md#verification)).

## When CI runs

| Event | Workflow | What runs |
|---|---|---|
| Pull request | `pr.yml` | `docs.yml` (docsync, otfabric-policy) always; `ci.yml`, `interop.yml` unless every changed file is documentation; then `OT Fabric Required Checks` |
| Push to a branch | none | nothing; dispatch `ci.yml`, `interop.yml` to test a branch without a pull request |
| Push to `main` | `docs.yml` | the light checks; the merged commit was tested as the pull request's merge commit |
| Schedule | `extended.yml` | the scheduled runs, on `main` |
| Release | `release.yml` | the release workflow tests the release commit again |

Documentation means an added or modified `*.md` file outside `.github/`, `testdata/` and `vendor/`, other than `AGENTS.md` and `CLAUDE.md`. A deleted or renamed document, or any other file, runs everything. `OT Fabric Required Checks` fails unless every job that applies succeeded; its summary lists them.
