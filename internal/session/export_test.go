package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testSession() *Session {
	s := &Session{
		ID:        "test-session-id-1234",
		Name:      "test",
		CWD:       "/home/user/project",
		CreatedAt: time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
		LastUsed:  time.Date(2026, 5, 15, 10, 5, 0, 0, time.UTC),
		State:     StateRouting,
	}
	s.History = []Turn{
		{Role: RoleUser, Agent: AgentLocal, Content: "hello", Timestamp: time.Date(2026, 5, 15, 10, 1, 0, 0, time.UTC)},
		{Role: RoleAssistant, Agent: AgentLocal, Content: "world", Timestamp: time.Date(2026, 5, 15, 10, 2, 0, 0, time.UTC)},
	}
	return s
}

func TestExportText_ContainsMetadata(t *testing.T) {
	s := testSession()
	out := ExportText(s)

	for _, want := range []string{"test-session-id-1234", "/home/user/project", "ROUTING"} {
		if !strings.Contains(out, want) {
			t.Errorf("ExportText missing %q", want)
		}
	}
}

func TestExportText_ContainsHistory(t *testing.T) {
	s := testSession()
	out := ExportText(s)

	if !strings.Contains(out, "hello") {
		t.Error("ExportText missing user turn content")
	}
	if !strings.Contains(out, "world") {
		t.Error("ExportText missing assistant turn content")
	}
}

func TestExportText_ToolCallsFormatted(t *testing.T) {
	s := testSession()
	s.History = append(s.History, Turn{
		Role:  RoleAssistant,
		Agent: AgentLocal,
		ToolCalls: []ToolCall{
			{ID: "tc1", Name: "bash", Arguments: `{"command":"ls"}`},
		},
		Timestamp: time.Now(),
	})
	out := ExportText(s)
	if !strings.Contains(out, "bash") {
		t.Error("ExportText should include tool call name")
	}
}

func TestExportText_ToolResultTruncated(t *testing.T) {
	s := testSession()
	long := strings.Repeat("x", 400)
	s.History = append(s.History, Turn{
		Role:      RoleToolResult,
		Content:   long,
		Timestamp: time.Now(),
	})
	out := ExportText(s)
	if strings.Contains(out, long) {
		t.Error("ExportText should truncate long tool results")
	}
	if !strings.Contains(out, "...") {
		t.Error("ExportText should show truncation indicator")
	}
}

func TestExportText_NoANSI(t *testing.T) {
	s := testSession()
	out := ExportText(s)
	if strings.Contains(out, "\033[") {
		t.Error("ExportText should not contain ANSI escape codes")
	}
}

func TestExportTextColorized_ContainsANSI(t *testing.T) {
	s := testSession()
	out := ExportTextColorized(s)
	if !strings.Contains(out, "\033[") {
		t.Error("ExportTextColorized should contain ANSI escape codes")
	}
	// Body content must still be present.
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Error("ExportTextColorized should include turn content")
	}
}

func TestExportJSON_ValidJSON(t *testing.T) {
	s := testSession()
	data, err := ExportJSON(s)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var roundtrip Session
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("ExportJSON produced invalid JSON: %v", err)
	}
	if roundtrip.ID != s.ID {
		t.Errorf("ID mismatch after round-trip: got %q", roundtrip.ID)
	}
	if len(roundtrip.History) != len(s.History) {
		t.Errorf("history length mismatch: got %d, want %d", len(roundtrip.History), len(s.History))
	}
}

// --- issue #277: reasoning in exports -------------------------------------

func thinkingSession() *Session {
	s := testSession()
	s.History[1].Thinking = "first I checked the failing test\nthen I ran it"
	return s
}

// Reasoning is included by default, fenced between the header and the reply.
func TestExportText_ReasoningIncludedByDefault(t *testing.T) {
	s := thinkingSession()
	out := ExportText(s)

	open := strings.Index(out, "  <reasoning>")
	close := strings.Index(out, "  </reasoning>")
	header := strings.Index(out, "primary")
	reply := strings.Index(out, "world")
	if open < 0 || close < 0 || close < open {
		t.Fatalf("reasoning markers missing or unbalanced:\n%s", out)
	}
	if header < 0 || open < header {
		t.Errorf("fence must come after the assistant header (header=%d open=%d)", header, open)
	}
	if reply < 0 || close > reply {
		t.Errorf("fence must come before the reply text (close=%d reply=%d)", close, reply)
	}
	for _, want := range []string{"first I checked", "ran it"} {
		if !strings.Contains(out, want) {
			t.Errorf("reasoning body missing %q:\n%s", want, out)
		}
	}
}

