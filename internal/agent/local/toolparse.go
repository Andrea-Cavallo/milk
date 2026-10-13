package local

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// reToolCall matches <tool_call>...</tool_call> blocks.
var reToolCall = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)

// reToolsBlock matches <tools>...</tools> blocks (Gemma 4 via llama.cpp).
var reToolsBlock = regexp.MustCompile(`(?s)<tools>\s*(.*?)\s*</tools>`)

// reFenced matches content inside markdown code fences.
// The closing fence is optional: the model sometimes omits it at end-of-stream.
var reFenced = regexp.MustCompile("(?s)```(?:xml|json)?\\s*(.*?)(?:\\s*```|$)")

// reGemmaSpecial matches Gemma 4's special-token tool call format:
//
//	<|tool_call>call:<name>{...}<tool_call|>
//
// <|"|> inside the body is a special-token quote for string values.
var reGemmaSpecial = regexp.MustCompile(`(?s)<\|tool_call>call:(\w+)(\{.*?\})<tool_call\|>`)

// reUnquotedKey quotes unquoted JSON keys produced after special-token substitution.
// Matches a key at the start of the object or after a comma, e.g. {path:"x"} → {"path":"x"}.
var reUnquotedKey = regexp.MustCompile(`([{,]\s*)(\w+)(\s*:)`)

// extractToolCalls parses tool calls from a raw content string emitted by the
// model when llama.cpp fails to translate them into the tool_calls field.
// Handles:
//   - <|tool_call>call:<name>{...}<tool_call|>  (Gemma 4 special tokens)
//   - <tool_call>{"name":..., "arguments":{...}}</tool_call>
//   - <tools>{"name":..., "arguments":{...}}</tools>
//   - ```xml\n{"name":..., "arguments":{...}}\n```
//   - bare JSON: {"name":..., "arguments":{...}}
func extractToolCalls(content string) []toolCall {
	if calls := extractGemmaSpecial(content); len(calls) > 0 {
		return calls
	}
	return extractJSONCandidates(content)
}

func extractGemmaSpecial(content string) []toolCall {
	matches := reGemmaSpecial.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	var calls []toolCall
	for _, m := range matches {
		if tc, ok := parseGemmaMatch(m[1], m[2]); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

func parseGemmaMatch(name, rawArgs string) (toolCall, bool) {
	rawArgs = strings.ReplaceAll(rawArgs, `<|"|>`, `"`)
	rawArgs = reUnquotedKey.ReplaceAllString(rawArgs, `$1"$2"$3`)
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return toolCall{}, false
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return toolCall{}, false
	}
	return toolCall{
		ID:       uuid.New().String(),
		Type:     "function",
		Function: toolCallFunction{Name: name, Arguments: string(argsJSON)},
	}, true
}

func extractJSONCandidates(content string) []toolCall {
	candidates := collectCandidates(content)
	var calls []toolCall
	for _, raw := range candidates {
		calls = append(calls, parseToolCallsJSON(raw)...)
	}
	return calls
}

// parseToolCallsJSON parses every tool call in one candidate body. Models do
// not stop at one object per block: Qwen2.5-Coder puts several calls in a
// fence, one JSON object per line; others emit a JSON array. The body is read
// as a stream of JSON values, so a truncated last call (cut stream) is dropped
// while the complete calls before it survive, and a value that is not a tool
// call (a config example) is skipped.
func parseToolCallsJSON(raw string) []toolCall {
	dec := json.NewDecoder(strings.NewReader(escapeStringControls(strings.TrimSpace(raw))))
	var calls []toolCall
	for {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return calls
		}
		if len(v) > 0 && v[0] == '[' {
			var elems []json.RawMessage
			if json.Unmarshal(v, &elems) == nil {
				for _, e := range elems {
					if tc, ok := parseToolCallJSON(string(e)); ok {
						calls = append(calls, tc)
					}
				}
			}
			continue
		}
		if tc, ok := parseToolCallJSON(string(v)); ok {
			calls = append(calls, tc)
		}
	}
}

// escapeStringControls escapes raw control characters inside JSON string
// literals. Models writing code into a "content" string often use real
// newlines and tabs instead of \n and \t, which strict JSON rejects. Valid
// JSON has no such characters inside strings, so it passes through unchanged.
func escapeStringControls(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case !inStr:
			if c == '"' {
				inStr = true
			}
			b.WriteByte(c)
		case esc:
			esc = false
			b.WriteByte(c)
		case c == '\\':
			esc = true
			b.WriteByte(c)
		case c == '"':
			inStr = false
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func collectCandidates(content string) []string {
	if m := submatchAll(reToolCall, content); len(m) > 0 {
		return m
	}
	if m := submatchAll(reToolsBlock, content); len(m) > 0 {
		return m
	}
	if m := submatchAll(reFenced, content); len(m) > 0 {
		return m
	}
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") {
		return []string{trimmed}
	}
	return nil
}

func submatchAll(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// nativeToolCall represents Qwen's native format where arguments is an object.
type nativeToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func parseToolCallJSON(raw string) (toolCall, bool) {
	var native nativeToolCall
	if err := json.Unmarshal([]byte(raw), &native); err != nil || native.Name == "" {
		return toolCall{}, false
	}

	// Normalise arguments to a JSON string (OpenAI format)
	var argsStr string
	if len(native.Arguments) > 0 {
		// If arguments is already a string (OpenAI format), unwrap it
		var s string
		if err := json.Unmarshal(native.Arguments, &s); err == nil {
			argsStr = s
		} else {
			// arguments is an object — re-encode as string
			argsStr = string(native.Arguments)
		}
	} else {
		argsStr = "{}"
	}

	return toolCall{
		ID:   uuid.New().String(),
		Type: "function",
		Function: toolCallFunction{
			Name:      native.Name,
			Arguments: argsStr,
		},
	}, true
}

// reUnparsedToolBlock matches a <tool_call>…</tool_call> block whose body is
// XML-style (<parameter=…>, </function>, or a name-less "=name>" remnant)
// rather than the JSON extractToolCalls understands — i.e. a call some
// servers leak into content when their own streaming parser fails.
var reUnparsedToolBlock = regexp.MustCompile(`(?s)<tool_call>(?:<?=|\s*<function=)?[^<]*?(?:<parameter=|</function>).*?</tool_call>`)

// stripUnparsedToolMarkup removes leaked XML-style tool-call blocks from a
// final response, reporting whether any were found. Only meant for text that
// already failed extractToolCalls, so the blocks never carried a runnable call.
func stripUnparsedToolMarkup(s string) (string, bool) {
	if !reUnparsedToolBlock.MatchString(s) {
		return s, false
	}
	return strings.TrimSpace(reUnparsedToolBlock.ReplaceAllString(s, "")), true
}
