package local

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/scoutme/milk/internal/session"
)

// TestManager_Spawn_WiresLiveBufferWriter verifies the out io.Writer a JobRun
// receives really is the job's own Job.Live buffer (see ADR-0047): writes
// made during execution must be visible via Live.Snapshot() once the job
// finishes, and via the same Job value returned by Manager.Jobs().
func TestManager_Spawn_WiresLiveBufferWriter(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	done := make(chan *Job, 1)
	mgr.SetOnDone(func(j *Job) { done <- j })

	job := mgr.Spawn("job", "t", "primary", "m", func(_ context.Context, _ string, out io.Writer) (string, session.TokenUsage, error) {
		if out == nil {
			return "", session.TokenUsage{}, fmt.Errorf("JobRun received a nil out writer")
		}
		fmt.Fprint(out, "step one\n")
		fmt.Fprint(out, "step two\n")
		return "ok", session.TokenUsage{}, nil
	})

	if job.Live == nil {
		t.Fatal("Spawn returned a Job with a nil Live buffer")
	}

	finished := <-done
	if finished.ID != job.ID {
		t.Fatalf("done for %s, want %s", finished.ID, job.ID)
	}

	got := job.Live.Snapshot()
	if !strings.Contains(got, "step one") || !strings.Contains(got, "step two") {
		t.Fatalf("Live.Snapshot() = %q, want both step markers", got)
	}

	// The same content must also be reachable through the copy Jobs() hands
	// out for display — Live is the one field on that copy meant to still
	// point at the live buffer (see Job.Live's doc comment).
	jobs := mgr.Jobs()
	if len(jobs) != 1 || jobs[0].Live == nil || jobs[0].Live.Snapshot() != got {
		t.Fatalf("Jobs()[0].Live.Snapshot() did not match the job's own buffer: %+v", jobs)
	}
}

// TestJob_LiveBuffer_ConcurrentAppendAndSnapshot exercises the real hazard
// Job.Live exists to survive: one goroutine (the job) appending while another
// (an attached TUI, polling for a live view) reads Snapshot concurrently. Run
// with -race. Uses a small cap to also confirm the buffer still trims
// correctly under concurrent access, not just single-threaded (see
// livebuf_test.go for the single-threaded trim cases).
func TestJob_LiveBuffer_ConcurrentAppendAndSnapshot(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	done := make(chan *Job, 1)
	mgr.SetOnDone(func(j *Job) { done <- j })

	const writes = 500
	job := mgr.Spawn("job", "t", "primary", "m", func(_ context.Context, _ string, out io.Writer) (string, session.TokenUsage, error) {
		for range writes {
			fmt.Fprint(out, "x")
		}
		return "ok", session.TokenUsage{}, nil
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			_ = job.Live.Snapshot()
			select {
			case <-stop:
				return
			default:
			}
		}
	})

	<-done
	close(stop)
	wg.Wait()

	got := job.Live.Snapshot()
	if len(got) != writes {
		t.Fatalf("Live.Snapshot() length = %d, want %d", len(got), writes)
	}
	if strings.Trim(got, "x") != "" {
		t.Fatalf("Live.Snapshot() = %q, want all %q", got, "x")
	}
}
