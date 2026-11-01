package messaging

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// TelegramMaxMessageChars is Telegram's per-message text limit (UTF-16 code
// units after entity parsing). The renderer measures the HTML source, which
// is always at least as long, so chunks are safely under the limit.
const TelegramMaxMessageChars = 4096

var (
	reInlineCode  = regexp.MustCompile("`([^`\n]+)`")
	reImage       = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	reMdLink      = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	reSlackLink   = regexp.MustCompile(`<((?:https?|mailto):[^|>\s]+)\|([^>]+)>`)
	reAngleURL    = regexp.MustCompile(`<((?:https?|mailto):[^>\s]+)>`)
	rePlaceholder = regexp.MustCompile("\x00(\\d+)\x00")
	// Formatting runs on HTML-escaped text: "<" only appears in tags we
	// inserted, so bodies exclude it to keep tags balanced.
	reStrike     = regexp.MustCompile(`~~([^~<\n]+?)~~`)
	reBoldStar   = regexp.MustCompile(`\*\*([^<\n]+?)\*\*`)
	reBoldUnder  = regexp.MustCompile(`(^|[^\w])__([^_<\n]+?)__($|[^\w])`)
	reItalicStar = regexp.MustCompile(`(^|[^\w*])\*([^\s*<](?:[^*<\n]*[^\s*<])?)\*($|[^\w*])`)
	reItalicUnd  = regexp.MustCompile(`(^|[^\w_])_([^\s_<](?:[^_<\n]*[^\s_<])?)_($|[^\w_])`)
	reHTMLTag    = regexp.MustCompile(`<[^>]+>`)
	reHTMLAnchor = regexp.MustCompile(`<a href="([^"]*)">(.*?)</a>`)
)

// telegramEscape escapes the three characters Telegram's HTML parse mode
// requires (&, <, >). Quotes only matter inside attributes (see escapeAttr).
func telegramEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

func telegramEscapeAttr(s string) string {
	return strings.ReplaceAll(telegramEscape(s), `"`, "&quot;")
}

func telegramEscapedRuneCost(r rune) int {
	switch r {
	case '&':
		return 5
	case '<', '>':
		return 4
	}
	return utf16RuneCost(r)
}

// telegramInline converts one line of Markdown inline syntax to Telegram HTML.
// The result is always tag-balanced.
func telegramInline(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	var held []string
	hold := func(v string) string {
		held = append(held, v)
		return "\x00" + strconv.Itoa(len(held)-1) + "\x00"
	}
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		return hold("<code>" + telegramEscape(reInlineCode.FindStringSubmatch(m)[1]) + "</code>")
	})
	link := func(text, url string) string {
		return hold(`<a href="` + telegramEscapeAttr(url) + `">` + telegramFormat(telegramEscape(text)) + "</a>")
	}
	s = reImage.ReplaceAllStringFunc(s, func(m string) string {
		p := reImage.FindStringSubmatch(m)
		if p[1] == "" {
			return link(p[2], p[2])
		}
		return link(p[1], p[2])
	})
	s = reMdLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reMdLink.FindStringSubmatch(m)
		return link(p[1], p[2])
	})
	s = reSlackLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reSlackLink.FindStringSubmatch(m)
		return link(p[2], p[1])
	})
	s = reAngleURL.ReplaceAllString(s, "$1")

	s = telegramFormat(telegramEscape(s))

	return rePlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		i, err := strconv.Atoi(rePlaceholder.FindStringSubmatch(m)[1])
		if err != nil || i >= len(held) {
			return ""
		}
		return held[i]
	})
}

// telegramFormat applies emphasis to already-escaped text.
func telegramFormat(s string) string {
	s = reStrike.ReplaceAllString(s, "<s>$1</s>")
	s = reBoldStar.ReplaceAllString(s, "<b>$1</b>")
	for i := 0; i < 2; i++ { // second pass catches matches whose boundary char was consumed
		s = reBoldUnder.ReplaceAllString(s, "$1<b>$2</b>$3")
		s = reItalicStar.ReplaceAllString(s, "$1<i>$2</i>$3")
		s = reItalicUnd.ReplaceAllString(s, "$1<i>$2</i>$3")
	}
	return s
}

// telegramPlainUnits renders raw text as escaped plain text pieces that each
// fit within limit (used when a single formatted line is too long).
func telegramPlainUnits(raw string, limit int, sep string) []msgUnit {
	var out []msgUnit
	for i, piece := range hardSplit(raw, limit, telegramEscapedRuneCost) {
		u := msgUnit{text: telegramEscape(piece), sep: "\n"}
		if i == 0 {
			u.sep = sep
		}
		out = append(out, u)
	}
	return out
}

// telegramLineUnit renders one inline line, falling back to plain text pieces
// when the formatted HTML would exceed limit.
func telegramLineUnit(prefix, raw string, limit int, sep string) []msgUnit {
	htmlLine := prefix + telegramInline(raw)
	if utf16Len(htmlLine) <= limit {
		return []msgUnit{{text: htmlLine, sep: sep}}
	}
	return telegramPlainUnits(html.UnescapeString(reHTMLTag.ReplaceAllString(prefix, ""))+raw, limit, sep)
}

