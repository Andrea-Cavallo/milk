package local

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/scoutme/milk/internal/session"
)

// TestStreamCompletion_RetriesImageDropSilently is a regression test for
// backend flakiness observed live against xiaomimimo's mimo-v2.5: a
// well-formed image_url part sometimes gets silently ignored (no error, no
// image_tokens in the usage), and a byte-identical retry succeeds. The user
// must never see the resulting non-answer from a dropped attempt — only the
// eventually-accepted response.
func TestStreamCompletion_RetriesImageDropSilently(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		n := calls.Add(1)
		if n < 3 {
			// Backend silently dropped the image: no image_tokens in usage.
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"I don't see any image attached."},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0}}}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		// Third attempt: backend actually processes the image this time.
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"I see a red square."},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":300,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0,"image_tokens":192}}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})

	var out strings.Builder
	history, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("expected 3 attempts (2 dropped + 1 accepted), got %d", got)
	}
	if strings.Contains(out.String(), "don't see any image") {
		t.Errorf("a dropped attempt's non-answer leaked to the live output: %q", out.String())
	}
	if !strings.Contains(out.String(), "red square") {
		t.Errorf("expected the accepted attempt's answer in the live output, got: %q", out.String())
	}

	found := false
	for _, m := range history {
		if m.Role == "assistant" && strings.Contains(m.Content, "red square") {
			found = true
		}
		if m.Role == "assistant" && strings.Contains(m.Content, "don't see any image") {
			t.Errorf("a dropped attempt's non-answer leaked into session history: %+v", m)
		}
	}
	if !found {
		t.Errorf("expected the accepted attempt's answer in history, got: %+v", history)
	}
}

// TestStreamCompletion_NoRetryForTextOnlyRequests verifies the retry loop
// never engages for a request with no image — image_tokens is meaningless
// there, and retrying would just double the cost of every ordinary turn.
func TestStreamCompletion_NoRetryForTextOnlyRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":5}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	var out strings.Builder
	_, err := a.Run(context.Background(), nil, "hi", &out, &session.Session{CWD: "/tmp"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt for a text-only request, got %d", got)
	}
}

// TestStreamCompletion_NoRetryWithoutUsageDetails pins the retry trigger's
// positive-evidence rule: a backend that reports no prompt_tokens_details at
// all (llama.cpp, vLLM, most proxies — image accounting is simply unknown)
// must be asked exactly once. Replaying every image request three times
// against such servers bought nothing but 3x latency and cost.
func TestStreamCompletion_NoRetryWithoutUsageDetails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"a red square"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":300,"completion_tokens":10}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})

	var out strings.Builder
	if _, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt (no usage details = no drop evidence), got %d", got)
	}
	if !strings.Contains(out.String(), "red square") {
		t.Errorf("expected the response in live output, got: %q", out.String())
	}
	if strings.Contains(out.String(), "reported no image processing") {
		t.Errorf("unknown image accounting must not raise the drop notice, got: %q", out.String())
	}
}

// TestStreamCompletion_PersistentDropSurfacesNotice pins the user-visible
// consequence of the retry budget running out: the non-answer must not stand
// unexplained — the notice lands in the live output and the returned text
// (history), and the turn is charged exactly once.
func TestStreamCompletion_PersistentDropSurfacesNotice(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"I don't see any image attached."},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0}}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})
	var charges int
	a.onTokens = func(model, role string, prompt, completion, cacheRead, cacheCreation int64, timing StreamTiming) {
		charges++
	}

	var out strings.Builder
	history, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("expected the full retry budget (3 attempts), got %d", got)
	}
	if !strings.Contains(out.String(), "reported no image processing") {
		t.Errorf("expected the drop notice in live output, got: %q", out.String())
	}
	foundText := false
	for _, m := range history {
		if m.Role == "assistant" && strings.Contains(m.Content, "reported no image processing") {
			foundText = true
		}
	}
	if !foundText {
		t.Errorf("expected the drop notice in the returned assistant text, history: %+v", history)
	}
	if charges != 1 {
		t.Errorf("expected exactly 1 token charge (the returned attempt), got %d", charges)
	}
}

// TestStreamCompletion_ChargesOnlyTheReturnedAttempt pins the accounting fix:
// discarded image-drop retry attempts cost real tokens upstream but must not
// inflate milk's own totals — only the attempt whose response is returned is
// charged.
func TestStreamCompletion_ChargesOnlyTheReturnedAttempt(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n < 2 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"non-answer"},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0}}}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"a red square"},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":300,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0,"image_tokens":192}}}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})
	var charges int
	var chargedPrompt int64
	a.onTokens = func(model, role string, prompt, completion, cacheRead, cacheCreation int64, timing StreamTiming) {
		charges++
		chargedPrompt = prompt
	}

	var out strings.Builder
	if _, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if charges != 1 {
		t.Fatalf("expected exactly 1 token charge, got %d", charges)
	}
	if chargedPrompt != 300 {
		t.Errorf("expected the accepted attempt's prompt tokens charged (300), got %d", chargedPrompt)
	}
}

