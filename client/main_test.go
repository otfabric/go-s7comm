// SPDX-License-Identifier: MIT

package client

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test ends with a goroutine still running:
// Discover, CompareRead and ProbeRackSlots all start workers (TEST-006).
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
