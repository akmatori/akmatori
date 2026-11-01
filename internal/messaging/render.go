package messaging

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Outbound content is provider-neutral GitHub-flavored Markdown. Each
// provider renders it for its platform and enforces its own size limit at
// send time. This file holds the shared pieces: a small line-based Markdown
// block parser, the chunk packer that splits long messages on block / line
// boundaries, and helpers both the Telegram and Discord renderers use.

type mdBlockKind int

const (
	mdParagraph mdBlockKind = iota
	mdHeading
	mdCode
	mdQuote
	mdList
	mdTable
	mdRule
)

// mdBlock is one Markdown block. lines holds the source lines (for mdCode:
// the code lines without the fences).
type mdBlock struct {
	kind  mdBlockKind
	lines []string
	lang  string // mdCode: info string
	level int    // mdHeading: 1-6
}

var (
	reFence     = regexp.MustCompile("^\\s*(```+|~~~+)\\s*([\\w+#.-]*)")
	reHeading   = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	reQuote     = regexp.MustCompile(`^\s{0,3}>\s?(.*)$`)
	reListItem  = regexp.MustCompile(`^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$`)
	reTableRow  = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	reTableSep  = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	reRule      = regexp.MustCompile(`^\s{0,3}([-*_])(\s*([-*_])){2,}\s*$`)
	reShortcode = regexp.MustCompile(`:([a-z0-9_+-]+):`)
)

// parseMarkdownBlocks splits text into blocks. It is deliberately forgiving:
// anything it does not recognise is a paragraph, an unclosed code fence runs
// to the end of the text.
func parseMarkdownBlocks(text string) []mdBlock {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var blocks []mdBlock
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			i++
		case reFence.MatchString(line):
			m := reFence.FindStringSubmatch(line)
			fence := m[1]
			b := mdBlock{kind: mdCode, lang: m[2]}
			i++
			for i < len(lines) {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence[:3]) && strings.Trim(strings.TrimSpace(lines[i]), fence[:1]) == "" {
					i++
					break
				}
				b.lines = append(b.lines, lines[i])
				i++
			}
			blocks = append(blocks, b)
		case reHeading.MatchString(line):
			m := reHeading.FindStringSubmatch(line)
			blocks = append(blocks, mdBlock{kind: mdHeading, level: len(m[1]), lines: []string{m[2]}})
			i++
		case reRule.MatchString(line):
			blocks = append(blocks, mdBlock{kind: mdRule})
			i++
		case reQuote.MatchString(line):
			b := mdBlock{kind: mdQuote}
			for i < len(lines) && reQuote.MatchString(lines[i]) {
				b.lines = append(b.lines, reQuote.FindStringSubmatch(lines[i])[1])
				i++
			}
			blocks = append(blocks, b)
		case reListItem.MatchString(line):
			b := mdBlock{kind: mdList}
			for i < len(lines) {
				l := lines[i]
				if strings.TrimSpace(l) == "" || reFence.MatchString(l) || reHeading.MatchString(l) {
					break
				}
				// Lines that are neither list items nor indented
				// continuations end the list.
				if !reListItem.MatchString(l) && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
					break
				}
				b.lines = append(b.lines, l)
				i++
			}
			blocks = append(blocks, b)
		case reTableRow.MatchString(line):
			b := mdBlock{kind: mdTable}
			for i < len(lines) && reTableRow.MatchString(lines[i]) {
				b.lines = append(b.lines, lines[i])
				i++
			}
			blocks = append(blocks, b)
		default:
			b := mdBlock{kind: mdParagraph}
			for i < len(lines) {
				l := lines[i]
				if strings.TrimSpace(l) == "" || reFence.MatchString(l) || reHeading.MatchString(l) ||
					reQuote.MatchString(l) || reRule.MatchString(l) || reTableRow.MatchString(l) ||
					(reListItem.MatchString(l) && len(b.lines) > 0) {
					break
				}
				b.lines = append(b.lines, l)
				i++
			}
			blocks = append(blocks, b)
		}
	}
	return blocks
}

// listItem is one parsed list line.
type listItem struct {
	level   int    // nesting depth (0 = top)
	ordered bool   // "1." style marker
	marker  string // original marker, e.g. "-" or "3."
	text    string // item text (or continuation text when cont is true)
	cont    bool   // continuation line, not a new item
}

func parseListLine(line string) listItem {
	expanded := strings.ReplaceAll(line, "\t", "    ")
	if m := reListItem.FindStringSubmatch(expanded); m != nil {
		marker := m[2]
		return listItem{
			level:   len(m[1]) / 2,
			ordered: marker[0] >= '0' && marker[0] <= '9',
			marker:  marker,
			text:    m[3],
		}
	}
	indent := len(expanded) - len(strings.TrimLeft(expanded, " "))
	return listItem{level: indent / 2, text: strings.TrimSpace(expanded), cont: true}
}

