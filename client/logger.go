// SPDX-License-Identifier: MIT

package client

import (
	"fmt"
	"log/slog"
)

// NewSlogLogger wraps a slog.Handler so it satisfies Logger.
// Messages use printf-style formatting (fmt.Sprintf) to match Logger call sites.
// A nil handler returns a no-op logger.
func NewSlogLogger(h slog.Handler) Logger {
	if h == nil {
		return nopLogger{}
	}
	return &slogLogger{sl: slog.New(h)}
}

type slogLogger struct{ sl *slog.Logger }

func (l *slogLogger) Debug(msg string, args ...any) {
	l.sl.Debug(fmt.Sprintf(msg, args...))
}

func (l *slogLogger) Info(msg string, args ...any) {
	l.sl.Info(fmt.Sprintf(msg, args...))
}

func (l *slogLogger) Error(msg string, args ...any) {
	l.sl.Error(fmt.Sprintf(msg, args...))
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}
