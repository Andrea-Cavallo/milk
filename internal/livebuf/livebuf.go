// Package livebuf provides a thread-safe, byte-capped buffer for streaming
// text that must stay off the main session transcript — background job and
// workflow tool/reasoning output the TUI can attach to on demand (see
// ADR-0047). Content here is in-memory only: never persisted, never fed back
// into a prompt or the session transcript.
package livebuf

import (
	"io"
	"sync"
)

// DefaultMaxBytes is used by New when maxBytes <= 0.
const DefaultMaxBytes = 256 * 1024

// Buffer accumulates streamed text up to a byte cap, dropping the oldest
// content once over the cap. Safe for one producer goroutine calling Append
// (or writing via Writer) concurrently with any number of reader goroutines
// calling Snapshot/Len.
type Buffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

// New creates a Buffer capped at maxBytes. maxBytes <= 0 uses DefaultMaxBytes.
func New(maxBytes int) *Buffer {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Buffer{max: maxBytes}
}

// Append adds p to the buffer, trimming the oldest bytes if the cap is
// exceeded.
func (b *Buffer) Append(p []byte) {
	if len(p) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = b.buf[over:]
	}
}

// Writer returns an io.Writer that appends every Write to the buffer.
func (b *Buffer) Writer() io.Writer { return &writer{b: b} }

// Snapshot returns a copy of the currently retained text.
func (b *Buffer) Snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// Len returns the number of bytes currently retained.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}

type writer struct{ b *Buffer }

func (w *writer) Write(p []byte) (int, error) {
	w.b.Append(p)
	return len(p), nil
}
