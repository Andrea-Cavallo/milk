package local

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/memory"
	"github.com/scoutme/milk/internal/session"
)

func TestHTMLToMarkdown(t *testing.T) {
	page := `<!doctype html><html><head><title>t</title>
<script>alert(1)</script><style>.x{}</style></head><body>
<h1>Page Title</h1>
<p>Some <code>inline</code> text with a <a href="https://example.com/x">link</a>.</p>
<ul><li>first</li><li>second</li></ul>
<pre>const x = 1;</pre>
<img src="/img.png" alt="pic">
</body></html>`

	md := htmlToMarkdown(page)
	for _, want := range []string{
		"# Page Title",
		"`inline`",
		"[link](https://example.com/x)",
		"- first",
		"- second",
		"```\nconst x = 1;\n```",
		"![pic](/img.png)",
		"Some",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	for _, ban := range []string{"alert(1)", ".x{}", "<p>", "<h1>"} {
		if strings.Contains(md, ban) {
			t.Errorf("markdown must not contain %q:\n%s", ban, md)
		}
	}
}

func TestUpgradeHTTPS(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantURL  string
		wantUpgr bool
	}{
		{"http://example.com/a", "https://example.com/a", true},
		{"https://example.com/a", "https://example.com/a", false},
		{"http://localhost:8080/x", "http://localhost:8080/x", false},
		{"http://127.0.0.1:9/x", "http://127.0.0.1:9/x", false},
		{"http://[::1]:9/x", "http://[::1]:9/x", false},
	} {
		got, upgr := upgradeHTTPS(tc.in)
		if got != tc.wantURL || upgr != tc.wantUpgr {
			t.Errorf("upgradeHTTPS(%q) = (%q, %v), want (%q, %v)", tc.in, got, upgr, tc.wantURL, tc.wantUpgr)
		}
	}
}

func webFetchServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `<html><body><h1>Doc Page</h1><p>Body &amp; text.</p></body></html>`) //nolint:errcheck
		case "/data":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"k": "v"}`) //nolint:errcheck
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, strings.Repeat("z", 5000)) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRunWebFetch_Formats(t *testing.T) {
	srv := webFetchServer(t)
	defer srv.Close()
	output := func(res string) string {
		var r toolResult
		if err := json.Unmarshal([]byte(res), &r); err != nil {
			t.Fatalf("not toolResult JSON: %v (%s)", err, res)
		}
		return r.Output
	}

	// auto: HTML → markdown
	res, _ := runWebFetch(context.Background(), map[string]any{"url": srv.URL + "/page"})
	if isToolError(res) {
		t.Fatalf("web_fetch: %s", res)
	}
	if out := output(res); !strings.Contains(out, "# Doc Page") || !strings.Contains(out, "Body & text.") {
		t.Errorf("auto format must convert HTML to markdown: %q", out)
	}

	// text: markup stripped, no markdown link syntax
	res, _ = runWebFetch(context.Background(), map[string]any{"url": srv.URL + "/page", "format": "text"})
	if out := output(res); strings.Contains(out, "](") || strings.Contains(out, "<h1>") {
		t.Errorf("text format must strip markup without markdown syntax: %q", out)
	}

	// html/raw: untouched bytes
	res, _ = runWebFetch(context.Background(), map[string]any{"url": srv.URL + "/page", "format": "html"})
	if out := output(res); !strings.Contains(out, "<h1>Doc Page</h1>") {
		t.Errorf("html format must keep raw bytes: %q", out)
	}

	// non-HTML passes through
	res, _ = runWebFetch(context.Background(), map[string]any{"url": srv.URL + "/data"})
	if out := output(res); !strings.Contains(out, `"k": "v"`) {
		t.Errorf("non-HTML body must pass through: %q", out)
	}

	// HTTP status is reported
	if res, _ := runWebFetch(context.Background(), map[string]any{"url": srv.URL + "/missing"}); !strings.Contains(res, `"exit_code":404`) {
		t.Errorf("404 must surface as exit code: %s", res)
	}
}

func TestRunWebFetch_Truncates(t *testing.T) {
	srv := webFetchServer(t)
	defer srv.Close()
	res, _ := runWebFetch(context.Background(), map[string]any{"url": srv.URL + "/big", "max_bytes": 100})
	if !strings.Contains(res, "truncated at max_bytes=100") {
		t.Errorf("expected truncation marker: %s", tail(res, 200))
	}
}

// http_get is a deprecated alias that still works.
func TestHTTPGet_AliasStillWorks(t *testing.T) {
	srv := webFetchServer(t)
	defer srv.Close()
	res, _ := dispatchTool(context.Background(), "http_get", `{"url":"`+srv.URL+`/data"}`, nil, nil, memory.ConsumerAll, "", nil)
	if isToolError(res) || !strings.Contains(res, "v") {
		t.Errorf("http_get alias must dispatch to web_fetch: %s", res)
	}
}

func postEditAgent(cmd string) *Agent {
	return (&Agent{skipPerms: true, toolTimeout: 5 * time.Second}).WithPostEditCommand(cmd)
}

func editToolCall(f, oldS, newS string) toolCall {
	return toolCall{ID: "e1", Function: toolCallFunction{Name: "edit_file",
		Arguments: `{"path":"` + f + `","old_string":"` + oldS + `","new_string":"` + newS + `"}`}}
}

// The post-edit command runs after a successful edit_file/write_file and its
// output is appended to the tool result (toolset-review G2).
func TestPostEditCheck_RunsAfterEdits(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{CWD: dir}
	a := postEditAgent("echo POSTEDIT-OK")

	out := a.dispatchOneTool(context.Background(), editToolCall(f, "one", "two"), 0, "", "", io.Discard, sess, nil, nil)
	if !strings.Contains(out.msg.Content, "[post-edit check") || !strings.Contains(out.msg.Content, "POSTEDIT-OK") {
		t.Errorf("post-edit output missing from edit_file result: %s", out.msg.Content)
	}

	writeCall := toolCall{ID: "w1", Function: toolCallFunction{Name: "write_file",
		Arguments: `{"path":"` + f + `","content":"three\n"}`}}
	out = a.dispatchOneTool(context.Background(), writeCall, 0, "", "", io.Discard, sess, nil, nil)
	if !strings.Contains(out.msg.Content, "POSTEDIT-OK") {
		t.Errorf("post-edit output missing from write_file result: %s", out.msg.Content)
	}

	// read_file is untouched.
	readCall := toolCall{ID: "r1", Function: toolCallFunction{Name: "read_file",
		Arguments: `{"path":"` + f + `"}`}}
	out = a.dispatchOneTool(context.Background(), readCall, 0, "", "", io.Discard, sess, nil, nil)
	if strings.Contains(out.msg.Content, "post-edit check") {
		t.Errorf("read_file must not trigger the post-edit check: %s", out.msg.Content)
	}

	// A failed edit does not run the check.
	badEdit := editToolCall(f, "nope", "x")
	out = a.dispatchOneTool(context.Background(), badEdit, 0, "", "", io.Discard, sess, nil, nil)
	if isToolError(out.msg.Content) && strings.Contains(out.msg.Content, "post-edit check") {
		t.Errorf("failed edit must not run the post-edit check: %s", out.msg.Content)
	}

	// The command runs in the session cwd.
	a = postEditAgent("pwd")
	out = a.dispatchOneTool(context.Background(), editToolCall(f, "three", "FOUR"), 0, "", "", io.Discard, sess, nil, nil)
	if !strings.Contains(out.msg.Content, dir) {
		t.Errorf("post-edit command must run in the session cwd %q: %s", dir, out.msg.Content)
	}

	// A failing check reports its exit code.
	a = postEditAgent("exit 3")
	out = a.dispatchOneTool(context.Background(), editToolCall(f, "FOUR", "five"), 0, "", "", io.Discard, sess, nil, nil)
	if !strings.Contains(out.msg.Content, "exit 3") {
		t.Errorf("failing check must report the exit code: %s", out.msg.Content)
	}

	// With no command configured, results are unchanged.
	a = &Agent{skipPerms: true, toolTimeout: 5 * time.Second}
	out = a.dispatchOneTool(context.Background(), editToolCall(f, "five", "six"), 0, "", "", io.Discard, sess, nil, nil)
	if strings.Contains(out.msg.Content, "post-edit check") {
		t.Errorf("no command configured — result must be untouched: %s", out.msg.Content)
	}
}
