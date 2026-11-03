// SPDX-License-Identifier: MIT

package wire

// must returns v, or panics on err: for encoder calls with valid test input.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
