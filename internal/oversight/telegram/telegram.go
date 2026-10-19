// Package telegram implements the oversight.Notifier interface using the
// Telegram Bot API. Messages are sent via plain HTTPS to the sendMessage
// endpoint; updates are routed by a single background polling goroutine.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scoutme/milk/internal/oversight"
)

const (
	defaultAPIBase     = "https://api.telegram.org"
	defaultPollTimeout = 20 * time.Second
	// maxMessageUnits is the per-message budget in UTF-16 code units (the
	// unit Telegram counts for its 4096-character sendMessage limit), kept a
	// margin below 4096 for entity-parsing subtleties. Messages longer than
	// this are split into ordered chunks — never truncated (issue #253).
	maxMessageUnits = 4000
	// sendQueueSize bounds the outbound queue; the single worker goroutine
	// drains it in FIFO order so multi-chunk messages arrive in order.
	sendQueueSize = 256
)

// Config holds the Telegram-specific configuration.
type Config struct {
	Token         string
	ChatID        int64
	PermTimeout   time.Duration // how long AskPermission waits for a reply
	TimeoutAction string        // "allow" or "deny" when PermTimeout expires
	APIBase       string        // override for testing; defaults to https://api.telegram.org
}

// Notifier sends notifications and forwards permission prompts via Telegram.
// A single background goroutine (started by StartPolling) receives all
// incoming messages and routes them to either a pending permission wait or
// the registered OnInput callback.
type Notifier struct {
	cfg    Config
	client *http.Client

	mu         sync.Mutex
	lastUpdate int64        // getUpdates offset
	permCh     chan string  // non-nil when AskPermission is waiting
	onInput    func(string) // called for non-permission messages when set

	outCh   chan string // outbound FIFO, drained by one worker goroutine
	outOnce sync.Once
}

// New creates a Notifier. Call StartPolling to begin receiving messages.
func New(cfg Config) *Notifier {
	if cfg.APIBase == "" {
		cfg.APIBase = defaultAPIBase
	}
	if cfg.PermTimeout <= 0 {
		cfg.PermTimeout = 2 * time.Minute
	}
	if cfg.TimeoutAction == "" {
		cfg.TimeoutAction = "deny"
	}
	return &Notifier{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
		outCh:  make(chan string, sendQueueSize),
	}
}

// SetOnInput registers a callback invoked for every incoming message that is
// not consumed by a pending AskPermission call. Safe to call before or after
// StartPolling. Pass nil to unregister.
func (n *Notifier) SetOnInput(cb func(string)) {
	n.mu.Lock()
	n.onInput = cb
	n.mu.Unlock()
}

// StartPolling launches the background update-polling loop. It runs until ctx
// is cancelled. Safe to call once after New.
func (n *Notifier) StartPolling(ctx context.Context) {
	go n.pollLoop(ctx)
}

// pollLoop is the single getUpdates consumer. It routes each message to either
// the pending permission channel or the onInput callback.
func (n *Notifier) pollLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		n.mu.Lock()
		offset := n.lastUpdate + 1
		n.mu.Unlock()

		params := url.Values{
			"offset":  []string{strconv.FormatInt(offset, 10)},
			"timeout": []string{strconv.Itoa(int(defaultPollTimeout.Seconds()))},
			"limit":   []string{"10"},
		}

		var result struct {
			OK     bool `json:"ok"`
			Result []struct {
				UpdateID int64 `json:"update_id"`
				Message  *struct {
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				} `json:"message"`
			} `json:"result"`
		}

		pollCtx, cancel := context.WithTimeout(ctx, defaultPollTimeout+5*time.Second)
		err := n.apiGet(pollCtx, "getUpdates", params, &result)
		cancel()

		if err != nil || !result.OK {
			// Back off briefly on error to avoid hammering the API.
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}

		for _, upd := range result.Result {
			n.mu.Lock()
			if upd.UpdateID > n.lastUpdate {
				n.lastUpdate = upd.UpdateID
			}
			permCh := n.permCh
			cb := n.onInput
			n.mu.Unlock()

			if upd.Message == nil || upd.Message.Chat.ID != n.cfg.ChatID || upd.Message.Text == "" {
				continue
			}
			text := upd.Message.Text

			if permCh != nil {
				// Non-blocking send — if the channel is full the permission
				// waiter already received a reply; treat as input.
				select {
				case permCh <- text:
					continue
				default:
				}
			}
			if cb != nil {
				go cb(text)
			}
		}
	}
}

// Ping sends a getMe request and returns an error if unreachable or token invalid.
func (n *Notifier) Ping(ctx context.Context) error {
	var result struct {
		OK bool `json:"ok"`
	}
	if err := n.apiGet(ctx, "getMe", nil, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram: getMe returned ok=false")
	}
	return nil
}

func (n *Notifier) NotifyTurnStart(_ context.Context, agent, target, prompt string) {
	short := trimPreview(prompt, 117, "...")
	msg := fmt.Sprintf("🔄 *%s* → %s\n`%s`", escMD(agent), escMD(target), escMD(short))
	n.sendAsync(msg)
}

