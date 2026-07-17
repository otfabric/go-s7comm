//go:build interop

// Package interop runs black-box tests of go-s7comm against the published
// snap7-interop SNAP7 servers (native Snap7 and pure-Python).
//
// Build with -tags=interop. Requires Docker by default (managed containers).
// See README.md § Interop tests.
package interop
