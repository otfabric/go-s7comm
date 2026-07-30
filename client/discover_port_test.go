// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestIsPortOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port
	if err := isPortOpen(context.Background(), "127.0.0.1", port, 2*time.Second); err != nil {
		t.Fatalf("expected port %s to be open: %v", strconv.Itoa(port), err)
	}

	// Unlikely-to-be-open port: expect dial to fail (connection refused or timeout)
	if err := isPortOpen(context.Background(), "127.0.0.1", 35555, 50*time.Millisecond); err == nil {
		t.Error("expected error when no listener on port")
	}
}
