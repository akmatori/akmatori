package messaging

import (
	"fmt"
	"strings"
)

// DiscordMaxMessageChars is Discord's per-message content limit.
const DiscordMaxMessageChars = 2000

// discordLineUnits returns one line as a unit, hard-split when it alone is
// over limit.
func discordLineUnits(line string, limit int, sep string) []msgUnit {
	var out []msgUnit
	for i, piece := range hardSplit(line, limit, utf16RuneCost) {
		u := msgUnit{text: piece, sep: "\n"}
		if i == 0 {
			u.sep = sep
		}
		out = append(out, u)
	}
	return out
}

func discordBlockUnits(b mdBlock, limit int, sep string) []msgUnit {
	nextSep := func(out []msgUnit) string {
		if len(out) == 0 {
			return sep
		}
		return "\n"
	}
	switch b.kind {
	case mdHeading:
		text := strings.TrimSpace(b.lines[0])
		line := strings.Repeat("#", b.level) + " " + text
		if b.level > 3 { // Discord renders only #, ## and ###
			line = "**" + strings.ReplaceAll(text, "**", "") + "**"
		}
		return discordLineUnits(line, limit, sep)
	case mdRule:
		return []msgUnit{{text: "———", sep: sep}}
	case mdCode:
		open, closeFence := "```"+b.lang+"\n", "\n```"
		budget := limit - utf16Len(open) - utf16Len(closeFence)
		var out []msgUnit
		for _, piece := range packLines(b.lines, budget, utf16RuneCost) {
			out = append(out, msgUnit{text: open + piece + closeFence, sep: nextSep(out)})
		}
		if len(out) == 0 {
			out = append(out, msgUnit{text: open + closeFence, sep: sep})
		}
		return out
	case mdQuote:
		var out []msgUnit
		for _, l := range b.lines {
			out = append(out, discordLineUnits("> "+trimHardBreak(l), limit, nextSep(out))...)
		}
		return out
	case mdTable: // Discord has no tables
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
			if len(parts) > 0 {
				out = append(out, discordLineUnits("• "+strings.Join(parts, " | "), limit, nextSep(out))...)
			}
		}
		return out
	default: // paragraph, list: Discord speaks Markdown already
		var out []msgUnit
		for _, l := range b.lines {
			l = reSlackLink.ReplaceAllString(trimHardBreak(l), "[$2]($1)")
			out = append(out, discordLineUnits(l, limit, nextSep(out))...)
		}
		return out
	}
}

// RenderDiscord adapts provider-neutral Markdown for Discord (headings beyond
// level 3 and tables are rewritten, emoji shortcodes become Unicode) and
// splits it into messages of at most limit characters. Splits happen between
// blocks or lines; a code block that is too long is closed and reopened in
// the next message.
func RenderDiscord(markdown string, limit int) []string {
	if limit <= 0 {
		limit = DiscordMaxMessageChars
	}
	markdown = replaceEmojiShortcodes(markdown)
	var units []msgUnit
	for _, b := range parseMarkdownBlocks(markdown) {
		units = append(units, discordBlockUnits(b, limit, "\n\n")...)
	}
	return packUnits(units, limit)
}
