package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Switchable TUI themes. "off" (the default) is the original look, byte for
// byte; "supermilk" is the opt-in skin (/theme supermilk or config "theme"):
// milk-white ground, soft ink text, muted olive accent. A skin restyles only
// the chrome (header, status bar, turn labels, panel tabs, scrollbars),
// paints the whole screen and, on exit, hands the terminal's own colours
// back.

type themeColor struct{ dark, light string }

// fixed makes a token that reads the same on any terminal background.
func fixed(hex string) themeColor { return themeColor{hex, hex} }

// adaptive is the lipgloss colour for style-based rendering.
func (c themeColor) adaptive() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: c.light, Dark: c.dark}
}

func (c themeColor) pick() string {
	if lipgloss.HasDarkBackground() {
		return c.dark
	}
	return c.light
}

func hexRGB(h string) (r, g, b int) {
	v, _ := strconv.ParseUint(h[1:], 16, 32)
	return int(v >> 16), int(v >> 8 & 0xff), int(v & 0xff)
}

// sgrBg / sgrFg are raw truecolor escapes, "" when not a TTY.
func (c themeColor) sgrBg() string {
	if !isTTY {
		return ""
	}
	r, g, b := hexRGB(c.pick())
	return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
}

func (c themeColor) sgrFg() string {
	if !isTTY {
		return ""
	}
	r, g, b := hexRGB(c.pick())
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

type themeDef struct {
	name                                                                       string
	modern                                                                     bool
	base, raised, toast, hairline, thumb, text, muted, accent, accentLo, think themeColor
	// terminal colours handed to the emulator while the theme is active
	termBg, termFg, termCursor string
	termAnsi                   [16]string // "" = leave the terminal's own palette
}

var themeOff = &themeDef{name: "off"}

// supermilk: a milk-white ground with warm ink text, hairline separators and
// a single muted olive accent; the ANSI ramp is pulled into the same family
// so coloured text (code spans, links) stays readable on the light ground.
var themeSuperMilk = &themeDef{
	name: "supermilk", modern: true,
	base: fixed("#f8f5ee"), raised: fixed("#efebe0"), toast: fixed("#e8e3d5"),
	hairline: fixed("#dcd7c9"), thumb: fixed("#c2bfb4"),
	text: fixed("#3f2a26"), muted: fixed("#7d7b74"), think: fixed("#5d6f4a"),
	accent: fixed("#6b6140"), accentLo: fixed("#c9c5b8"),
	termBg: "#f8f5ee", termFg: "#3f2a26", termCursor: "#3f2a26",
	termAnsi: [16]string{
		"#f8f5ee", "#8a4b3c", "#5f6b3a", "#7a6a2e", "#4a6280", "#7a5a78", "#4a6f6a", "#3f2a26",
		"#7d7b74", "#a85f4d", "#7a8a4e", "#9a8640", "#6482a8", "#9a7498", "#6a948c", "#2b1a17",
	},
}

var themes = map[string]*themeDef{"off": themeOff, "supermilk": themeSuperMilk}

// th is the active theme; tk* mirror its tokens for call sites.
var (
	th                                                                        = themeOff
	tkBase, tkRaised, tkToast, tkHairline, tkThumb, tkText, tkMuted, tkAccent themeColor
	tkAccentLo, tkThink                                                       themeColor
)

// setTheme activates a theme by name and rebuilds every style derived from
// it. Unknown names are rejected.
func setTheme(name string) bool {
	t, ok := themes[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return false
	}
	th = t
	tkBase, tkRaised, tkToast, tkHairline, tkThumb = t.base, t.raised, t.toast, t.hairline, t.thumb
	tkText, tkMuted, tkAccent, tkAccentLo, tkThink = t.text, t.muted, t.accent, t.accentLo, t.think
	applyThinkSurface()
	pulseColors = buildPulse()
	rebuildStyles()
	return true
}

// rebuildStyles reassigns the lipgloss styles owned by the chrome. For "off"
// these are the original definitions.
func rebuildStyles() {
	if !th.modern {
		styleHeaderBar = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#1E2A4A", Dark: "#0E0E1A"}).
			Foreground(lipgloss.AdaptiveColor{Light: "#D8E4F8", Dark: "#AABBCC"}).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#4466AA", Dark: "#334466"})
		styleStatusBar = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#E5E5E5", Dark: "#2B2B2B"})
		styleBorder = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#AAA", Dark: "#555"})
		stylePanelTitle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#555", Dark: "#888"}).
			Bold(true)
		stylePanelSection = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#777", Dark: "#666"})
		return
	}
	styleHeaderBar = lipgloss.NewStyle().
		Background(tkBase.adaptive()).
		Foreground(tkMuted.adaptive()).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(tkHairline.adaptive())
	styleStatusBar = lipgloss.NewStyle().
		Background(tkRaised.adaptive()).
		Foreground(tkMuted.adaptive())
	styleBorder = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(tkHairline.adaptive())
	stylePanelTitle = lipgloss.NewStyle().Foreground(tkAccent.adaptive()).Bold(true)
	stylePanelSection = lipgloss.NewStyle().Foreground(tkMuted.adaptive()).Bold(true)
}

// originalPulse is the yellow breathing ramp of the original look.
var originalPulse = [16]string{
	"\033[38;2;60;50;0m", "\033[38;2;67;58;1m", "\033[38;2;81;73;4m", "\033[38;2;110;101;9m",
	"\033[38;2;157;135;15m", "\033[38;2;204;170;20m", "\033[38;2;232;197;25m", "\033[38;2;251;215;28m",
	"\033[38;2;255;220;30m", "\033[38;2;251;215;28m", "\033[38;2;232;197;25m", "\033[38;2;204;170;20m",
	"\033[38;2;157;135;15m", "\033[38;2;110;101;9m", "\033[38;2;81;73;4m", "\033[38;2;67;58;1m",
}

// buildPulse is the 16-step breathing gradient keyed to the spinner frame:
// the original yellow ramp, or accent-floor → accent → floor for a theme.
func buildPulse() [16]string {
	if !th.modern {
		return originalPulse
	}
	var out [16]string
	r0, g0, b0 := hexRGB(tkAccentLo.dark)
	r1, g1, b1 := hexRGB(tkAccent.dark)
	for i := range out {
		t := (1 - math.Cos(2*math.Pi*float64(i)/16)) / 2
		mix := func(a, b int) int { return a + int(math.Round(t*float64(b-a))) }
		out[i] = fmt.Sprintf("\033[38;2;%d;%d;%dm", mix(r0, r1), mix(g0, g1), mix(b0, b1))
	}
	return out
}

// accentStyle is the "gold" emphasis: original gold, or the theme accent.
func accentStyle() string {
	if !th.modern {
		return "\033[1;38;2;255;208;60m"
	}
	return ansiBold + tkAccent.sgrFg()
}

// wordmark is the startup-banner "milk".
func wordmark() string {
	if !th.modern {
		return "\033[1;38;2;255;208;96mmilk\033[0m"
	}
	return boldGold("milk")
}

func panelAltBackgroundCode() string {
	if !isTTY {
		return ""
	}
	if th.modern {
		return tkRaised.sgrBg()
	}
	if lipgloss.HasDarkBackground() {
		return "\033[48;2;24;24;28m"
	}
	return "\033[48;2;236;236;239m"
}

func toastBackgroundCode() string {
	if !isTTY {
		return ""
	}
	if th.modern {
		return tkToast.sgrBg()
	}
	if lipgloss.HasDarkBackground() {
		return "\033[48;2;26;38;58m" // #1A263A
	}
	return "\033[48;2;217;228;245m" // #D9E4F5
}

// Scrollbars: original dim glyphs, or a hairline track with a slim thumb.
func scrollTrackGlyph() string {
	if !th.modern {
		return dim("│")
	}
	return colorize("│", tkHairline.sgrFg())
}

func scrollThumbGlyph() string {
	if !th.modern {
		return dim("▌")
	}
	return colorize("┃", tkThumb.sgrFg())
}

// agentTag is the label before an agent's reply.
func agentTag(name, role string) string {
	if !th.modern {
		if role == "escalation" {
			return bold(blue(name+":")) + " "
		}
		return bold(green(name+":")) + " "
	}
	return "\n" + colorize(strings.ToUpper(name), ansiBold+tkAccent.sgrFg()) + dim(" · "+strings.ToUpper(role)) + softRule() + "\n"
}

// userEcho is the transcript echo of a submitted line.
func userEcho(label, input string) string {
	if !th.modern {
		return label + input + "\n"
	}
	return "\n" + colorize("YOU", ansiBold+tkAccent.sgrFg()) + softRule() + "\n" + input + "\n"
}

// osc writes one terminal control string.
func osc(format string, a ...any) {
	fmt.Fprintf(os.Stdout, "\x1b]"+format+"\x1b\\", a...)
}

// applyThemeTerminal pushes the theme's colours into the terminal emulator:
// default background/foreground/cursor and the 16-colour ANSI palette, so
// everything milk does not paint itself (edge columns, scrollbar gutter,
// ANSI-coloured text) matches. Returns the restore func. A no-op for "off".
func applyThemeTerminal() func() {
	if !isTTY || !th.modern {
		return func() {}
	}
	osc("11;%s", th.termBg)
	osc("10;%s", th.termFg)
	osc("12;%s", th.termCursor)
	for i, c := range th.termAnsi {
		if c != "" {
			osc("4;%d;%s", i, c)
		}
	}
	return restoreTerminalColors
}

// restoreTerminalColors hands the terminal's own palette back.
func restoreTerminalColors() {
	if !isTTY {
		return
	}
	osc("104")
	osc("110")
	osc("111")
	osc("112")
}

// dimCode is the "recede" style: faint, or the theme's muted ink — SGR faint
// washes out to near-invisible on a light ground.
func dimCode() string {
	if !th.modern {
		return ansiDim
	}
	return tkMuted.sgrFg()
}

// softRule is a short hairline that trails a turn label.
func softRule() string { return " " + colorize(strings.Repeat("─", 18), tkHairline.sgrFg()) }

const originalThinkBgParams = "48;5;235"

// applyThinkSurface points the thinking-surface background at the theme: the
// original dark grey, or the raised surface of a skin (readable on light).
func applyThinkSurface() {
	thinkBgParams = originalThinkBgParams
	if th.modern {
		r, g, b := hexRGB(tkRaised.dark)
		thinkBgParams = fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
	}
	ansiThinkBg = "\033[" + thinkBgParams + "m"
}

// compactBlankLines drops empty lines so reasoning reads as one tight run.
func compactBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for i, l := range lines {
		if strings.TrimSpace(l) == "" && i != len(lines)-1 {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
