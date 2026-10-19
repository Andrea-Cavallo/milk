package local

import (
	"html"
	"regexp"
	"strings"
)

// htmlmd.go — best-effort HTML → Markdown conversion for the web_fetch tool
// (toolset-review G5). Deliberately small: no external dependencies, no
// general-purpose HTML parser — regex surgery good enough to turn a docs page
// into readable text a model can search. html_request/web_fetch with
// format "html"/"raw" keeps the original bytes when fidelity matters.

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style|noscript|svg|template)\b[^>]*>.*?</(?:script|style|noscript|svg|template)>`)
	reComments    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHeading     = regexp.MustCompile(`(?is)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	reAnchor      = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reImage       = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	reImgAttr     = regexp.MustCompile(`(?is)(src|alt)=["']([^"']*)["']`)
	reListItem    = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	rePre         = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	reInlineCode  = regexp.MustCompile(`(?is)<code[^>]*>(.*?)</code>`)
	reBreak       = regexp.MustCompile(`(?is)<br\s*/?>`)
	reParagraph   = regexp.MustCompile(`(?is)</?(p|div|section|article|header|footer|main|aside|nav|tr|table|ul|ol|dl|dd|dt|blockquote|figure|figcaption)\b[^>]*>`)
	reAnyTag      = regexp.MustCompile(`(?s)<[^>]+>`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

// htmlToMarkdown converts an HTML document to rough Markdown: headings,
// links, images, list items, code blocks and paragraphs; everything else is
// stripped to text.
func htmlToMarkdown(src string) string {
	s := reComments.ReplaceAllString(src, "")
	s = reScriptStyle.ReplaceAllString(s, "")

	// Pre blocks first so inline rules don't touch their content.
	s = rePre.ReplaceAllStringFunc(s, func(m string) string {
		body := reAnyTag.ReplaceAllString(submatchBody(rePre, m), "")
		body = strings.TrimSpace(html.UnescapeString(body))
		return "\n```\n" + body + "\n```\n"
	})

	s = reHeading.ReplaceAllStringFunc(s, func(m string) string {
		level := submatchN(reHeading, m, 1)
		text := strings.TrimSpace(stripTags(html.UnescapeString(submatchN(reHeading, m, 2))))
		return "\n" + strings.Repeat("#", clampInt(len(level), 1, 6)) + " " + text + "\n"
	})

	s = reAnchor.ReplaceAllStringFunc(s, func(m string) string {
		href := submatchN(reAnchor, m, 1)
		text := strings.TrimSpace(stripTags(html.UnescapeString(submatchN(reAnchor, m, 2))))
		if text == "" || href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") {
			return text
		}
		return "[" + text + "](" + href + ")"
	})

	s = reImage.ReplaceAllStringFunc(s, func(m string) string {
		attrs := reImgAttr.FindAllStringSubmatch(m, -1)
		var src, alt string
		for _, a := range attrs {
			switch strings.ToLower(a[1]) {
			case "src":
				src = a[2]
			case "alt":
				alt = a[2]
			}
		}
		if src == "" {
			return ""
		}
		return "![" + alt + "](" + src + ")"
	})

	s = reInlineCode.ReplaceAllString(s, "`$1`")
	s = reListItem.ReplaceAllStringFunc(s, func(m string) string {
		text := strings.TrimSpace(stripTags(html.UnescapeString(submatchBody(reListItem, m))))
		return "\n- " + text
	})
	s = reBreak.ReplaceAllString(s, "\n")
	s = reParagraph.ReplaceAllString(s, "\n")

	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	// Normalize whitespace: drop space-only lines, cap blank runs at one.
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == "" {
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				continue
			}
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.TrimSpace(l))
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	return reBlankLines.ReplaceAllString(out, "\n\n")
}

// stripTags removes every HTML tag from s.
func stripTags(s string) string { return reAnyTag.ReplaceAllString(s, "") }

// submatchBody returns capture group 1 of re's first match within m (m is the
// full match the regex produced, so re-matching it is exact).
func submatchBody(re *regexp.Regexp, m string) string {
	if sm := re.FindStringSubmatch(m); len(sm) > 1 {
		return sm[1]
	}
	return ""
}

// submatchN returns capture group n of re's first match within m.
func submatchN(re *regexp.Regexp, m string, n int) string {
	if sm := re.FindStringSubmatch(m); len(sm) > n {
		return sm[n]
	}
	return ""
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