// tableRows parses table lines into header + data rows, skipping the
// separator row.
func tableRows(lines []string) (header []string, rows [][]string) {
	for i, l := range lines {
		if i > 0 && reTableSep.MatchString(l) {
			continue
		}
		s := strings.TrimSpace(l)
		s = strings.TrimPrefix(s, "|")
		s = strings.TrimSuffix(s, "|")
		cells := strings.Split(s, "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		if header == nil {
			header = cells
			continue
		}
		rows = append(rows, cells)
	}
	return header, rows
}

// slackEmojiShortcodes maps the Slack shortcodes Akmatori itself emits in
// banners and notes to Unicode, so Telegram / Discord do not show ":gear:".
var slackEmojiShortcodes = map[string]string{
	"red_circle":                "🔴",
	"large_orange_circle":       "🟠",
	"large_yellow_circle":       "🟡",
	"large_blue_circle":         "🔵",
	"white_circle":              "⚪",
	"label":                     "🏷️",
	"computer":                  "💻",
	"gear":                      "⚙️",
	"warning":                   "⚠️",
	"memo":                      "📝",
	"book":                      "📖",
	"twisted_rightwards_arrows": "🔀",
	"rotating_light":            "🚨",
	"white_check_mark":          "✅",
	"x":                         "❌",
	"hourglass_flowing_sand":    "⏳",
	"mag":                       "🔍",
	"bulb":                      "💡",
}

// replaceEmojiShortcodes swaps known Slack emoji shortcodes for Unicode.
// Unknown shortcodes (and things like "10:30:00") are left alone.
func replaceEmojiShortcodes(s string) string {
	if !strings.Contains(s, ":") {
		return s
	}
	return reShortcode.ReplaceAllStringFunc(s, func(m string) string {
		if u, ok := slackEmojiShortcodes[m[1:len(m)-1]]; ok {
			return u
		}
		return m
	})
}

// utf16Len counts UTF-16 code units, the unit Telegram uses for its message
// limit. It is a safe upper bound for Discord's character count too.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

func utf16RuneCost(r rune) int { return utf16.RuneLen(r) }

// msgUnit is an indivisible piece of rendered output (balanced markup) plus
// the separator that joins it to the previous unit.
type msgUnit struct {
	text string
	sep  string
}

// packUnits joins units into chunks of at most limit (by utf16Len). Every unit
// must already fit within limit; splits only ever happen between units.
func packUnits(units []msgUnit, limit int) []string {
	var chunks []string
	var cur strings.Builder
	curLen := 0
	for _, u := range units {
		if u.text == "" {
			continue
		}
		ul := utf16Len(u.text)
		if curLen == 0 {
			cur.WriteString(u.text)
			curLen = ul
			continue
		}
		sl := utf16Len(u.sep)
		if curLen+sl+ul <= limit {
			cur.WriteString(u.sep)
			cur.WriteString(u.text)
			curLen += sl + ul
			continue
		}
		chunks = append(chunks, cur.String())
		cur.Reset()
		cur.WriteString(u.text)
		curLen = ul
	}
	if curLen > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

// hardSplit cuts raw into pieces whose summed per-rune cost stays within
// limit. It prefers to cut after whitespace and never splits a rune. cost
// must be additive per rune (e.g. the length of the HTML-escaped rune).
func hardSplit(raw string, limit int, cost func(rune) int) []string {
	if limit <= 0 {
		return nil
	}
	runes := []rune(raw)
	var out []string
	start, used, lastSpace := 0, 0, -1
	for i := 0; i < len(runes); i++ {
		c := cost(runes[i])
		if used+c > limit && i > start {
			cut := i
			if lastSpace > start {
				cut = lastSpace + 1
			}
			out = append(out, string(runes[start:cut]))
			start, used, lastSpace = cut, 0, -1
			for j := start; j < i; j++ {
				used += cost(runes[j])
				if unicode.IsSpace(runes[j]) {
					lastSpace = j
				}
			}
		}
		used += c
		if unicode.IsSpace(runes[i]) {
			lastSpace = i
		}
	}
	if start < len(runes) {
		out = append(out, string(runes[start:]))
	}
	return out
}

// packLines groups lines (joined with "\n") into pieces of at most limit
// cost. A single line over the limit is hard-split.
func packLines(lines []string, limit int, cost func(rune) int) []string {
	strCost := func(s string) int {
		n := 0
		for _, r := range s {
			n += cost(r)
		}
		return n
	}
	var out []string
	var cur []string
	curCost := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur, curCost = nil, 0
		}
	}
	for _, l := range lines {
		lc := strCost(l)
		if lc > limit {
			flush()
			out = append(out, hardSplit(l, limit, cost)...)
			continue
		}
		add := lc
		if len(cur) > 0 {
			add++ // newline
		}
		if curCost+add > limit {
			flush()
			add = lc
		}
		cur = append(cur, l)
		curCost += add
	}
	flush()
	return out
}

// trimHardBreak drops Markdown hard-break trailing spaces / backslash.
func trimHardBreak(line string) string {
	line = strings.TrimRight(line, " \t")
	if strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\") {
		line = strings.TrimSuffix(line, "\\")
	}
	return line
}
