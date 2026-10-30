# AGENTS.md — go-s7comm

## What this is

Pure Go client for the classic Siemens S7 communication protocol on [go-cotp](https://github.com/otfabric/go-cotp): read/write, discovery, rack/slot probing, SZL diagnostics and block upload. Consumed by [s7commctl](https://github.com/otfabric/s7commctl). Profile: **protocol-library**. Nothing is certified; see LIMITATIONS.md.

## Organisation standards

This repository follows **otfabric/engineering policy 1.0**, profile **protocol-library** (`.otfabric/policy.yaml`, mode `baseline`). Organisation skills: `otfabric` Claude Code plugin. Where this file and the organisation standards disagree, this file wins here — say so in your report.

## Where the truth lives

| Question | Source |
|---|---|
| Public API | API.md |
| Errors, `ReadResult` statuses, context and reconnect semantics | ERRORS.md |
| Scope not implemented, verification | LIMITATIONS.md |
| Logging | OBSERVABILITY.md |
| Release history | RELEASE.md |

## Hard rules

1. Never lower the go-ci `coverage-min` (88 today, below the profile floor of 90: raise it as tests are added), add lint suppressions, or skip/delete tests to make CI pass.
2. Never invent protocol semantics or claim support beyond recorded evidence.
3. TPKT belongs to go-cotp: this module does not import go-tpkt directly.
4. Exported API changes are breaking for s7commctl: this library stays on v0 or v1.
5. Writes and scans act on real equipment: keep their defaults conservative (SECURITY.md).
6. No new runtime dependency without an ADR.

## Commands

| When you change… | Run |
|---|---|
| anything | `make check` |
| interop-relevant behaviour | `make interop` (Docker; images pinned in the Makefile) |

## Definition of Done

`make check` green; tests for new behaviour including error paths; fuzz target for any new decoder; docs updated in the same change; RELEASE.md section for user-visible changes.

## Git and releases

Branches and PRs. Do not push to `main`, tag or run the Release workflow unless the maintainer asks in the session.