func (n *Notifier) NotifyToolUse(_ context.Context, toolName, summary string) {
	var msg string
	if summary != "" {
		msg = fmt.Sprintf("⚙ *%s*: `%s`", escMD(toolName), escMD(summary))
	} else {
		msg = fmt.Sprintf("⚙ *%s*", escMD(toolName))
	}
	n.sendAsync(msg)
}

func (n *Notifier) NotifyToolResult(_ context.Context, toolName, summary string, isError bool) {
	if summary == "" {
		return
	}
	// Tool-result summaries stay a summary (per issue #253's scope) — capped,
	// but the cap is rune-safe so it can never split a multi-byte character.
	summary = trimPreview(summary, 497, "\n…")
	icon := "📤"
	if isError {
		icon = "❌"
	}
	n.sendAsync(fmt.Sprintf("%s *%s*\n`%s`", icon, escMD(toolName), escMD(summary)))
}

func (n *Notifier) NotifyTurnDone(_ context.Context, agent string, err error) {
	if err != nil {
		n.sendAsync(fmt.Sprintf("❌ *%s* error: `%s`", escMD(agent), escMD(err.Error())))
	}
}

// NotifyResponse relays the agent's response in full. Text longer than one
// Telegram message is split into ordered chunks (each its own sendMessage),
// never trimmed — see issue #253. Chunks go through the FIFO send queue, so
// they arrive in order, as do back-to-back notifications.
func (n *Notifier) NotifyResponse(_ context.Context, agent, text string) {
	if text == "" {
		return
	}
	header := "💬 *" + escMD(agent) + "*"
	// Reserve room for the worst-case header suffix " (n/m)" on chunked sends
	// so that header + escaped chunk always fits the Telegram limit.
	budget := maxMessageUnits - utf16Len(header) - 10
	if budget < 256 {
		budget = 256
	}
	chunks := chunkMessage(text, budget)
	for i, chunk := range chunks {
		h := header
		if len(chunks) > 1 {
			h = fmt.Sprintf("💬 *%s* (%d/%d)", escMD(agent), i+1, len(chunks))
		}
		n.sendAsync(h + "\n" + escMD(chunk))
	}
}

// AskPermission sends a formatted permission prompt and waits for a y/n reply
// via the background poll loop. answered is true only for a real reply; a
// timeout (cfg.PermTimeout or ctx) is silence — the returned decision then
// reflects cfg.TimeoutAction, and it is the caller (cmd/milk's ask layer) that
// decides whether that resolves the ask (foreground) or keeps it open until
// its own deadline (background asks — see ADR-0052).
func (n *Notifier) AskPermission(ctx context.Context, req oversight.PermRequest) (oversight.PermDecision, bool) {
	lines := []string{fmt.Sprintf("🔐 Permission request — *%s*", escMD(req.ToolName))}
	if req.Input != "" {
		lines = append(lines, fmt.Sprintf("`%s`", escMD(req.Input)))
	}
	if req.BlockedPath != "" {
		lines = append(lines, fmt.Sprintf("path: `%s`", escMD(req.BlockedPath)))
	}
	if req.Description != "" {
		lines = append(lines, escMD(req.Description))
	}
	lines = append(lines, "\nReply *y* to allow or *n* to deny\\.")
	n.send(strings.Join(lines, "\n"))

	ch := make(chan string, 1)
	n.mu.Lock()
	n.permCh = ch
	n.mu.Unlock()

	defer func() {
		n.mu.Lock()
		n.permCh = nil
		n.mu.Unlock()
	}()

	select {
	case reply := <-ch:
		r := strings.TrimSpace(strings.ToLower(reply))
		if r == "y" || r == "yes" {
			n.sendAsync("✅ Allowed")
			return oversight.PermAllow, true
		}
		n.sendAsync("🚫 Denied")
		return oversight.PermDeny, true
	case <-time.After(n.cfg.PermTimeout):
	case <-ctx.Done():
	}

	action := "Denied (timeout)"
	if n.cfg.TimeoutAction == "allow" {
		action = "Allowed (timeout)"
	}
	n.sendAsync(fmt.Sprintf("⏱ %s", action))
	if n.cfg.TimeoutAction == "allow" {
		return oversight.PermAllow, false
	}
	return oversight.PermTimeout, false
}

// AskInput sends an ask_user question and waits for a free-text reply via the
// background poll loop — the same single prompt slot as AskPermission (the
// caller serializes remote asks; see cmd/milk's oversight ask mutex). The
// reply is the answer exactly as typed. Silence (cfg.PermTimeout or ctx) is
// not an answer: answered is false and the caller keeps waiting on its other
// surfaces or the timed answer — unlike permissions, questions have no
// timeout action.
func (n *Notifier) AskInput(ctx context.Context, req oversight.InputRequest) (string, bool) {
	lines := []string{fmt.Sprintf("❓ *%s*", escMD(req.Question))}
	if len(req.Options) > 0 {
		opts := make([]string, len(req.Options))
		for i, o := range req.Options {
			opts[i] = escMD(o)
		}
		lines = append(lines, "Options: "+strings.Join(opts, " · "))
	}
	lines = append(lines, "\nReply with your answer\\.")
	n.send(strings.Join(lines, "\n"))

	ch := make(chan string, 1)
	n.mu.Lock()
	n.permCh = ch
	n.mu.Unlock()

	defer func() {
		n.mu.Lock()
		n.permCh = nil
		n.mu.Unlock()
	}()

	select {
	case reply := <-ch:
		return reply, true
	case <-time.After(n.cfg.PermTimeout):
	case <-ctx.Done():
	}
	return "", false
}

