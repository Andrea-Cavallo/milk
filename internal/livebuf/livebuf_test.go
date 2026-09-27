package livebuf

import (
	"strings"
	"sync"
	"testing"
)

func TestAppendAndSnapshot(t *testing.T) {
	b := New(1024)
	b.Append([]byte("hello "))
	b.Append([]byte("world"))
	if got := b.Snapshot(); got != "hello world" {
		t.Fatalf("Snapshot() = %q, want %q", got, "hello world")
	}
	if got := b.Len(); got != len("hello world") {
		t.Fatalf("Len() = %d, want %d", got, len("hello world"))
	}
}

func TestWriterAppends(t *testing.T) {
	b := New(1024)
	w := b.Writer()
	n, err := w.Write([]byte("chunk"))
	if err != nil || n != len("chunk") {
		t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, len("chunk"))
	}
	if got := b.Snapshot(); got != "chunk" {
		t.Fatalf("Snapshot() = %q, want %q", got, "chunk")
	}
}

func TestCapTrimsOldest(t *testing.T) {
	b := New(5)
	b.Append([]byte("abc"))
	b.Append([]byte("de"))  // buffer now exactly "abcde" (5 bytes)
	b.Append([]byte("fgh")) // pushes buffer to 8 bytes, over cap by 3 -> drop oldest 3
	if got := b.Snapshot(); got != "defgh" {
		t.Fatalf("Snapshot() = %q, want %q", got, "defgh")
	}
	if got := b.Len(); got != 5 {
		t.Fatalf("Len() = %d, want 5", got)
	}
}

func TestCapTrimsSingleOversizedAppend(t *testing.T) {
	b := New(5)
	b.Append([]byte("abcdefghij")) // one write, already 10 bytes, over cap by 5
	if got := b.Snapshot(); got != "fghij" {
		t.Fatalf("Snapshot() = %q, want %q", got, "fghij")
	}
	if got := b.Len(); got != 5 {
		t.Fatalf("Len() = %d, want 5", got)
	}
}

func TestNewDefaultsNonPositiveCap(t *testing.T) {
	b := New(0)
	if b.max != DefaultMaxBytes {
		t.Fatalf("max = %d, want DefaultMaxBytes (%d)", b.max, DefaultMaxBytes)
	}
	b = New(-5)
	if b.max != DefaultMaxBytes {
		t.Fatalf("max = %d, want DefaultMaxBytes (%d)", b.max, DefaultMaxBytes)
	}
}

// TestConcurrentAppendAndSnapshot exercises one producer goroutine appending
// while a reader goroutine repeatedly snapshots, matching the
// producer-vs-attach-viewer shape this buffer exists for. Run with -race.
func TestConcurrentAppendAndSnapshot(t *testing.T) {
	b := New(64 * 1024)
	const writes = 2000
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range writes {
			b.Append([]byte("x"))
		}
		close(done)
	}()
	go func() {
		defer wg.Done()
		for {
			_ = b.Snapshot()
			_ = b.Len()
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	wg.Wait()

	got := b.Snapshot()
	if len(got) != writes {
		t.Fatalf("Snapshot() length = %d, want %d", len(got), writes)
	}
	if strings.Trim(got, "x") != "" {
		t.Fatalf("Snapshot() = %q, want all %q", got, "x")
	}
}
