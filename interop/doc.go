//go:build interop

// Package interop runs black-box tests of go-s7comm against the published
// snap7-interop SNAP7 servers (native Snap7 and pure-Python).
//
// Build with -tags=interop. make check typechecks this package with that tag;
// running the matrix (make interop) requires Docker by default (managed containers).
// See README.md § Interop tests. Editor/gopls: use buildFlags -tags=interop
// (see .vscode/settings.json) so symbols resolve outside make interop.
package interop