// telegramBlockUnits renders one block into units of at most limit.
func telegramBlockUnits(b mdBlock, limit int, sep string) []msgUnit {
	switch b.kind {
	case mdHeading:
		text := strings.ReplaceAll(strings.ReplaceAll(b.lines[0], "**", ""), "__", "")
		return telegramLineUnit("", "**"+strings.TrimSpace(text)+"**", limit, sep)
	case mdRule:
		return []msgUnit{{text: "———", sep: sep}}
	case mdCode:
		open, closeTag := "<pre>", "</pre>"
		if b.lang != "" {
			open = `<pre><code class="language-` + telegramEscapeAttr(b.lang) + `">`
			closeTag = "</code></pre>"
		}
		budget := limit - utf16Len(open) - utf16Len(closeTag)
		var out []msgUnit
		for i, piece := range packLines(b.lines, budget, telegramEscapedRuneCost) {
			u := msgUnit{text: open + telegramEscape(piece) + closeTag, sep: "\n"}
			if i == 0 {
				u.sep = sep
			}
			out = append(out, u)
		}
		if len(out) == 0 {
			out = append(out, msgUnit{text: open + closeTag, sep: sep})
		}
		return out
	case mdQuote:
		const open, closeTag = "<blockquote>", "</blockquote>"
		budget := limit - len(open) - len(closeTag)
		var out []msgUnit
		var cur []string
		curLen := 0
		flush := func() {
			if len(cur) > 0 {
				s := "\n"
				if len(out) == 0 {
					s = sep
				}
				out = append(out, msgUnit{text: open + strings.Join(cur, "\n") + closeTag, sep: s})
				cur, curLen = nil, 0
			}
		}
		for _, l := range b.lines {
			h := telegramInline(trimHardBreak(l))
			hl := utf16Len(h)
			if hl > budget {
				flush()
				s := "\n"
				if len(out) == 0 {
					s = sep
				}
				out = append(out, telegramPlainUnits(l, limit, s)...)
				continue
			}
			add := hl
			if len(cur) > 0 {
				add++
			}
			if curLen+add > budget {
				flush()
				add = hl
			}
			cur = append(cur, h)
			curLen += add
		}
		flush()
		return out
	case mdList:
		var out []msgUnit
		for _, l := range b.lines {
			it := parseListLine(l)
			indent := strings.Repeat("    ", it.level)
			prefix := indent + "• "
			switch {
			case it.cont:
				prefix = indent + "  "
			case it.ordered:
				prefix = indent + it.marker + " "
			}
			s := "\n"
			if len(out) == 0 {
				s = sep
			}
			out = append(out, telegramLineUnit(telegramEscape(prefix), trimHardBreak(it.text), limit, s)...)
		}
		return out
	case mdTable:
		header, rows := tableRows(b.lines)
		var out []msgUnit
		for _, row := range rows {
			var parts []string
			for j, cell := range row {
				if cell == "" {
					continue
				}
				if j < len(header) && header[j] != "" {
					parts = append(parts, fmt.Sprintf("**%s:** %s", header[j], cell))
				} else {
					parts = append(parts, cell)
				}
			}
			if len(parts) == 0 {
				continue
			}
			s := "\n"
			if len(out) == 0 {
				s = sep
			}
			out = append(out, telegramLineUnit("• ", strings.Join(parts, " | "), limit, s)...)
		}
		return out
	default: // paragraph
		var out []msgUnit
		for _, l := range b.lines {
			s := "\n"
			if len(out) == 0 {
				s = sep
			}
			out = append(out, telegramLineUnit("", trimHardBreak(strings.TrimLeft(l, " \t")), limit, s)...)
		}
		return out
	}
}

// RenderTelegram converts provider-neutral Markdown into Telegram HTML
// (parse_mode=HTML) chunks of at most limit characters. Long messages are
// split between blocks or lines, never inside a tag or entity; an oversized
// code block is split into several <pre> blocks.
func RenderTelegram(markdown string, limit int) []string {
	if limit <= 0 {
		limit = TelegramMaxMessageChars
	}
	markdown = replaceEmojiShortcodes(markdown)
	var units []msgUnit
	for _, b := range parseMarkdownBlocks(markdown) {
		units = append(units, telegramBlockUnits(b, limit, "\n\n")...)
	}
	return packUnits(units, limit)
}

// telegramHTMLToPlain strips the HTML produced by RenderTelegram so a chunk
// Telegram refused to parse can be re-sent as plain text. Links keep their
// URL as "text (url)".
func telegramHTMLToPlain(s string) string {
	s = reHTMLAnchor.ReplaceAllStringFunc(s, func(m string) string {
		p := reHTMLAnchor.FindStringSubmatch(m)
		text := reHTMLTag.ReplaceAllString(p[2], "")
		url := p[1]
		if html.UnescapeString(text) == html.UnescapeString(url) {
			return text
		}
		return text + " (" + url + ")"
	})
	return html.UnescapeString(reHTMLTag.ReplaceAllString(s, ""))
}
