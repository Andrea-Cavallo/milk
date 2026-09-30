package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func msg(role string) Message { return Message{Role: role} }

func TestDropOldestDroppableUnit_DropsWholePriorTurnFirst(t *testing.T) {
	msgs := []Message{
		msg("system"),
		msg("user"),      // prior turn
		msg("assistant"), // prior turn
		msg("user"),      // current turn (protectedIdx)
		msg("assistant"),
		msg("tool"),
	}
	got, ok := dropOldestDroppableUnit(msgs, 3)
	if !ok {
		t.Fatal("expected a drop to occur")
	}
	if len(got) != 4 {
		t.Fatalf("expected the whole prior turn (2 messages) dropped, got %d messages: %+v", len(got), got)
	}
	roles := rolesOf(got)
	if want := []string{"system", "user", "assistant", "tool"}; !equalStrings(roles, want) {
		t.Errorf("expected roles %v, got %v", want, roles)
	}
}

func TestDropOldestDroppableUnit_DropsAtomicAssistantToolGroupWhenOnlyCurrentTurnLeft(t *testing.T) {
	msgs := []Message{
		msg("system"),
		msg("user"), // only user message — current turn (protectedIdx)
		msg("assistant"),
		msg("tool"),
		msg("tool"),
		msg("assistant"),
		msg("tool"),
	}
	got, ok := dropOldestDroppableUnit(msgs, 1)
	if !ok {
		t.Fatal("expected a drop to occur")
	}
	roles := rolesOf(got)
	want := []string{"system", "user", "assistant", "tool"}
	if !equalStrings(roles, want) {
		t.Errorf("expected the oldest assistant+2 tool-results group dropped as one unit, got roles %v", roles)
	}
}

// TestDropOldestDroppableUnit_NeverOrphansToolMessage repeatedly drops until
// nothing is left to drop, checking after every step that no "tool"-role
// message is ever left as the message immediately following the sole
// remaining user message — the invalid shape from the real incident this
// fix addresses.
func TestDropOldestDroppableUnit_NeverOrphansToolMessage(t *testing.T) {
	msgs := []Message{
		msg("system"),
		msg("user"),
		msg("assistant"),
		msg("tool"),
		msg("tool"),
		msg("assistant"),
		msg("tool"),
		msg("assistant"),
		msg("tool"),
	}
	for i := 0; ; i++ {
		if i > 20 {
			t.Fatal("drop loop did not terminate")
		}
		userIdxs := []int{}
		for idx, m := range msgs {
			if m.Role == "user" {
				userIdxs = append(userIdxs, idx)
			}
		}
		if len(userIdxs) == 1 {
			anchor := userIdxs[0]
			if anchor+1 < len(msgs) && msgs[anchor+1].Role == "tool" {
				t.Fatalf("orphaned tool message immediately after the sole user message: %+v", rolesOf(msgs))
			}
		}
		// Only one user message exists in this scenario (index 1), and
		// neither branch of dropOldestDroppableUnit ever removes anything
		// at or before its protectedIdx, so the anchor never moves.
		next, ok := dropOldestDroppableUnit(msgs, 1)
		if !ok {
			break
		}
		msgs = next
	}
	// Must always end with at least system + user still present.
	roles := rolesOf(msgs)
	if len(roles) < 2 || roles[0] != "system" || roles[1] != "user" {
		t.Fatalf("expected system+user to survive, got %v", roles)
	}
}

func TestDropOldestDroppableUnit_ReturnsFalseWhenNothingLeftToDrop(t *testing.T) {
	msgs := []Message{msg("system"), msg("user")}
	got, ok := dropOldestDroppableUnit(msgs, 1)
	if ok {
		t.Errorf("expected no drop possible, got %v", rolesOf(got))
	}
}

func TestDropOldestDroppableUnit_NoUserMessage(t *testing.T) {
	msgs := []Message{msg("system")}
	// No user message exists at all, so no valid protectedIdx exists either
	// — any index passed here should be treated as an invalid anchor.
	_, ok := dropOldestDroppableUnit(msgs, 1)
	if ok {
		t.Error("expected no drop possible with no user message at all")
	}
}

