// SPDX-License-Identifier: MIT

// Package s7comm is the module root for the Siemens S7 communication library.
//
// Applications import the subpackages, not this package:
//
//   - [github.com/otfabric/go-s7comm/client] — Connect, read/write, discover, probe, SZL, blocks
//   - [github.com/otfabric/go-s7comm/model] — memory areas, addresses, device and connection info
//   - [github.com/otfabric/go-s7comm/wire] — S7 PDU encode/decode and TSAP helpers
//
// Transport uses [github.com/otfabric/go-cotp] (TP0 over TPKT). This module does not
// own TPKT framing.
//
// Docs: README.md, API.md (public API), ERRORS.md (ReadResult / sentinels),
// OBSERVABILITY.md (logging), RELEASE.md (changelog).
package s7comm
