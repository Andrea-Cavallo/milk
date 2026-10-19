package contextreduce

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	want := Policy{
		Enabled:                 true,
		InstructionThresholdPct: 55,
		ToolCompactThresholdPct: 65,
		ToolPruneThresholdPct:   75,
		InstructionsTrimChars:   4000,
		ProtectedTools:          []string{"bash", "read_file", "ask_user"},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("DefaultPolicy() = %+v, want %+v", p, want)
	}
}

func TestLevelFor(t *testing.T) {
	full := DefaultPolicy()
	tests := []struct {
		name        string
		tokens      int
		denominator int
		policy      Policy
		want        Level
	}{
		{
			name: "disabled policy", tokens: 90, denominator: 100,
			policy: Policy{Enabled: false, InstructionThresholdPct: 55, ToolCompactThresholdPct: 65, ToolPruneThresholdPct: 75},
			want:   LevelNone,
		},
		{
			name: "disabled policy at 100 pct", tokens: 100, denominator: 100,
			policy: Policy{Enabled: false, InstructionThresholdPct: 55, ToolCompactThresholdPct: 65, ToolPruneThresholdPct: 75},
			want:   LevelNone,
		},
		{
			name: "zero tokens", tokens: 0, denominator: 100,
			policy: full, want: LevelNone,
		},
		{
			name: "negative tokens", tokens: -5, denominator: 100,
			policy: full, want: LevelNone,
		},
		{
			name: "zero denominator", tokens: 100, denominator: 0,
			policy: full, want: LevelNone,
		},
		{
			name: "negative denominator", tokens: 100, denominator: -1000,
			policy: full, want: LevelNone,
		},
		{
			name: "empty policy falls back to defaults, low pressure", tokens: 10, denominator: 100,
			policy: Policy{Enabled: true}, want: LevelNone,
		},
		{
			name: "just below instruction threshold", tokens: 54, denominator: 100,
			policy: full, want: LevelNone,
		},
		{
			name: "exact instruction threshold hit", tokens: 55, denominator: 100,
			policy: full, want: LevelInstructions,
		},
		{
			name: "exact tool-compact threshold hit", tokens: 65, denominator: 100,
			policy: full, want: LevelToolsCompact,
		},
		{
			name: "just below tool-compact threshold", tokens: 64, denominator: 100,
			policy: full, want: LevelInstructions,
		},
		{
			name: "exact tool-prune threshold hit", tokens: 75, denominator: 100,
			policy: full, want: LevelToolsPrune,
		},
		{
			name: "just below tool-prune threshold", tokens: 74, denominator: 100,
			policy: full, want: LevelToolsCompact,
		},
		{
			name: "above tool-prune threshold", tokens: 100, denominator: 100,
			policy: full, want: LevelToolsPrune,
		},
		{
			name: "integer division rounds down below threshold", tokens: 54, denominator: 99,
			policy: full, want: LevelNone, // 54*100/99 = 54
		},
		{
			name: "non-round denominator hits threshold", tokens: 13, denominator: 20,
			policy: full, want: LevelToolsCompact, // 65 pct
		},
		{
			name: "only compact threshold set, others default", tokens: 66, denominator: 100,
			policy: Policy{Enabled: true, ToolCompactThresholdPct: 66}, want: LevelToolsCompact,
		},
		{
			name: "only compact threshold set, below it defaults to instructions", tokens: 65, denominator: 100,
			policy: Policy{Enabled: true, ToolCompactThresholdPct: 66}, want: LevelInstructions,
		},
		{
			name: "out-of-order thresholds are clamped upward", tokens: 80, denominator: 100,
			policy: Policy{Enabled: true, InstructionThresholdPct: 80, ToolCompactThresholdPct: 50, ToolPruneThresholdPct: 60},
			want:   LevelToolsPrune, // compact raised to 80, prune raised to 80
		},
		{
			name: "out-of-order thresholds below clamped value", tokens: 79, denominator: 100,
			policy: Policy{Enabled: true, InstructionThresholdPct: 80, ToolCompactThresholdPct: 50, ToolPruneThresholdPct: 60},
			want:   LevelNone,
		},
		{
			name: "thresholds clamped to 99", tokens: 99, denominator: 100,
			policy: Policy{Enabled: true, InstructionThresholdPct: 200, ToolCompactThresholdPct: 200, ToolPruneThresholdPct: 200},
			want:   LevelToolsPrune,
		},
		{
			name: "oversized threshold clamps to 99 below it", tokens: 98, denominator: 100,
			policy: Policy{Enabled: true, InstructionThresholdPct: 200, ToolCompactThresholdPct: 200, ToolPruneThresholdPct: 200},
			want:   LevelNone,
		},
		{
			name: "negative threshold clamps to 1", tokens: 1, denominator: 100,
			policy: Policy{Enabled: true, InstructionThresholdPct: -5, ToolCompactThresholdPct: 2, ToolPruneThresholdPct: 3},
			want:   LevelInstructions,
		},
		{
			name: "all-zero custom policy matches default ladder", tokens: 75, denominator: 100,
			policy: Policy{Enabled: true}, want: LevelToolsPrune,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LevelFor(tt.tokens, tt.denominator, tt.policy); got != tt.want {
				t.Errorf("LevelFor(%d, %d, %+v) = %v, want %v", tt.tokens, tt.denominator, tt.policy, got, tt.want)
			}
		})
	}
}

