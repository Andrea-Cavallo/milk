package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/scoutme/milk/internal/agent/local"
)

// PendingAttachment holds a file staged for inclusion in the next agent turn.
type PendingAttachment struct {
	Path     string // original file path
	Name     string // base filename
	MIMEType string // detected MIME type
	Data     []byte // file contents
}

// isImage reports whether the attachment is an image milk can deliver as a
// multipart vision payload (or a CLI @path reference). SVG is excluded: it is
// text, most vision endpoints accept only raster formats, and the model reads
// SVG source fine as an inline text attachment.
func (a PendingAttachment) isImage() bool {
	return strings.HasPrefix(a.MIMEType, "image/") && a.MIMEType != "image/svg+xml"
}

// maxAttachmentBytes caps how much payload one attachment may carry — a
// sanity bound against pasting/attaching something that exhausts memory or
// the request budget (the model-side payload cap cannot shrink a single
// oversize attachment; the request would just fail).
const maxAttachmentBytes = 32 << 20 // 32MB

// maxInlinedAttachmentChars caps how much of a text attachment is inlined
// into the prompt verbatim; beyond it the block is truncated with a marker.
const maxInlinedAttachmentChars = 200_000

// loadAttachment reads path from disk, detects its MIME type, and returns
// a PendingAttachment ready for staging. Returns an error when the file
// cannot be read or exceeds maxAttachmentBytes.
func loadAttachment(path string) (PendingAttachment, error) {
	return loadAttachmentMIME(path, "")
}

// loadAttachmentMIME is loadAttachment with the MIME type supplied by a
// caller that already knows it — the clipboard probes do (a milk temp file's
// name is a meaningless milk-paste-*.bin), and round-tripping through
// filename detection threw that knowledge away.
func loadAttachmentMIME(path, knownMIME string) (PendingAttachment, error) {
	path = expandHome(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return PendingAttachment{}, fmt.Errorf("reading %q: %w", path, err)
	}
	if len(data) > maxAttachmentBytes {
		return PendingAttachment{}, fmt.Errorf("%q is %d bytes — attachments are limited to %d bytes", path, len(data), maxAttachmentBytes)
	}
	name := filepath.Base(path)
	mime := knownMIME
	if mime == "" {
		mime = detectMIME(path, data)
	}
	return PendingAttachment{Path: path, Name: name, MIMEType: mime, Data: data}, nil
}

// expandHome replaces a leading "~/" with the user's home directory.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

// detectMIME returns a best-guess MIME type for the file at path with the
// given contents. Magic bytes win over the extension (a .png that is really
// text is text); an extension that claims a binary type is trusted only when
// the content doesn't contradict it; anything unrecognised falls back to
// text/plain for textual content and application/octet-stream otherwise —
// never raw binary dumped into the prompt as if it were text.
func detectMIME(path string, data []byte) string {
	if mime := sniffMIME(data); mime != "" {
		return mime
	}
	if mime := mimeByExt(path); mime != "" {
		if isBinaryMIME(mime) && len(data) > 0 && looksTextual(data) {
			return "text/plain"
		}
		return mime
	}
	if looksTextual(data) {
		return "text/plain"
	}
	return "application/octet-stream"
}

// sniffMIME identifies content by magic bytes, returning "" when nothing
// matches. Covers the raster formats vision endpoints accept plus PDF.
func sniffMIME(data []byte) string {
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	switch {
	case len(sniff) >= 4 && sniff[0] == 0x89 && sniff[1] == 'P' && sniff[2] == 'N' && sniff[3] == 'G':
		return "image/png"
	case len(sniff) >= 3 && sniff[0] == 0xFF && sniff[1] == 0xD8 && sniff[2] == 0xFF:
		return "image/jpeg"
	case len(sniff) >= 6 && (string(sniff[:6]) == "GIF87a" || string(sniff[:6]) == "GIF89a"):
		return "image/gif"
	case len(sniff) >= 12 && string(sniff[:4]) == "RIFF" && string(sniff[8:12]) == "WEBP":
		return "image/webp"
	case len(sniff) >= 2 && sniff[0] == 'B' && sniff[1] == 'M':
		return "image/bmp"
	case len(sniff) >= 4 && (string(sniff[:4]) == "II*\x00" || string(sniff[:4]) == "MM\x00*"):
		return "image/tiff"
	case len(sniff) >= 4 && sniff[0] == 0x00 && sniff[1] == 0x00 && sniff[2] == 0x01 && sniff[3] == 0x00:
		return "image/x-icon"
	case len(sniff) >= 4 && string(sniff[:4]) == "%PDF":
		return "application/pdf"
	}
	return ""
}

