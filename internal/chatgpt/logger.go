package chatgpt

import (
	"io"
	"log/slog"
	"sync/atomic"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

var logger atomic.Pointer[slog.Logger]

// SetLogger names where this package writes down a sign-in, refresh or sign-out. The app
// hands it a logger at startup; until it does, and when it hands nil, the
// package discards. Safe to call while requests are in flight.
func SetLogger(l *slog.Logger) {
	if l == nil {
		l = discard
	}
	logger.Store(l)
}

// log is the logger in force, never nil.
func log() *slog.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	return discard
}