func TestLevelFor_LadderIsMonotonic(t *testing.T) {
	p := Policy{Enabled: true, InstructionThresholdPct: 70, ToolCompactThresholdPct: 40, ToolPruneThresholdPct: 50}
	var prev Level = -1
	for tokens := 0; tokens <= 100; tokens++ {
		got := LevelFor(tokens, 100, p)
		if got < prev {
			t.Fatalf("LevelFor(%d, 100, ...) = %v, drops below previous %v", tokens, got, prev)
		}
		prev = got
	}
}

func TestStepDownTier(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"full", "standard"},
		{"standard", "minimal"},
		{"minimal", "minimal"},
		{"", "minimal"},
		{"FULL", "minimal"},
		{"bogus", "minimal"},
		{"full ", "minimal"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := StepDownTier(tt.in); got != tt.want {
				t.Errorf("StepDownTier(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTrimBlock(t *testing.T) {
	t.Run("short string unchanged", func(t *testing.T) {
		s := "hello"
		if got := TrimBlock(s, 100); got != s {
			t.Errorf("TrimBlock(%q, 100) = %q, want unchanged", s, got)
		}
	})
	t.Run("exact fit unchanged", func(t *testing.T) {
		s := strings.Repeat("x", 100)
		if got := TrimBlock(s, 100); got != s {
			t.Errorf("TrimBlock(exact 100, 100) = %d chars, want unchanged", len(got))
		}
	})
	t.Run("zero and negative maxChars unchanged", func(t *testing.T) {
		s := strings.Repeat("x", 100)
		for _, max := range []int{0, -1, -100} {
			if got := TrimBlock(s, max); got != s {
				t.Errorf("TrimBlock(s, %d) = %d chars, want unchanged", max, len(got))
			}
		}
	})
	t.Run("keeps head tail and marker within budget", func(t *testing.T) {
		s := "HEAD" + strings.Repeat("x", 1000) + "TAIL"
		max := 100
		got := TrimBlock(s, max)
		if len(got) > max {
			t.Errorf("result length %d exceeds maxChars %d", len(got), max)
		}
		if !strings.HasPrefix(got, "HEAD") {
			t.Errorf("result %q does not preserve head", got[:20])
		}
		if !strings.HasSuffix(got, "TAIL") {
			t.Errorf("result %q does not preserve tail", got[len(got)-20:])
		}
		if !strings.Contains(got, "... (truncated) ...") {
			t.Errorf("result %q missing truncation marker", got)
		}
		if len(got) >= len(s) {
			t.Errorf("result is not shorter than input")
		}
	})
	t.Run("marker counted within maxChars", func(t *testing.T) {
		s := strings.Repeat("y", 500)
		for _, max := range []int{21, 22, 30, 64, 65, 200} {
			got := TrimBlock(s, max)
			if len(got) > max {
				t.Errorf("maxChars=%d: result length %d exceeds budget", max, len(got))
			}
		}
	})
	t.Run("tiny maxChars falls back to head truncation", func(t *testing.T) {
		s := "hello world, this is long"
		got := TrimBlock(s, 5)
		if len(got) > 5 {
			t.Errorf("TrimBlock(s, 5) = %q (%d chars), want <= 5", got, len(got))
		}
	})
	t.Run("rune safe on multibyte input", func(t *testing.T) {
		s := strings.Repeat("日", 300) + strings.Repeat("é", 300) + strings.Repeat("ß", 300)
		for _, max := range []int{25, 40, 100, 101, 137, 500} {
			got := TrimBlock(s, max)
			if len(got) > max {
				t.Errorf("maxChars=%d: result length %d exceeds budget", max, len(got))
			}
			if !utf8.ValidString(got) {
				t.Errorf("maxChars=%d: result %q is not valid UTF-8", max, got)
			}
			if strings.ContainsRune(got, '\uFFFD') {
				t.Errorf("maxChars=%d: result contains replacement rune", max)
			}
		}
	})
	t.Run("rune safe head fallback", func(t *testing.T) {
		s := strings.Repeat("日", 50)
		got := TrimBlock(s, 5) // 5 bytes cannot hold a full rune plus marker
		if !utf8.ValidString(got) {
			t.Errorf("result %q is not valid UTF-8", got)
		}
		if len(got) > 5 {
			t.Errorf("result length %d exceeds maxChars 5", len(got))
		}
	})
}

func TestCompactToolDescription(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty stays empty", in: "", want: ""},
		{name: "first sentence only", in: "Does a thing. Also does more things.", want: "Does a thing"},
		{name: "cut at newline", in: "Line one\nLine two", want: "Line one"},
		{name: "newline before period", in: "First line\nSecond. Third", want: "First line"},
		{name: "period-newline keeps sentence", in: "A.\nB", want: "A."},
		{name: "no delimiter keeps all", in: "No period here", want: "No period here"},
		{name: "trims surrounding space", in: "  Hello. World  ", want: "Hello"},
		{name: "terminal period without space kept", in: "Short.", want: "Short."},
		{name: "only spaces trims to empty", in: "   ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompactToolDescription(tt.in); got != tt.want {
				t.Errorf("CompactToolDescription(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	t.Run("caps long sentence with ellipsis", func(t *testing.T) {
		in := strings.Repeat("a", 200) + ". tail"
		got := CompactToolDescription(in)
		if n := utf8.RuneCountInString(got); n != 120 {
			t.Errorf("got %d runes (%q), want 120", n, got)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("got %q, want trailing ellipsis", got)
		}
	})
	t.Run("no ellipsis at exactly 120 runes", func(t *testing.T) {
		in := strings.Repeat("b", 120) + ". tail"
		got := CompactToolDescription(in)
		if got != strings.Repeat("b", 120) {
			t.Errorf("got %q, want 120 b's unchanged", got)
		}
	})
	t.Run("cap is rune based on multibyte input", func(t *testing.T) {
		in := strings.Repeat("é", 300) + ". tail"
		got := CompactToolDescription(in)
		if n := utf8.RuneCountInString(got); n != 120 {
			t.Errorf("got %d runes, want 120", n)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("got %q, want trailing ellipsis", got)
		}
	})
}

func TestCompactTools(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if got := CompactTools(nil); got != nil {
			t.Errorf("CompactTools(nil) = %v, want nil", got)
		}
	})
	t.Run("empty input returns empty", func(t *testing.T) {
		got := CompactTools([]map[string]any{})
		if len(got) != 0 {
			t.Errorf("CompactTools(empty) = %v, want empty", got)
		}
	})

	t.Run("compacts description at both depths", func(t *testing.T) {
		tools := []map[string]any{
			{
				"name":        "top_only",
				"description": "Top level description. More text.",
				"type":        "function",
				"function": map[string]any{
					"name":       "top_only",
					"parameters": map[string]any{"type": "object"},
				},
			},
			{
				"type": "function",
				"function": map[string]any{
					"name":        "fn_level",
					"description": "Nested description. More text.",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"arg": map[string]any{"description": "Arg description. More."},
						},
					},
				},
			},
			{
				"description": "Both depths. Top.",
				"function": map[string]any{
					"name":        "both",
					"description": "Both depths. Fn.",
				},
			},
		}
		got := CompactTools(tools)
		if len(got) != 3 {
			t.Fatalf("got %d tools, want 3", len(got))
		}
		if d := got[0]["description"]; d != "Top level description" {
			t.Errorf("top-level description = %q", d)
		}
		fn := got[1]["function"].(map[string]any)
		if d := fn["description"]; d != "Nested description" {
			t.Errorf("function-level description = %q", d)
		}
		// Deeper descriptions are out of scope and untouched.
		props := fn["parameters"].(map[string]any)["properties"].(map[string]any)
		arg := props["arg"].(map[string]any)
		if d := arg["description"]; d != "Arg description. More." {
			t.Errorf("deep description = %q, want untouched", d)
		}
		both := got[2]
		if d := both["description"]; d != "Both depths" {
			t.Errorf("both-depth top description = %q", d)
		}
		if d := both["function"].(map[string]any)["description"]; d != "Both depths" {
			t.Errorf("both-depth fn description = %q", d)
		}
	})

	t.Run("non-string descriptions untouched", func(t *testing.T) {
		tools := []map[string]any{
			{"description": 42, "function": map[string]any{"description": nil}},
		}
		got := CompactTools(tools)
		if d := got[0]["description"]; d != 42 {
			t.Errorf("top-level description = %v, want 42", d)
		}
		fn := got[0]["function"].(map[string]any)
		if d, ok := fn["description"]; !ok || d != nil {
			t.Errorf("function description = %v, want nil", d)
		}
	})

	t.Run("input not mutated and no map aliasing", func(t *testing.T) {
		tools := []map[string]any{
			{
				"description": "Top description. Rest dropped.",
				"function": map[string]any{
					"name":        "t",
					"description": "Fn description. Rest dropped.",
					"parameters": map[string]any{
						"properties": map[string]any{"a": map[string]any{"description": "deep"}},
					},
				},
			},
		}
		snapshot := cloneMap(tools[0])

		got := CompactTools(tools)
		if !reflect.DeepEqual(tools[0], snapshot) {
			t.Errorf("input mutated: got %+v, want %+v", tools[0], snapshot)
		}
		if len(got) != 1 {
			t.Fatalf("got %d tools, want 1", len(got))
		}
		if &got[0] == &tools[0] {
			t.Errorf("output element aliases input element")
		}

		// Mutating every map in the output must not leak into the input.
		got[0]["description"] = "changed"
		fn := got[0]["function"].(map[string]any)
		fn["description"] = "changed"
		fn["parameters"].(map[string]any)["properties"].(map[string]any)["a"].(map[string]any)["description"] = "changed"
		if !reflect.DeepEqual(tools[0], snapshot) {
			t.Errorf("output mutation leaked into input: got %+v, want %+v", tools[0], snapshot)
		}
	})
}