// mimeByExt maps a file extension to a MIME type, "" when unknown.
func mimeByExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".js", ".mjs":
		return "text/javascript"
	case ".ts":
		return "text/typescript"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "text/yaml"
	case ".toml":
		return "text/toml"
	case ".md":
		return "text/markdown"
	case ".txt", ".log":
		return "text/plain"
	case ".sh", ".bash", ".zsh":
		return "text/x-shellscript"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	}
	return ""
}

// looksTextual reports whether data reads as text: no NUL bytes and valid
// UTF-8 in the first 8KB (the part inlined into prompts and history).
func looksTextual(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sniff := data
	if len(sniff) > 8192 {
		sniff = sniff[:8192]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return false
	}
	return utf8.Valid(sniff)
}

// attachmentContextBlock returns the context block injected into the
// agent context for text-based attachments. PDF and other binary non-image
// types are noted as binary with a size hint rather than dumped verbatim.
func attachmentContextBlock(a PendingAttachment) string {
	if a.isImage() {
		// Images go through image parts (or CLI @path staging), not context blocks.
		return fmt.Sprintf("[attached image: %s]\n", a.Name)
	}
	if a.MIMEType == "application/pdf" || isBinaryMIME(a.MIMEType) || !looksTextual(a.Data) {
		return fmt.Sprintf("[attached file: %s (binary, %d bytes) — text extraction not supported]\n", a.Name, len(a.Data))
	}
	body := string(a.Data)
	if len(body) > maxInlinedAttachmentChars {
		body = body[:maxInlinedAttachmentChars] + fmt.Sprintf("\n[... attachment truncated at %d chars — read %s for the rest]", maxInlinedAttachmentChars, a.Path)
	}
	return fmt.Sprintf("[attached file: %s]\n```\n%s\n```\n", a.Name, body)
}

// attachmentDataURI returns a data URI for an image attachment, suitable for
// injection into multipart content or escalation context blocks.
func attachmentDataURI(a PendingAttachment) string {
	enc := base64.StdEncoding.EncodeToString(a.Data)
	return fmt.Sprintf("data:%s;base64,%s", a.MIMEType, enc)
}

// decodeDataURI reverses attachmentDataURI: given "data:<mime>;base64,<data>",
// returns the decoded bytes and mime type. ok is false for anything else
// (e.g. a malformed or non-base64 URI).
func decodeDataURI(uri string) (data []byte, mimeType string, ok bool) {
	rest, found := strings.CutPrefix(uri, "data:")
	if !found {
		return nil, "", false
	}
	mime, b64, found := strings.Cut(rest, ";base64,")
	if !found {
		return nil, "", false
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", false
	}
	return raw, mime, true
}

// isBinaryMIME reports whether mime indicates a binary (non-text) type.
func isBinaryMIME(mime string) bool {
	return !strings.HasPrefix(mime, "text/") &&
		mime != "application/json" &&
		mime != "image/svg+xml" &&
		!strings.HasSuffix(mime, "+json") &&
		!strings.HasSuffix(mime, "+xml")
}

// unquoteFilePath strips surrounding single quotes added by terminals that
// shell-quote paths on drag-and-drop (e.g. GNOME Terminal pastes '/path/to/file').
func unquoteFilePath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		s = s[1 : len(s)-1]
	}
	return s
}

// looksLikeFilePath returns true when s has a file-path prefix — but does NOT
// check whether the path actually exists. Used to decide whether to insert
// @path into the input area when the user declines to attach.
func looksLikeFilePath(s string) bool {
	s = unquoteFilePath(s)
	return strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "~/") ||
		strings.HasPrefix(s, `\\`) // Windows UNC / network path
}

