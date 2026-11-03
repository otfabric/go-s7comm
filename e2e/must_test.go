// SPDX-License-Identifier: MIT

package e2e

// must returns v, or panics on err: for encoder calls with valid test input.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