// TestStreamCompletion_PayloadTrim_NeverOrphansToolMessage reproduces the
// real incident: a background job's turn (one user message, then a long
// tool-call history from reading several files) whose marshaled request
// exceeds maxPayloadBytes. Asserts the actual request sent to the server
// still starts with system+user and contains no orphaned tool message.
func TestStreamCompletion_PayloadTrim_NeverOrphansToolMessage(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMaxPayloadBytes(2000)

	big := strings.Repeat("x", 500)
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "the actual task"},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			Message{Role: "assistant", Content: "", ToolCalls: []toolCall{{ID: fmt.Sprintf("tc%d", i), Function: toolCallFunction{Name: "read_file"}}}},
			Message{Role: "tool", Content: big, ToolCallID: fmt.Sprintf("tc%d", i)},
		)
	}

	var out strings.Builder
	_, _, _, _, _, err := agent.streamCompletion(context.Background(), msgs, nil, &out, 1)
	if err != nil {
		t.Fatalf("streamCompletion returned error: %v", err)
	}

	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("could not parse sent request body: %v", err)
	}
	if len(req.Messages) < 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
		t.Fatalf("expected system+user to survive trimming, got roles %v", rolesOfRaw(req.Messages))
	}
	for i, m := range req.Messages {
		if m.Role == "tool" {
			if i == 0 || req.Messages[i-1].Role != "assistant" {
				t.Fatalf("orphaned tool message at index %d with no preceding assistant message: %v", i, rolesOfRaw(req.Messages))
			}
		}
	}
	if len(gotBody) > 2000+500 { // some slack for JSON structure overhead
		t.Errorf("expected the sent request to be trimmed close to the 2000-byte budget, got %d bytes", len(gotBody))
	}
}

// TestDropOldestDroppableUnit_PreservesRealQuestionAcrossInjectedNudge
// reproduces the real incident this fix addresses: a loop-recovery nudge
// (loop_streak.go's loopRecoveryAction, the try-best detector, the
// max-iteration reminder, and the "image dropped" notice all inject one)
// is itself a Role: "user" message. Before this fix, dropOldestDroppableUnit
// treated that nudge as marking a newer, disposable turn boundary and
// deleted the real question (protectedIdx) plus everything answering it in
// a single drop — see the doc comment on dropOldestDroppableUnit.
func TestDropOldestDroppableUnit_PreservesRealQuestionAcrossInjectedNudge(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "there's a percept about a dog name I don't recall"}, // protectedIdx=1
		{Role: "assistant", ToolCalls: []toolCall{{ID: "tc0", Function: toolCallFunction{Name: "get_memory"}}}},
		{Role: "tool", Content: "(no relevant memories found)", ToolCallID: "tc0"},
		{Role: "user", Content: "<system-reminder>duplicate tool call nudge</system-reminder>"}, // injected nudge, NOT a real turn
		{Role: "assistant", ToolCalls: []toolCall{{ID: "tc1", Function: toolCallFunction{Name: "bash"}}}},
		{Role: "tool", Content: "git status output", ToolCallID: "tc1"},
	}
	got, ok := dropOldestDroppableUnit(msgs, 1)
	if !ok {
		t.Fatal("expected a drop to occur")
	}
	found := false
	for _, m := range got {
		if strings.Contains(m.Content, "dog name") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the real question to survive the drop, got %+v", rolesOf(got))
	}
	if got[1].Role != "user" || !strings.Contains(got[1].Content, "dog name") {
		t.Fatalf("expected the real question to remain at protectedIdx (1), got %+v", rolesOf(got))
	}
}

// TestStreamCompletion_PayloadTrim_PreservesRealQuestionAcrossInjectedNudge
// is the end-to-end version: with maxPayloadBytes forcing a trim, the actual
// request sent to the server must still contain the real question's text,
// even though a nudge-style "user" message was injected partway through the
// turn's tool-call history.
func TestStreamCompletion_PayloadTrim_PreservesRealQuestionAcrossInjectedNudge(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMaxPayloadBytes(2000)

	big := strings.Repeat("x", 500)
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "there's a percept about a dog name I don't recall"}, // userMsgIdx=1
	}
	for i := 0; i < 5; i++ {
		msgs = append(msgs,
			Message{Role: "assistant", ToolCalls: []toolCall{{ID: fmt.Sprintf("tc%d", i), Function: toolCallFunction{Name: "bash"}}}},
			Message{Role: "tool", Content: big, ToolCallID: fmt.Sprintf("tc%d", i)},
		)
	}
	// A loop-recovery nudge, injected mid-turn exactly as loopRecoveryAction does.
	msgs = append(msgs, Message{Role: "user", Content: "<system-reminder>stop repeating yourself</system-reminder>"})
	for i := 5; i < 10; i++ {
		msgs = append(msgs,
			Message{Role: "assistant", ToolCalls: []toolCall{{ID: fmt.Sprintf("tc%d", i), Function: toolCallFunction{Name: "bash"}}}},
			Message{Role: "tool", Content: big, ToolCallID: fmt.Sprintf("tc%d", i)},
		)
	}

	var out strings.Builder
	_, _, _, _, _, err := agent.streamCompletion(context.Background(), msgs, nil, &out, 1)
	if err != nil {
		t.Fatalf("streamCompletion returned error: %v", err)
	}
	if !strings.Contains(string(gotBody), "dog name") {
		t.Fatalf("expected the real question to survive payload trimming and reach the server, got body: %s", gotBody)
	}
}

func rolesOf(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

func rolesOfRaw(msgs []struct {
	Role       string `json:"role"`
	ToolCallID string `json:"tool_call_id"`
}) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
