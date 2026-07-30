// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewSlogLogger(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := NewSlogLogger(h)
	l.Debug("dbg %s", "x")
	l.Info("info %d", 1)
	l.Error("err %v", "boom")
	out := buf.String()
	for _, want := range []string{"dbg x", "info 1", "err boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestNewSlogLogger_Nil(t *testing.T) {
	l := NewSlogLogger(nil)
	if l == nil {
		t.Fatal("nil handler should return non-nil no-op logger")
	}
	l.Debug("x")
	l.Info("x")
	l.Error("x")
}
