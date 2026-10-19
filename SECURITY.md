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

### Scanning and Probing

`Discover`, `ProbeRackSlots`, `ProbeReadableRanges` and `CompareRead` open many connections or issue many requests. Only use them on systems you are authorised to scan.

- `Discover` is IPv4 only and rejects ranges larger than `/12`; prefer `/24` or smaller in OT networks. Its defaults are 10 parallel hosts and a 2 s timeout; `WithDiscoverSafetyMode(client.SafetyConservative)` lowers that to 3 in parallel with a 50 ms delay between rack/slot attempts (`WithDiscoverRateLimit` sets that delay directly)
- `WithRateLimit` on a client adds a delay between the chunks of a read that spans several PDUs

### Timeouts

- `WithTimeout` bounds `Connect` (dial and handshake) only. Reads, writes and uploads are bounded by the context you pass; use `context.WithTimeout` or `WithDeadline`, or an unresponsive peer can block a call indefinitely

### Input Handling

- Received PDUs are parsed by the `wire` package, which checks lengths before use. The S7 header, read-var, SZL and upload response parsers and the TPDU inspector are fuzzed