// NoReasoning strips markers and body alike.
func TestExportText_ReasoningStripped(t *testing.T) {
	out := ExportTextWithOptions(thinkingSession(), ExportOptions{NoReasoning: true})
	for _, bad := range []string{"<reasoning>", "first I checked", "ran it"} {
		if strings.Contains(out, bad) {
			t.Errorf("stripped export still contains %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "world") {
		t.Error("reply content must survive stripping")
	}
	if strings.Contains(out, "\033") {
		t.Error("plain export must stay ANSI-free")
	}
}

// Byte-identical across runs — exports read session data only, so UI fold
// state can never leak in (the determinism the issue asks for).
func TestExportText_Deterministic(t *testing.T) {
	s := thinkingSession()
	if ExportText(s) != ExportText(s) {
		t.Error("two exports of the same session differ")
	}
	if ExportTextWithOptions(s, ExportOptions{NoReasoning: true}) !=
		ExportTextWithOptions(s, ExportOptions{NoReasoning: true}) {
		t.Error("two stripped exports of the same session differ")
	}
	if ExportTextColorized(s) != ExportTextColorized(s) {
		t.Error("two colorized exports of the same session differ")
	}
	// Reading the session must not mutate it (a second export via another
	// path sees the same bytes).
	if s.History[1].Thinking == "" {
		t.Error("export must not clear the session's stored reasoning")
	}
}

// Oversized reasoning is budgeted with an omission note, not dumped whole.
func TestExportText_ReasoningTruncated(t *testing.T) {
	s := testSession()
	big := strings.Repeat("line of reasoning\n", 2000) // ~38 KB > 8192
	s.History[1].Thinking = big
	out := ExportText(s)
	if !strings.Contains(out, "chars omitted") {
		t.Errorf("capped reasoning must carry an omission note:\n%.400s", out)
	}
	if strings.Contains(out, big[:100]) && strings.Count(out, "line of reasoning") > 1000 {
		t.Errorf("oversized reasoning was dumped whole (%d lines)", strings.Count(out, "line of reasoning"))
	}
	// The fence stays balanced around the capped body.
	if strings.Index(out, "  </reasoning>") < strings.Index(out, "  <reasoning>") {
		t.Error("unbalanced fence after truncation")
	}
}

// Colorized markers carry the thinking hue; the body stays verbatim.
func TestExportText_ColorizedReasoningMarkers(t *testing.T) {
	out := ExportTextColorized(thinkingSession())
	if !strings.Contains(out, "\033[1;35m  <reasoning>\033[0m") {
		t.Errorf("open marker not colourised as thinking:\n%.300s", out)
	}
	if !strings.Contains(out, "first I checked the failing test\n") {
		t.Error("reasoning body must stay verbatim under colorize")
	}
}

// JSON includes reasoning by default; NoReasoning drops only the thinking
// keys (other fields intact, session unmutated), losing no numeric precision.
func TestExportJSON_ReasoningFlags(t *testing.T) {
	s := thinkingSession()
	s.History[1].ToolCalls = []ToolCall{{ID: "tc1", Name: "bash", Arguments: `{"command":"ls"}`}}

	raw, err := ExportJSON(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"thinking"`) {
		t.Errorf("default JSON export must include reasoning:\n%.200s", raw)
	}

	stripped, err := ExportJSONWithOptions(s, ExportOptions{NoReasoning: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stripped), `"thinking"`) {
		t.Errorf("stripped JSON export still carries thinking:\n%.200s", stripped)
	}
	var probe struct {
		History []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Name string `json:"name"`
			} `json:"tool_calls"`
		} `json:"history"`
	}
	if err := json.Unmarshal(stripped, &probe); err != nil {
		t.Fatalf("stripped export is not valid JSON: %v", err)
	}
	if len(probe.History) != len(s.History) || probe.History[1].Content != "world" ||
		len(probe.History[1].ToolCalls) != 1 {
		t.Errorf("stripping must not touch anything else: %+v", probe.History)
	}
	if s.History[1].Thinking == "" {
		t.Error("stripping must not mutate the session")
	}
}
