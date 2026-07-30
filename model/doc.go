// SPDX-License-Identifier: MIT

// Package model defines shared S7 data types used by client and wire.
//
// It covers memory [Area] codes and [Address] values, [DeviceInfo] /
// [ConnectionInfo], CPU and protection enums, block metadata ([BlockInfo],
// [BlockData]), and thin wrappers over [github.com/otfabric/go-s7comm/model/codec]
// for big-endian BOOL/BYTE/WORD/INT/DWORD/DINT/REAL/STRING conversion.
//
// Types here are transport-agnostic value models; protocol I/O lives in client,
// framing in wire.
package model
