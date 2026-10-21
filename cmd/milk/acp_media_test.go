package main

// ACP multimodal prompt parsing (ADR-0074): text blocks assemble verbatim,
// image blocks decode into milk's PendingAttachment shape (the /attach and
// clipboard-paste shape — so the TUI's multimodal path is reused wholesale),
// everything else degrades to a compact placeholder plus a client-visible
// note. The assembled prompt text never carries a payload — it doubles as the
// session-history form.

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/transport/acp"
)

const acpTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestParseACPPrompt_TextOnly(t *testing.T) {
	text, media := parseACPPrompt([]acp.ContentBlock{acp.TextBlock("hello "), acp.TextBlock("world")})
	if media != nil {
		t.Fatalf("text-only prompt must return media == nil, got %+v", media)
	}
	if text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
}

func TestParseACPPrompt_ImageBlock(t *testing.T) {
	text, media := parseACPPrompt([]acp.ContentBlock{
		acp.TextBlock("what is in this image?"),
		acp.ImageBlock(acpTinyPNG, "image/png"),
	})
	if media == nil {
		t.Fatal("image prompt must return media")
	}
	if len(media.images) != 1 {
		t.Fatalf("images = %d, want 1", len(media.images))
	}
	att := media.images[0]
	if att.MIMEType != "image/png" || att.Name != "image-1.png" || len(att.Data) == 0 {
		t.Errorf("attachment = %+v", att)
	}
	if !strings.Contains(text, "what is in this image?") || !strings.Contains(text, "[attached image: image-1.png (image/png)]") {
		t.Errorf("assembled text missing parts: %q", text)
	}
	if strings.Contains(text, acpTinyPNG) {
		t.Errorf("assembled text carries the payload (must be placeholder-only): %q", text)
	}

	// The multipart vision part is a base64 data URI over the same bytes.
	parts := media.imageParts()
	if len(parts) != 1 || parts[0].Type != "image_url" || parts[0].ImageURL == nil {
		t.Fatalf("imageParts = %+v", parts)
	}
	if !strings.HasPrefix(parts[0].ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("image part is not a data URI: %q", parts[0].ImageURL.URL)
	}

	// The accept-echo keeps the block the client sent.
	if len(media.echo) != 2 || media.echo[1].Type != "image" || media.echo[1].Data != acpTinyPNG {
		t.Errorf("echo blocks = %+v", media.echo)
	}
}

func TestParseACPPrompt_ImageDataURITolerated(t *testing.T) {
	// The spec says block.Data is bare base64; a client sending a full data:
	// URI must still work.
	_, media := parseACPPrompt([]acp.ContentBlock{
		acp.ImageBlock("data:image/png;base64,"+acpTinyPNG, "image/png"),
	})
	if media == nil || len(media.images) != 1 {
		t.Fatalf("data-URI payload not decoded: %+v", media)
	}
	if string(media.images[0].Data) == "" {
		t.Error("decoded bytes are empty")
	}
}

func TestParseACPPrompt_ImageWithoutMIMEType(t *testing.T) {
	// An empty mimeType is sniffed from the bytes (PNG magic).
	_, media := parseACPPrompt([]acp.ContentBlock{acp.ImageBlock(acpTinyPNG, "")})
	if media == nil || len(media.images) != 1 {
		t.Fatalf("image not decoded: %+v", media)
	}
	if media.images[0].MIMEType != "image/png" {
		t.Errorf("sniffed MIME = %q, want image/png", media.images[0].MIMEType)
	}
}

func TestParseACPPrompt_UndecodableImage(t *testing.T) {
	text, media := parseACPPrompt([]acp.ContentBlock{
		acp.TextBlock("look"),
		acp.ImageBlock("!!!not base64!!!", "image/png"),
	})
	if media == nil {
		t.Fatal("must still return media (for the echo)")
	}
	if len(media.images) != 0 {
		t.Errorf("undecodable image must not be staged: %+v", media.images)
	}
	if len(media.notes) != 1 {
		t.Errorf("notes = %v, want one drop notice", media.notes)
	}
	if !strings.Contains(text, "[attached image: unreadable") {
		t.Errorf("placeholder missing: %q", text)
	}
}

func TestParseACPPrompt_AudioAndResourceLink(t *testing.T) {
	text, media := parseACPPrompt([]acp.ContentBlock{
		acp.TextBlock("check these"),
		acp.AudioBlock("YXVk", "audio/ogg"),
		acp.ResourceLinkBlock(acp.Resource{URI: "file:///tmp/notes.md", Name: "notes.md", MimeType: "text/markdown"}),
	})
	if media == nil {
		t.Fatal("must return media")
	}
	if len(media.notes) != 1 {
		t.Errorf("notes = %v, want the audio drop notice", media.notes)
	}
	if !strings.Contains(text, "[attached audio:") || !strings.Contains(text, "[linked resource: notes.md — file:///tmp/notes.md (text/markdown)]") {
		t.Errorf("placeholders missing: %q", text)
	}
}

