package local

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/scoutme/milk/internal/session"
)

// A tool-returned image (e.g. an MCP full-page screenshot) is shrunk before it
// is added to the conversation, so the next request carries the small copy.
func TestToolImageIsShrunkBeforeNextRequest(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4000, 4000))
	for y := 0; y < 4000; y++ {
		for x := 0; x < 4000; x++ {
			if (x/50+y/50)%2 == 0 {
				img.SetRGBA(x, y, color.RGBA{20, 40, 200, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{240, 240, 240, 255})
			}
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	// Pad with noise-free tail is unnecessary: 4000 px alone exceeds the cap.
	big := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	var secondBody atomic.Value
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc1","function":{"name":"mcp_shot","arguments":"{}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body) //nolint:errcheck
		secondBody.Store(buf.String())
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := New(srv.URL, "test-model")
	a.supportsVision = true
	a.WithMCPToolSet(fakeImageToolSet{img: big})
	var out strings.Builder
	if _, err := a.Run(context.Background(), nil, "take a screenshot", &out, &session.Session{CWD: "/tmp"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := secondBody.Load().(string)
	if body == "" {
		t.Fatal("no second request captured")
	}
	if len(body) > len(big)/2 {
		t.Errorf("second request carries %d bytes; the %d-byte tool image was not shrunk", len(body), len(big))
	}
}

type fakeImageToolSet struct{ img string }

func (f fakeImageToolSet) Schemas(context.Context) []map[string]any {
	return []map[string]any{{"type": "function", "function": map[string]any{
		"name": "mcp_shot", "description": "screenshot", "parameters": map[string]any{"type": "object", "properties": map[string]any{}},
	}}}
}

func (f fakeImageToolSet) Dispatch(_ context.Context, name, _ string) (string, []string, bool) {
	if name != "mcp_shot" {
		return "", nil, false
	}
	return "captured", []string{f.img}, true
}
