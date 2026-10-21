package main

// Multimodal session/prompt input (ADR-0074): ACP's ContentBlock union lets a
// client paste/attach images, audio and resource links alongside text. This
// file is the one place prompt blocks are taken apart — text is assembled for
// the model prompt, image blocks become milk's standard attachment payload
// (the same PendingAttachment shape /attach and clipboard paste produce, so
// the TUI's whole multimodal path is reused: multipart vision parts for
// local-provider agents via local.WithImageParts, @temp-file references for a
// CLI (claude) runner), and everything else degrades to a compact placeholder
// line plus a client-visible note. No payload ever lands in session history:
// the assembled text carries placeholders only.

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/transport/acp"
)

// acpPromptMedia is one session/prompt's non-text content plus the assembled
// prompt text. A nil *acpPromptMedia (synthetic follow-up prompts) means
// "plain text, nothing to attach"; all methods tolerate it.
type acpPromptMedia struct {
	// echo is the prompt's content blocks as the client sent them, for the
	// accept-echo (user_message upsert): what the client pasted round-trips
	// unchanged so its UI can render the image it just attached.
	echo []acp.ContentBlock
	// images are the decoded image blocks, in order, in milk's attachment
	// shape (PendingAttachment — same as /attach).
	images []PendingAttachment
	// notes are client-visible notices for content that could not be
	// forwarded to a model (audio, undecodable payloads, open-set kinds).
	notes []string
}

// parseACPPrompt takes one session/prompt's content blocks apart. Text-only
// prompts return media == nil so every caller keeps its old code path
// (plain string in, plain echo out); any non-text block yields a media
// handle carrying the echo blocks and the decoded attachments.
func parseACPPrompt(blocks []acp.ContentBlock) (string, *acpPromptMedia) {
	var sb strings.Builder
	var texts int
	for _, b := range blocks {
		if b.Type == "text" {
			texts++
			sb.WriteString(b.Text)
		}
	}
	hasNonText := texts != len(blocks)
	if !hasNonText {
		return sb.String(), nil
	}

	m := &acpPromptMedia{echo: blocks}
	seq := 0 // shared counter so every synthesized name is unique
	// Re-walk with the text interleaved so each placeholder lands where its
	// block sat in the prompt.
	sb.Reset()
	for _, b := range blocks {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)

		case "image":
			att, err := decodeACPImage(b, seq)
			if err != nil {
				fmt.Fprintf(&sb, "\n[attached image: unreadable — %v]\n", err)
				m.notes = append(m.notes, fmt.Sprintf("an image attachment was dropped: %v", err))
				continue
			}
			if !att.isImage() {
				// An image block whose payload is not a raster image (a PDF,
				// an SVG, junk) must never become an image_url part — most
				// backends hard-400 on non-image payloads there. Textual
				// payloads (SVG) inline as text; anything else is noted.
				if looksTextual(att.Data) {
					seq++
					fmt.Fprintf(&sb, "\n[attached: %s (%s)]\n```\n%s\n```\n", att.Name, att.MIMEType, boundedInline(att.Data))
				} else {
					fmt.Fprintf(&sb, "\n[attached: %s (%s, binary, %d bytes) — not forwarded to the model]\n", att.Name, orDefault(att.MIMEType, "binary"), len(att.Data))
					m.notes = append(m.notes, fmt.Sprintf("the attachment %s (%s) is not an image payload and was not forwarded to the model", att.Name, att.MIMEType))
				}
				continue
			}
			seq++
			m.images = append(m.images, att)
			fmt.Fprintf(&sb, "\n[attached image: %s (%s)]\n", att.Name, att.MIMEType)

		case "audio":
			seq++
			name := fmt.Sprintf("audio-%d%s", seq, mimeExtension(b.MimeType))
			fmt.Fprintf(&sb, "\n[attached audio: %s (%s) — not forwarded to the model]\n", name, orDefault(b.MimeType, "audio"))
			m.notes = append(m.notes, "audio attachments are not forwarded to the model — milk only supports image input today")

		case "resource_link":
			fmt.Fprintf(&sb, "\n%s\n", acpResourceLine(b.Resource))
			if b.Resource != nil && b.Resource.Text != "" {
				fmt.Fprintf(&sb, "```\n%s\n```\n", b.Resource.Text)
			} else if b.Resource != nil && b.Resource.Blob != "" {
				m.notes = append(m.notes, fmt.Sprintf("the linked resource %s carries binary content that is not forwarded to the model", b.Resource.URI))
			}

		default:
			fmt.Fprintf(&sb, "\n[content block of type %q — not forwarded to the model]\n", b.Type)
			m.notes = append(m.notes, fmt.Sprintf("a content block of type %q was not forwarded to the model", b.Type))
		}
	}
	return strings.TrimSpace(sb.String()), m
}

// boundedInline caps inline payload text at the same bound as text
// attachments before it goes into the prompt.
func boundedInline(data []byte) string {
	body := string(data)
	if len(body) > maxInlinedAttachmentChars {
		return body[:maxInlinedAttachmentChars] + "\n[... truncated]"
	}
	return body
}

// decodeACPImage turns one ACP image block into milk's attachment shape. Per
// the spec block.Data is bare base64; a full data: URI is tolerated on the way
// in (some clients send one). An empty mimeType is sniffed from the bytes.
func decodeACPImage(b acp.ContentBlock, seq int) (PendingAttachment, error) {
	data := []byte(nil)
	mimeType := b.MimeType
	if raw, mime, ok := decodeDataURI(b.Data); ok {
		data, mimeType = raw, orDefault(mimeType, mime)
	} else if b.Data != "" {
		raw, err := base64.StdEncoding.DecodeString(b.Data)
		if err != nil {
			return PendingAttachment{}, fmt.Errorf("payload is not base64: %w", err)
		}
		data = raw
	}
	if len(data) == 0 {
		return PendingAttachment{}, fmt.Errorf("empty payload")
	}
	if len(data) > maxAttachmentBytes {
		return PendingAttachment{}, fmt.Errorf("payload is %d bytes — attachments are limited to %d bytes", len(data), maxAttachmentBytes)
	}
	if mimeType == "" {
		mimeType = detectMIME("", data)
	}
	name := fmt.Sprintf("image-%d%s", seq+1, mimeExtension(mimeType))
	return PendingAttachment{Name: name, MIMEType: mimeType, Data: data}, nil
}

// acpResourceLine renders a resource_link's reference line.
func acpResourceLine(r *acp.Resource) string {
	if r == nil {
		return "[linked resource: (no uri)]"
	}
	name := r.Name
	if name == "" {
		name = r.URI
	}
	if r.MimeType != "" {
		return fmt.Sprintf("[linked resource: %s — %s (%s)]", name, r.URI, r.MimeType)
	}
	return fmt.Sprintf("[linked resource: %s — %s]", name, r.URI)
}

// imageParts returns the prompt's images as multipart vision content parts
// (base64 data URIs) for a local-provider agent — the same shape the TUI
// wires in via local.WithImageParts (issue #297: per-call ctx, never an
// instance field — the agents are shared with background work).
func (m *acpPromptMedia) imageParts() []local.ContentPart {
	if m == nil || len(m.images) == 0 {
		return nil
	}
	parts := make([]local.ContentPart, 0, len(m.images))
	for _, a := range m.images {
		parts = append(parts, local.ContentPart{
			Type:     "image_url",
			ImageURL: &local.ImageURLPart{URL: attachmentDataURI(a)},
		})
	}
	return parts
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