// send sends a MarkdownV2-formatted message synchronously, ignoring errors.
func (n *Notifier) send(text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payload := map[string]any{
		"chat_id":    n.cfg.ChatID,
		"text":       text,
		"parse_mode": "MarkdownV2",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.cfg.APIBase+"/bot"+n.cfg.Token+"/sendMessage",
		bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// sendAsync enqueues text on the FIFO outbound queue, started lazily. A single
// worker goroutine drains it, so multi-chunk messages and back-to-back
// notifications always arrive in send order (a goroutine per message — the
// pre-#253 behavior — could reorder chunks).
func (n *Notifier) sendAsync(text string) {
	n.outOnce.Do(func() {
		go func() {
			for msg := range n.outCh {
				n.send(msg)
			}
		}()
	})
	select {
	case n.outCh <- text:
	default:
		// Queue full: deliver best-effort out of band rather than dropping
		// content (order may interleave only in this overflow case).
		go n.send(text)
	}
}

// apiGet calls a Telegram Bot API method with query params and decodes the JSON response.
func (n *Notifier) apiGet(ctx context.Context, method string, params url.Values, out any) error {
	u := n.cfg.APIBase + "/bot" + n.cfg.Token + "/" + method
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// escMD escapes special characters for Telegram MarkdownV2.
func escMD(s string) string {
	replacer := strings.NewReplacer(
		`_`, `\_`, `*`, `\*`, `[`, `\[`, `]`, `\]`,
		`(`, `\(`, `)`, `\)`, `~`, `\~`, "`", "\\`",
		`>`, `\>`, `#`, `\#`, `+`, `\+`, `-`, `\-`,
		`=`, `\=`, `|`, `\|`, `{`, `\{`, `}`, `\}`,
		`.`, `\.`, `!`, `\!`,
	)
	return replacer.Replace(s)
}

// utf16Len returns the length of s in UTF-16 code units — the unit Telegram
// counts against its 4096-character sendMessage limit (astral runes are 2).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// trimPreview cuts s to at most max runes — rune-safe, so it never splits a
// multi-byte character — and appends suffix to mark the cut.
func trimPreview(s string, max int, suffix string) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + suffix
}

// chunkMessage splits text into chunks whose escaped size fits maxUnits UTF-16
// code units, so header + escaped chunk always fits one Telegram message. It
// breaks at line boundaries when possible, then at whitespace, and only hard
// splits at a rune boundary when an unbroken run exceeds the budget. No chunk
// ever splits a character; the concatenation of chunks is exactly text.
func chunkMessage(text string, maxUnits int) []string {
	if text == "" {
		return nil
	}
	if utf16Len(escMD(text)) <= maxUnits {
		return []string{text}
	}
	var chunks []string
	cur := ""
	curUnits := 0
	flush := func() {
		if cur != "" {
			chunks = append(chunks, cur)
			cur = ""
			curUnits = 0
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		le := utf16Len(escMD(line))
		if cur == "" && le <= maxUnits {
			cur, curUnits = line, le
			continue
		}
		if cur != "" && curUnits+le <= maxUnits {
			cur += line
			curUnits += le
			continue
		}
		flush()
		if le <= maxUnits {
			cur, curUnits = line, le
			continue
		}
		// A single line exceeds the budget on its own.
		for _, piece := range splitLongLine(line, maxUnits) {
			chunks = append(chunks, piece)
		}
	}
	flush()
	return chunks
}

// splitLongLine splits one over-long line into pieces each fitting maxUnits
// (escaped). It prefers cutting at whitespace (when at least half the fitted
// run is kept) and falls back to hard rune-boundary splits.
func splitLongLine(line string, maxUnits int) []string {
	var pieces []string
	r := []rune(line)
	for len(r) > 0 {
		if utf16Len(escMD(string(r))) <= maxUnits {
			pieces = append(pieces, string(r))
			break
		}
		acc, fit, lastSpace := 0, 0, 0
		for i, ch := range r {
			w := utf16Len(escMD(string(ch)))
			if acc+w > maxUnits {
				fit = i
				break
			}
			acc += w
			fit = i + 1
			if ch == ' ' || ch == '\t' {
				lastSpace = i + 1
			}
		}
		if fit == 0 {
			fit = 1 // always make progress
		}
		if lastSpace > 0 && lastSpace >= fit/2 {
			fit = lastSpace
		}
		pieces = append(pieces, string(r[:fit]))
		r = r[fit:]
	}
	return pieces
}
