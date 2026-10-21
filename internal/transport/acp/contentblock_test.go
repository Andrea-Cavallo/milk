package acp

// ContentBlock union pins: the image/audio/resource_link payload fields the
// ACP schema defines on each block kind, open-set pass-through of fields milk
// doesn't model (a received block must round-trip losslessly — it is echoed
// back as the user message's accept-echo), and tolerance for the flat
// resource_link shape alongside the nested one.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContentBlock_ImageRoundTrip(t *testing.T) {
	blk := ImageBlock("aGVsbG8=", "image/png")
	raw, err := json.Marshal(blk)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(raw)
	for _, want := range []string{`"type":"image"`, `"data":"aGVsbG8="`, `"mimeType":"image/png"`} {
		if !strings.Contains(s, want) {
			t.Errorf("marshalled block missing %s: %s", want, s)
		}
	}

	var got ContentBlock
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != "image" || got.Data != "aGVsbG8=" || got.MimeType != "image/png" {
		t.Errorf("round trip lost payload: %+v", got)
	}
}

func TestContentBlock_AudioFields(t *testing.T) {
	raw := `{"type":"audio","data":"YXVk","mimeType":"audio/ogg"}`
	var got ContentBlock
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != "audio" || got.Data != "YXVk" || got.MimeType != "audio/ogg" {
		t.Errorf("audio payload lost: %+v", got)
	}
}

func TestContentBlock_NestedResourceLink(t *testing.T) {
	raw := `{"type":"resource_link","resource":{"uri":"file:///tmp/a.png","name":"a.png","mimeType":"image/png","size":123}}`
	var got ContentBlock
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Resource == nil || got.Resource.URI != "file:///tmp/a.png" || got.Resource.Name != "a.png" ||
		got.Resource.MimeType != "image/png" || got.Resource.Size != 123 {
		t.Fatalf("resource payload lost: %+v", got.Resource)
	}

	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), `"resource":`) || !strings.Contains(string(out), "file:///tmp/a.png") {
		t.Errorf("nested resource_link did not re-marshal nested: %s", out)
	}
}

func TestContentBlock_FlatResourceLinkFolded(t *testing.T) {
	// Some clients emit the flat shape (uri/name at the block's top level)
	// instead of nesting under "resource"; the modelled view must fold it.
	raw := `{"type":"resource_link","uri":"file:///tmp/b.txt","name":"b.txt","mimeType":"text/plain"}`
	var got ContentBlock
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Resource == nil || got.Resource.URI != "file:///tmp/b.txt" || got.Resource.Name != "b.txt" {
		t.Fatalf("flat resource_link not folded: %+v", got)
	}
	// The client's own keys survive for a faithful echo.
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), `"uri":"file:///tmp/b.txt"`) {
		t.Errorf("flat keys lost on re-marshal: %s", out)
	}
}

func TestContentBlock_UnknownFieldsPassThrough(t *testing.T) {
	raw := `{"type":"tool_result","data":"payload","_meta":{"milk":"x"},"annotations":{"audience":["user"]}}`
	var got ContentBlock
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"type":"tool_result"`, `"data":"payload"`, `"_meta"`, `"milk":"x"`, `"annotations"`} {
		if !strings.Contains(s, want) {
			t.Errorf("open-set block lost %s on round trip: %s", want, s)
		}
	}
}

func TestResource_UnknownFieldsPassThrough(t *testing.T) {
	raw := `{"type":"resource_link","resource":{"uri":"file:///x","name":"x","_meta":{"k":"v"}}}`
	var got ContentBlock
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), `"k":"v"`) {
		t.Errorf("resource-level _meta lost on round trip: %s", out)
	}
}

func TestContentBlock_TextMarshalShapeUnchanged(t *testing.T) {
	// The common text block must stay exactly the shape it always was —
	// every other surface (chunks, upserts, tool-call content) marshals it.
	raw, err := json.Marshal(TextBlock("hi"))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(raw) != `{"type":"text","text":"hi"}` {
		t.Errorf("text block shape changed: %s", raw)
	}
}
