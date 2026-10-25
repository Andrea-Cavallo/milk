//go:build unix

package local

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Pure-read runs still execute concurrently (the barrier model only
// serialises side-effecting calls). Two read_file calls on FIFOs: the writer
// fills B first and only then A — if the reads ran sequentially (A first),
// reader-A and writer-B would deadlock until the tool timeout, so both a
// timeout-sized elapsed time and a missing body mean over-serialisation.
//
// Unix-only by build tag, not a runtime skip: syscall.Mkfifo does not exist
// on Windows, so this test must not even compile there (it broke
// cross-platform `go vet ./...`, see the CI windows job).
func TestBatch_ReadRunStillConcurrent(t *testing.T) {
	dir := t.TempDir()
	fa := filepath.Join(dir, "a.fifo")
	fb := filepath.Join(dir, "b.fifo")
	if err := syscall.Mkfifo(fa, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if err := syscall.Mkfifo(fb, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	go func() {
		wb, err := os.OpenFile(fb, os.O_WRONLY, 0o600) // blocks until a reader opens
		if err != nil {
			return
		}
		wb.WriteString("bbb") //nolint:errcheck
		wb.Close()
		wa, err := os.OpenFile(fa, os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		wa.WriteString("aaa") //nolint:errcheck
		wa.Close()
	}()

	a := &Agent{skipPerms: true, toolTimeout: 2 * time.Second}
	tcs := []toolCall{
		{ID: "ra", Function: toolCallFunction{Name: "read_file", Arguments: `{"path":"` + fa + `"}`}},
		{ID: "rb", Function: toolCallFunction{Name: "read_file", Arguments: `{"path":"` + fb + `"}`}},
	}
	start := time.Now()
	msgs, err := a.executeToolCalls(context.Background(), nil, tcs, "", "", io.Discard, nil, nil, "", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeToolCalls: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("read run looks sequential (elapsed %v) — pure reads must overlap", elapsed)
	}
	res := toolResultsByCall(msgs)
	if !strings.Contains(res["ra"], "aaa") || !strings.Contains(res["rb"], "bbb") {
		t.Errorf("both reads must complete: ra=%q rb=%q", res["ra"], res["rb"])
	}
}