func TestParseACPPrompt_ResourceTextInlined(t *testing.T) {
	text, media := parseACPPrompt([]acp.ContentBlock{
		acp.ResourceLinkBlock(acp.Resource{URI: "file:///tmp/x.txt", Name: "x.txt", Text: "inline contents"}),
	})
	if media == nil {
		t.Fatal("must return media")
	}
	if !strings.Contains(text, "inline contents") {
		t.Errorf("resource text not inlined: %q", text)
	}
}

func TestParseACPPrompt_ImageOnly(t *testing.T) {
	// An image-only prompt must still assemble a non-empty prompt text (the
	// placeholder) so routing/loop detection see a real user turn.
	text, media := parseACPPrompt([]acp.ContentBlock{acp.ImageBlock(acpTinyPNG, "image/png")})
	if media == nil || len(media.images) != 1 {
		t.Fatalf("images = %+v", media)
	}
	if strings.TrimSpace(text) == "" {
		t.Error("assembled text is empty")
	}
}

// TestStageCLIImageRefs_StagesTempFiles pins the runner-side CLI delivery:
// an ACP image's multipart parts become @temp-file references at dispatch
// time (stageCLIImageRefs — shared with the TUI and tool-agent calls), never
// payloads in the prompt.
func TestStageCLIImageRefs_StagesTempFiles(t *testing.T) {
	_, media := parseACPPrompt([]acp.ContentBlock{
		acp.TextBlock("look"),
		acp.ImageBlock(base64.StdEncoding.EncodeToString([]byte("png-bytes")), "image/png"),
	})
	block, cleanup := stageCLIImageRefs(media.imageParts())
	defer cleanup()
	if !strings.Contains(block, "@") || !strings.Contains(block, "milk-img-") {
		t.Errorf("expected an @path reference, got %q", block)
	}
	if strings.Contains(block, "png-bytes") {
		t.Errorf("payload leaked into the CLI block: %q", block)
	}
}

// TestParseACPPrompt_NonRasterImageBlockNotForwarded pins the payload-type
// guard: an image block whose bytes are not a raster image (a PDF, junk) must
// never become an image_url part — most backends hard-400 on that — and the
// client hears about the drop.
func TestParseACPPrompt_NonRasterImageBlockNotForwarded(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 binary\x00\x01\x02"))
	text, media := parseACPPrompt([]acp.ContentBlock{
		acp.TextBlock("read this"),
		acp.ImageBlock(pdf, "application/pdf"),
	})
	if len(media.images) != 0 {
		t.Errorf("non-raster payload must not become an image part: %+v", media.images)
	}
	if !strings.Contains(text, "not forwarded to the model") {
		t.Errorf("expected a placeholder noting the drop, got %q", text)
	}
	if len(media.notes) == 0 {
		t.Error("expected a client-visible note")
	}
}

// TestParseACPPrompt_SVGImageBlockInlinesAsText pins the textual-payload
// route: an SVG arrives as an image block but is text — it inlines as a code
// fence instead of riding the vision path.
func TestParseACPPrompt_SVGImageBlockInlinesAsText(t *testing.T) {
	svg := base64.StdEncoding.EncodeToString([]byte("<svg><rect/></svg>"))
	text, media := parseACPPrompt([]acp.ContentBlock{acp.ImageBlock(svg, "image/svg+xml")})
	if len(media.images) != 0 {
		t.Errorf("svg must not become a vision part: %+v", media.images)
	}
	if !strings.Contains(text, "<svg><rect/></svg>") {
		t.Errorf("expected svg source inlined, got %q", text)
	}
}

// TestParseACPPrompt_OversizeImageRejected pins the attachment size cap.
func TestParseACPPrompt_OversizeImageRejected(t *testing.T) {
	big := make([]byte, maxAttachmentBytes+1)
	big[0], big[1], big[2], big[3] = 0x89, 'P', 'N', 'G'
	_, media := parseACPPrompt([]acp.ContentBlock{
		acp.ImageBlock(base64.StdEncoding.EncodeToString(big), "image/png"),
	})
	if len(media.images) != 0 {
		t.Error("oversize payload must not be accepted")
	}
	if len(media.notes) == 0 {
		t.Error("expected a client-visible note for the oversize drop")
	}
}
