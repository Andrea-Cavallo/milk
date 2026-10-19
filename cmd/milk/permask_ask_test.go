package main

import (
	"context"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/oversight"
)

// Issue #249 question-race contract (askUserQuestion): the three-source
// shape of ADR-0052's ask layer with a question instead of a permission —
// first real answer wins, silence is never an answer, and only the timed
// answer may resolve an unanswered background question.

func TestAskUserQuestionNoSurfaceResolvesImmediately(t *testing.T) {
	answer, ok, src := askUserQuestion(context.Background(), oversight.Noop{},
		oversight.InputRequest{Question: "q"}, nil, askPolicy{})
	if ok || answer != "" || src != permSrcNoSurface {
		t.Fatalf("answer=%q ok=%v src=%q — want immediate no-surface resolution", answer, ok, src)
	}
}

func TestAskUserQuestionDirectWins(t *testing.T) {
	fn := &fakeNotifier{inputCh: make(chan string, 1)}
	answer, ok, src := askUserQuestion(context.Background(), fn,
		oversight.InputRequest{Question: "q"},
		func(context.Context) (string, bool) { return "blue", true },
		askPolicy{})
	if !ok || answer != "blue" || src != permSrcDirect {
		t.Fatalf("answer=%q ok=%v src=%q", answer, ok, src)
	}
}

func TestAskUserQuestionRemoteWins(t *testing.T) {
	fn := &fakeNotifier{inputCh: make(chan string, 1)}
	fn.inputCh <- "from telegram"
	answer, ok, src := askUserQuestion(context.Background(), fn,
		oversight.InputRequest{Question: "q"},
		func(ctx context.Context) (string, bool) { <-ctx.Done(); return "", false },
		askPolicy{})
	if !ok || answer != "from telegram" || src != permSrcRemote {
		t.Fatalf("answer=%q ok=%v src=%q", answer, ok, src)
	}
}

func TestAskUserQuestionDeadlineResolvesUnanswered(t *testing.T) {
	fn := &fakeNotifier{inputCh: make(chan string, 1)} // never answered
	start := time.Now()
	answer, ok, src := askUserQuestion(context.Background(), fn,
		oversight.InputRequest{Question: "q"},
		func(ctx context.Context) (string, bool) { <-ctx.Done(); return "", false },
		askPolicy{deadline: 50 * time.Millisecond})
	if ok || answer != "" || src != permSrcDefault {
		t.Fatalf("answer=%q ok=%v src=%q — want timed no-answer", answer, ok, src)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("deadline did not bound the ask")
	}
}

func TestAskUserQuestionRemoteSilenceNeverResolves(t *testing.T) {
	// The remote never answers (its own timeout is silence); the direct
	// surface answers later — the question must resolve with the human's
	// answer, not with an early "nobody".
	fn := &fakeNotifier{inputCh: make(chan string, 1)}
	direct := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(direct)
	}()
	answer, ok, src := askUserQuestion(context.Background(), fn,
		oversight.InputRequest{Question: "q"},
		func(ctx context.Context) (string, bool) {
			select {
			case <-direct:
				return "late but real", true
			case <-ctx.Done():
				return "", false
			}
		},
		askPolicy{})
	if !ok || answer != "late but real" || src != permSrcDirect {
		t.Fatalf("answer=%q ok=%v src=%q", answer, ok, src)
	}
}

func TestAskUserQuestionDismissedSurfaceIsNoAnswer(t *testing.T) {
	answer, ok, src := askUserQuestion(context.Background(), oversight.Noop{},
		oversight.InputRequest{Question: "q"},
		func(context.Context) (string, bool) { return "", false },
		askPolicy{})
	if ok || answer != "" || src != permSrcDirect {
		t.Fatalf("answer=%q ok=%v src=%q — a dismissed dialog is nobody answering", answer, ok, src)
	}
}

func TestAskUserQuestionCancellationResolvesUnanswered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	answer, ok, src := askUserQuestion(ctx, &fakeNotifier{inputCh: make(chan string, 1)},
		oversight.InputRequest{Question: "q"},
		func(ctx context.Context) (string, bool) { <-ctx.Done(); return "", false },
		askPolicy{})
	if ok || answer != "" || src != permSrcDefault {
		t.Fatalf("answer=%q ok=%v src=%q", answer, ok, src)
	}
}
