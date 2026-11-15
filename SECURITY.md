# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `go-s7comm`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/go-s7comm/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue, ideally a minimal program or test
- The affected version(s)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### S7comm Has No Built-In Security

Classic S7comm runs over ISO-on-TCP (port 102) with no authentication and no encryption: anyone who can reach a PLC can read and write its memory, and anyone on the path can read or alter the traffic. go-s7comm implements this classic protocol only; it has no TLS and does not send passwords.

- **Keep S7 traffic on isolated, trusted networks**; never expose port 102 to the Internet. Use a VPN or secured gateway across untrusted networks
- **Writes act on a running process**: `WriteArea`, `WriteDB`, `WriteBit` and `WriteDBBit` change PLC memory immediately. Validate addresses and values in your application, and test against a simulator rather than production equipment
- `UploadBlock` and `ReadDiagBuffer` return program code and diagnostic history; treat the results as sensitive

### The Test Server

Package `server` lets anyone who reaches its port read and write all of its memory, like the protocol itself. It is for tests: bind it to loopback or an isolated test network. `MaxConnections` (default 32), `HandshakeTimeout` (5 s) and `IdleTimeout` (off by default) bound what one peer can hold; `IdleTimeout` also closes a connection that does not take its answers; set `IdleTimeout` on a shared network. Its request decoders and handler are fuzzed.

### Scanning and Probing

`Discover`, `ProbeRackSlots`, `ProbeReadableRanges` and `CompareRead` open many connections or issue many requests. Only use them on systems you are authorised to scan.

- `Discover` is IPv4 only and rejects ranges larger than `/12`; prefer `/24` or smaller in OT networks. Its defaults are 10 parallel hosts and a 2 s timeout; `WithDiscoverSafetyMode(client.SafetyConservative)` lowers that to 3 in parallel with a 50 ms delay between rack/slot attempts (`WithDiscoverRateLimit` sets that delay directly)
- `WithRateLimit` on a client adds a delay between the chunks of a read that spans several PDUs

### Restricting Destinations

`WithDialer` sets the function that opens the TCP connection of `Connect`. An application that lets users reach only approved hosts should enforce the allowlist there, for example in a `net.Dialer.Control` hook, which sees the resolved IP right before connecting; checking the host name before `Connect` leaves a DNS-rebinding gap. The scan helpers take their own dialer: `WithDiscoverDialer` for `Discover`, and the `Dialer` field of `RackSlotProbeRequest` and `CompareReadRequest`. Set the same allowlist on each one you expose; with a nil dialer they dial any target directly.

### Timeouts

- `WithTimeout` bounds `Connect` (dial and handshake) only. Reads, writes and uploads are bounded by the context you pass; use `context.WithTimeout` or `WithDeadline`, or an unresponsive peer can block a call indefinitely. The scan helpers bound every step by their own timeout, including the read of `CompareRead` and each probe of `ProbeReadableRanges` (`RangeProbeRequest.Timeout`, default 5 s)

### Input Handling

- Received PDUs are parsed by the `wire` package, which checks lengths before use. The S7 header, read-var, SZL and upload response parsers, the TPDU inspector and the server-side request decoders are fuzzed, and so are the client's SZL record decoders and its collection of user data answers ([TESTING.md](TESTING.md#fuzzing))
- The decoders of `wire` return slices borrowed from their input, without capacity beyond their length. What the client returns (`ReadResult.Data`, `BlockData.Data`, SZL data, block lists) is a copy that belongs to the caller

### Limits on what a peer can send

What one call of the client accepts from its peer, where the limit is enforced, and the test that asserts it. An answer or a list past a limit is refused with an error that matches `ErrProtocolFailure`.

| Limit | Value | Enforced in | Test |
|---|---|---|---|
| Transport message (TSDU) | 65,535 octets, the largest S7 PDU | go-cotp `MaxTSDULength`, set in `connectCOTP` | `client` `TestConnectRefusesOversizedMessage` |
| Negotiated PDU size | at most the proposed size (`WithMaxPDU`, default 480; a rack/slot probe proposes 480) | `performS7Setup` | `TestNegotiatedPDUSizeIsAtMostTheProposal`, `TestProbeRackSlotsPDUSizeIsAtMostTheProposal` |
| Length of one answer | the negotiated PDU size; the proposed size when the setup answer grants 0 | `sendReceive`, `runFollowUp` | `TestAnswerLengthIsBoundedByNegotiatedPDUSize`, `TestAnswerLengthIsBoundedByProposalWithoutNegotiatedSize` |
| Packets of one user data answer (SZL list, block list, block counts) | 257: the first and 256 follow-ups | `userdata` | `TestUserdata_FollowUpFailures` (allocation budget), `TestUserdataOversizedPacketsAllocationBudget`, `FuzzUserdataAnswers` |
| Entries of one block list | 65,536, the number of 16-bit block numbers | `ListBlocks` | `TestListBlocksEntryLimit` |
| Block types of one block count answer | the map is sized for at most 256, the number of one-byte type codes | `BlockCounts` | `TestBlockCountMapSizeAndAllocationBudget` |
| Segments of one upload | 4,096 | `UploadBlock` | `TestUploadBlockSegmentLimit` |
| Bytes of one upload | 8 MiB | `UploadBlock` | `TestUploadBlockByteLimitAllocationBudget` |
| Bytes of one read item | the bytes that were asked for | `ReadArea` | `TestReadAreaOverlongItem` |
| Load memory length and security field of a block header | 2,147,483,647, so that both fit an `int` on every platform | `wire.ParseBlockInfoResponse` | `wire` `TestParseBlockInfo32BitFields` |

With the default proposal of 480, one user data answer is at most 257 × 480 octets (123 kB) and one upload at most 4,096 segments of 462 bytes (1.9 MB). `WithMaxPDU` with a larger value raises both, up to the limits of the table, which hold at any PDU size. `ListAllBlocks` calls `ListBlocks` for seven block types and keeps every list.

A caller's own request is bounded too: `ProbeReadableRanges` takes a range inside the S7 byte address space only (2,097,152 addresses) and returns `*ValidationError` for any other, before it builds an offset (`TestProbeReadableRanges_RangeBounds`).