// isLikelyFilePath reports whether s looks like an absolute file path that
// exists on disk. It must start with "/" or "~/", and after home-expansion the
// path must exist.
func isLikelyFilePath(s string) bool {
	s = unquoteFilePath(s)
	if !looksLikeFilePath(s) {
		return false
	}
	// Single "/" is not a file attachment.
	if s == "/" {
		return false
	}
	expanded := expandHome(s)
	info, err := os.Stat(expanded)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// attachmentTaskBlock returns a short file-reference block for injection into a
// workflow task string. Attachments are referenced by their on-disk path rather
// than inlined: workflow task text passes through interp's variable budget and
// prompt render caps (truncateLargeVarsWithBudget / maxRenderedPromptChars),
// which would mangle base64 image data or large file contents. Path references
// stay short and remain resolvable by every role on every pass — the referenced
// files are the workflow-owned copies materializeWorkflowAttachments made and
// stay on disk for the lifetime of the workflow run.
func attachmentTaskBlock(attachments []PendingAttachment) string {
	if len(attachments) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Attachments\n\n")
	for _, a := range attachments {
		fmt.Fprintf(&b, "- %s (%s, %d bytes) — file: %s\n", a.Name, a.MIMEType, len(a.Data), a.Path)
	}
	b.WriteString("\nAttachments stay on disk at the paths above — read or reference them as needed (e.g. as visual reference for image generation).\n")
	return b.String()
}

// workflowTaskWithAttachments appends the attachmentTaskBlock for staged
// attachments to a workflow task string. With no attachments the task is
// returned unchanged.
func workflowTaskWithAttachments(task string, attachments []PendingAttachment) string {
	if len(attachments) == 0 {
		return task
	}
	return task + "\n\n" + attachmentTaskBlock(attachments)
}

// materializeWorkflowAttachments copies each staged attachment into a fresh
// workflow-owned temp directory and rewrites its Path to the copy, returning
// the updated attachments and the directory for the caller to remove when the
// workflow ends. Task text and CLI @path references name these files for the
// whole run, and a clipboard-pasted attachment's original path is a milk temp
// file that no longer exists by dispatch time — the in-memory Data is its only
// durable source. Never fails the workflow: an attachment that cannot be
// written keeps its original path (the task block still names it).
func materializeWorkflowAttachments(attachments []PendingAttachment) ([]PendingAttachment, string) {
	if len(attachments) == 0 {
		return attachments, ""
	}
	dir, err := os.MkdirTemp("", "milk-workflow-attach-*")
	if err != nil {
		return attachments, ""
	}
	out := make([]PendingAttachment, 0, len(attachments))
	for i, a := range attachments {
		dst := filepath.Join(dir, fmt.Sprintf("%d-%s", i, a.Name))
		if err := os.WriteFile(dst, a.Data, 0o600); err != nil {
			out = append(out, a)
			continue
		}
		a.Path = dst
		out = append(out, a)
	}
	return out, dir
}

// attachmentPlaceholder returns the compact placeholder stored in session
// history in place of attachment data.
func attachmentPlaceholder(a PendingAttachment) string {
	return fmt.Sprintf("[attached: %s]", a.Name)
}

// stageCLIImageRefs stages image content parts (base64 data URIs) as temp
// files and returns the @path block a CLI (claude) runner resolves natively —
// the one delivery path for images on a runner that can't take multipart
// vision parts. The runner that actually handles the turn calls this (see
// cliRunner.Execute / cliRunner.RunToolCall), so a local model never sees
// @path noise and the files are guaranteed alive for the whole turn
// (self-escalation and permission retries included) — the returned cleanup
// runs when the turn or call ends. A part that cannot be staged falls back to
// an inline data URI, which claude also understands in prompt text.
func stageCLIImageRefs(parts []local.ContentPart) (string, func()) {
	var b strings.Builder
	var files []string
	cleanup := func() {
		for _, p := range files {
			os.Remove(p) //nolint:errcheck
		}
	}
	for _, part := range parts {
		if part.ImageURL == nil {
			continue
		}
		data, mime, ok := decodeDataURI(part.ImageURL.URL)
		if !ok {
			continue
		}
		f, err := os.CreateTemp("", "milk-img-*"+mimeExtension(mime))
		if err == nil {
			if _, werr := f.Write(data); werr == nil {
				f.Close()
				files = append(files, f.Name())
				fmt.Fprintf(&b, "@%s\n", f.Name())
				continue
			}
			f.Close()
			os.Remove(f.Name()) //nolint:errcheck
		}
		fmt.Fprintf(&b, "[attached image]\n%s\n\n", part.ImageURL.URL)
	}
	return b.String(), cleanup
}