// TestStreamCompletion_LaterAttemptErrorFallsBack pins the buffered-response
// fallback: a retry attempt that errors (bad gateway, dropped connection) must
// not throw away the previous attempt's buffered response — it is at worst the
// same non-answer a persistent drop would have produced.
func TestStreamCompletion_LaterAttemptErrorFallsBack(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"first attempt answer"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0}}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})

	var out strings.Builder
	history, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 attempts (drop + errored retry), got %d", got)
	}
	if !strings.Contains(out.String(), "first attempt answer") {
		t.Errorf("expected the buffered first attempt to be returned, got: %q", out.String())
	}
	found := false
	for _, m := range history {
		if m.Role == "assistant" && strings.Contains(m.Content, "first attempt answer") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the buffered first attempt in history, got: %+v", history)
	}
}

// TestStreamCompletion_ImageRejectionRetriesWithoutImages pins the graceful
// degradation: an endpoint that rejects image input outright (a text-only
// model behind a mis-set "vision": true) must not fail the turn — the images
// are stripped, the turn is re-asked once, and a notice explains that the
// model did not see them.
func TestStreamCompletion_ImageRejectionRetriesWithoutImages(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "image_url") {
			http.Error(w, `{"error":{"message":"No endpoints found that support image input"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"the module is example.com/t"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":5}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})

	var out strings.Builder
	history, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "example.com/t") {
		t.Errorf("expected the stripped retry's answer in output, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "rejected image input") {
		t.Errorf("expected the image-rejection notice in output, got: %q", out.String())
	}
	found := false
	for _, m := range history {
		if m.Role == "assistant" && strings.Contains(m.Content, "rejected image input") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the image-rejection notice in returned text, history: %+v", history)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 attempts (rejected + stripped retry), got %d", got)
	}
}

// TestStreamCompletion_NonImageErrorsAreNotStripped pins the trigger's
// precision: an unrelated API error must surface as-is — no stripped retry,
// no notice — even on an image-bearing request.
func TestStreamCompletion_NonImageErrorsAreNotStripped(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})

	var out strings.Builder
	_, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil)
	if err == nil {
		t.Fatal("expected the API error to surface")
	}
	if strings.Contains(out.String(), "rejected image input") {
		t.Errorf("no notice expected for an unrelated error, got: %q", out.String())
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt (no stripped retry), got %d", got)
	}
}

// TestStripImageParts_FoldsTextBackIntoContent pins the message surgery the
// degradation performs: image parts go, text parts fold into Content, and a
// message left empty keeps a placeholder.
func TestStripImageParts_FoldsTextBackIntoContent(t *testing.T) {
	msgs := []Message{
		{Role: "user", ContentParts: []ContentPart{
			{Type: "text", Text: "what is in this image"},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc"}},
		}},
		{Role: "user", ContentParts: []ContentPart{
			{Type: "text", Text: `[image result from tool "screenshot"]`},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,def"}},
		}},
		{Role: "user", Content: "plain text", ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,ghi"}},
		}},
	}
	stripImageParts(msgs)

	if msgs[0].Content != "what is in this image" {
		t.Errorf("user prompt not folded into Content: %+v", msgs[0])
	}
	if msgs[1].Content != `[image result from tool "screenshot"]` {
		t.Errorf("tool-image caption not folded into Content: %+v", msgs[1])
	}
	if msgs[2].Content != "plain text" {
		t.Errorf("existing Content clobbered: %+v", msgs[2])
	}
	for i, m := range msgs {
		if len(m.ContentParts) != 0 {
			t.Errorf("msg %d still has content parts: %+v", i, m.ContentParts)
		}
	}
}

// ── non-chat-completions transports (ADR-0074 review) ───────────────────────

// TestConverse_MultipartMessagesCarryImages pins the Converse conversion:
// vision content parts must become image blocks (they were silently dropped
// before, and a Content-less tool-image message vanished entirely).
func TestConverse_MultipartMessagesCarryImages(t *testing.T) {
	msgs := []Message{
		{Role: "user", ContentParts: []ContentPart{
			{Type: "text", Text: "what is in this image"},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,aGVsbG8="}},
		}},
		{Role: "user", Content: "", ContentParts: []ContentPart{
			{Type: "text", Text: `[image result from tool "screenshot"]`},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/tiff;base64,aGVsbG8="}},
		}},
	}
	out, _ := convertMessagesToConverse(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 user messages, got %d: %+v", len(out), out)
	}
	if out[0].Content[1].Image == nil || out[0].Content[1].Image.Format != "png" || out[0].Content[1].Image.Source.Bytes != "aGVsbG8=" {
		t.Errorf("png image block missing/wrong: %+v", out[0].Content)
	}
	// tiff is not a Converse format — visible drop note instead of silence.
	if !strings.Contains(out[1].Content[1].Text, "dropped") {
		t.Errorf("expected a visible drop note for the unsupported format, got %+v", out[1].Content)
	}
}

// TestResponses_MultipartMessagesCarryImages pins the Responses conversion:
// input_image items with data URIs, and a visible note for formats the API
// won't take.
func TestResponses_MultipartMessagesCarryImages(t *testing.T) {
	msgs := []Message{
		{Role: "user", ContentParts: []ContentPart{
			{Type: "text", Text: "look"},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,aGVsbG8="}},
		}},
		{Role: "user", ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/tiff;base64,aGVsbG8="}},
		}},
	}
	items := messagesToResponses(msgs)
	parts0, ok := items[0].Content.([]responsesContentPart)
	if !ok || len(parts0) != 2 || parts0[1].Type != "input_image" {
		t.Fatalf("expected [input_text input_image], got %+v", items[0].Content)
	}
	parts1, ok := items[1].Content.([]responsesContentPart)
	if !ok || len(parts1) != 1 || !strings.Contains(parts1[0].Text, "dropped") {
		t.Fatalf("expected a visible drop note for the unsupported format, got %+v", items[1].Content)
	}
	// Plain string messages keep the plain string shape.
	if got := messagesToResponses([]Message{{Role: "user", Content: "hi"}})[0].Content; got != "hi" {
		t.Errorf("plain content shape regressed: %+v", got)
	}
}

// TestCloneForBackground_KeepsVision pins the background-clone field list:
// dropping supportsVision sent every background job's images down the
// "not configured for vision" branch regardless of configuration.
func TestCloneForBackground_KeepsVision(t *testing.T) {
	a := New("http://localhost:0", "test-model")
	a.supportsVision = true
	clone := a.cloneForBackground()
	if !clone.supportsVision {
		t.Error("clone lost supportsVision — background job images would be dropped")
	}
}

// TestMessagesContentBytes_CountsImageParts pins the compaction heuristic:
// image payloads must count toward the turn's content bytes.
func TestMessagesContentBytes_CountsImageParts(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "abc", ContentParts: []ContentPart{
			{Type: "text", Text: "abc"},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,aGVsbG8="}},
		}},
	}
	got := messagesContentBytes(msgs)
	want := len("abc") + len("abc") + len("data:image/png;base64,aGVsbG8=")
	if got != want {
		t.Errorf("messagesContentBytes = %d, want %d", got, want)
	}
}

// TestRunBackgroundTask_ForwardsAttachedImages pins the user-spawned-job
// image path: attachments staged when the job spawns ride its first message
// as multipart vision parts (they used to be silently ignored).
func TestRunBackgroundTask_ForwardsAttachedImages(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":1}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	ctx := WithImageParts(context.Background(), []ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,aGVsbG8="}},
	})
	var out strings.Builder
	if _, _, err := a.RunBackgroundTask(ctx, "job_test", nil, "/tmp", "what is in this image", "", &out, nil); err != nil {
		t.Fatalf("RunBackgroundTask: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 || !strings.Contains(bodies[0], "image_url") {
		t.Errorf("expected the attached image in the job's first request body (%d bodies)", len(bodies))
	}
}

// TestStreamCompletion_NoDropNoticeOnCachedFollowUpIteration pins the false
// positive seen live against mimo-v2.6: the turn's image is processed on the
// first request (image_tokens reported), but every later tool-loop iteration
// re-sends it from the backend's prompt cache and reports only cached_tokens —
// which must not read as a silent drop (no retries, no notice).
func TestStreamCompletion_NoDropNoticeOnCachedFollowUpIteration(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc1","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":300,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":0,"image_tokens":192}}}`+"\n\n")
		default:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"I see a red square."},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":320,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":256}}}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.WithPermissions(nil, func(tool, summary string) bool { return true })
	a.SetPendingImageParts([]ContentPart{
		{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,abc123"}},
	})

	var out strings.Builder
	if _, err := a.Run(context.Background(), nil, "what is in this image", &out, &session.Session{CWD: "/tmp"}, nil); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 requests (tool call + answer, no drop retries), got %d", got)
	}
	if strings.Contains(out.String(), "reported no image processing") {
		t.Errorf("false drop notice on a cached follow-up iteration: %q", out.String())
	}
}

func TestStreamCompletion_RequestSizeExcludesImageData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0,"image_tokens":50}}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	var reported int64
	a.WithOnRequestSize(func(n int64) { reported = n })
	big := "data:image/png;base64," + strings.Repeat("A", 2_000_000)
	a.SetPendingImageParts([]ContentPart{{Type: "image_url", ImageURL: &ImageURLPart{URL: big}}})

	var out strings.Builder
	if _, err := a.Run(context.Background(), nil, "what is this", &out, &session.Session{CWD: "/tmp"}, nil); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if reported <= 0 || reported > 500_000 {
		t.Errorf("request size should exclude the 2 MB image payload, got %d", reported)
	}
}
