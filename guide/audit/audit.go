// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package audit appends one JSON line per event to the live system's audit log.
//
// The log records WHO did WHAT through WHICH channel (console or issue tag), which step
// changed state and which guide sections an answer cited. It never records a token, a
// command's output or the text of a private bundle — only its section references.
package audit

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one log line.
type Event struct {
	Time    time.Time         `json:"time"`
	Channel string            `json:"channel"` // console | tag
	Actor   string            `json:"actor"`
	Action  string            `json:"action"`
	Step    string            `json:"step,omitempty"`
	State   string            `json:"state,omitempty"`
	Cites   []string          `json:"cites,omitempty"`
	Detail  string            `json:"detail,omitempty"`
	Extra   map[string]string `json:"extra,omitempty"`
}

// Log is safe for concurrent use.
type Log struct {
	mu  sync.Mutex
	w   io.Writer
	c   io.Closer
	now func() time.Time
}

// Open appends to path (0600, parent 0700).
func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Log{w: f, c: f, now: time.Now}, nil
}

// To logs to w (tests, or stderr when no file can be opened).
func To(w io.Writer) *Log { return &Log{w: w, now: time.Now} }

// Record writes e. Errors are swallowed: an unwritable log must not stop an install,
// and the console already shows everything the log would have held.
func (l *Log) Record(e Event) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = l.now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = l.w.Write(append(b, '\n'))
}

// Close closes the underlying file.
func (l *Log) Close() error {
	if l == nil || l.c == nil {
		return nil
	}
	return l.c.Close()
}