func TestPruneTools(t *testing.T) {
	tool := func(name string) map[string]any {
		return map[string]any{"name": name, "description": name + " tool"}
	}
	fnTool := func(name string) map[string]any {
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name, "description": name + " tool"},
		}
	}

	t.Run("nil used returns input unchanged", func(t *testing.T) {
		tools := []map[string]any{tool("a"), tool("b")}
		got := PruneTools(tools, nil, []string{"bash"})
		if !reflect.DeepEqual(got, tools) {
			t.Errorf("PruneTools with nil used = %v, want input unchanged", got)
		}
	})
	t.Run("empty used returns input unchanged", func(t *testing.T) {
		tools := []map[string]any{tool("a")}
		got := PruneTools(tools, map[string]bool{}, nil)
		if !reflect.DeepEqual(got, tools) {
			t.Errorf("PruneTools with empty used = %v, want input unchanged", got)
		}
	})

	t.Run("keeps used and protected in order", func(t *testing.T) {
		tools := []map[string]any{tool("grep"), tool("sed"), tool("bash"), tool("awk"), fnTool("read_file")}
		got := PruneTools(tools, map[string]bool{"grep": true, "sed": true}, []string{"bash", "read_file"})
		want := []map[string]any{tools[0], tools[1], tools[2], tools[4]}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("PruneTools = %v, want %v", names(got), names(want))
		}
	})

	t.Run("name from nested function counts", func(t *testing.T) {
		tools := []map[string]any{fnTool("web_search"), fnTool("code_exec")}
		got := PruneTools(tools, map[string]bool{"web_search": true}, nil)
		if len(got) != 1 || !sameMap(got[0], tools[0]) {
			t.Errorf("PruneTools = %v, want [web_search]", names(got))
		}
	})

	t.Run("either name match keeps the tool", func(t *testing.T) {
		mixed := map[string]any{"name": "alias", "function": map[string]any{"name": "real"}}
		tools := []map[string]any{mixed}
		if got := PruneTools(tools, map[string]bool{"real": true}, nil); len(got) != 1 {
			t.Errorf("PruneTools by function name = %v, want kept", names(got))
		}
		if got := PruneTools(tools, map[string]bool{"alias": true}, nil); len(got) != 1 {
			t.Errorf("PruneTools by top-level name = %v, want kept", names(got))
		}
	})

	t.Run("unnamed tools dropped", func(t *testing.T) {
		tools := []map[string]any{{"description": "no name"}, tool("keep")}
		got := PruneTools(tools, map[string]bool{"keep": true}, nil)
		if len(got) != 1 || !sameMap(got[0], tools[1]) {
			t.Errorf("PruneTools = %v, want [keep]", names(got))
		}
	})

	t.Run("all dropped returns empty", func(t *testing.T) {
		tools := []map[string]any{tool("a"), fnTool("b")}
		got := PruneTools(tools, map[string]bool{"c": true}, []string{"d"})
		if len(got) != 0 {
			t.Errorf("PruneTools = %v, want empty", names(got))
		}
	})

	t.Run("never mutates input", func(t *testing.T) {
		tools := []map[string]any{tool("a"), tool("b")}
		snapshot := []map[string]any{cloneMap(tools[0]), cloneMap(tools[1])}
		got := PruneTools(tools, map[string]bool{"a": true}, nil)
		if !reflect.DeepEqual(tools, snapshot) {
			t.Errorf("input mutated: got %+v, want %+v", tools, snapshot)
		}
		if len(got) != 1 || !sameMap(got[0], tools[0]) {
			t.Errorf("PruneTools = %v, want [a] sharing input elements", names(got))
		}
	})
}

// sameMap reports whether a and b are the same map (shared backing store).
func sameMap(a, b map[string]any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func names(tools []map[string]any) []string {
	var out []string
	for _, tool := range tools {
		name := "?"
		if s, ok := tool["name"].(string); ok {
			name = s
		} else if fn, ok := tool["function"].(map[string]any); ok {
			if s, ok := fn["name"].(string); ok {
				name = s
			}
		}
		out = append(out, name)
	}
	return out
}
