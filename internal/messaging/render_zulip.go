package messaging

import (
	"regexp"
	"strings"
)

// ZulipMaxMessageChars is Zulip's default message length limit (the server's
// max_message_length, reported by POST /register).
const ZulipMaxMessageChars = 10000

// ZulipMaxTopicChars is Zulip's default topic length limit (max_topic_length).
const ZulipMaxTopicChars = 60

// reZulipMention matches a Zulip mention ("@**name**", "@**all**"). Agent
// text must not notify people: renderZulipText turns it into a silent mention
// ("@_**name**"), which shows the name without a notification.
var reZulipMention = regexp.MustCompile(`@\*\*`)

func zulipRuneCost(rune) int { return 1 }

// renderZulipText prepares one line of prose for Zulip.
func renderZulipText(s string) string {
	s = reSlackLink.ReplaceAllString(s, "[$2]($1)")
	return reZulipMention.ReplaceAllString(s, "@_**")
}

func zulipLineUnits(line string, limit int, sep string) []msgUnit {
	var out []msgUnit
	for i, piece := range hardSplit(line, limit, zulipRuneCost) {
		u := msgUnit{text: piece, sep: "\n"}
		if i == 0 {
			u.sep = sep
		}
		out = append(out, u)
	}
	return out
}

func zulipBlockUnits(b mdBlock, limit int, sep string) []msgUnit {
	nextSep := func(out []msgUnit) string {
		if len(out) == 0 {
			return sep
		}
		return "\n"
	}
	switch b.kind {
	case mdHeading:
		line := strings.Repeat("#", b.level) + " " + renderZulipText(strings.TrimSpace(b.lines[0]))
		return zulipLineUnits(line, limit, sep)
	case mdRule:
		return []msgUnit{{text: "———", sep: sep}}
	case mdCode:
		open, closeFence := "```"+b.lang+"\n", "\n```"
		budget := limit - len([]rune(open)) - len([]rune(closeFence))
		var out []msgUnit
		for _, piece := range packLines(b.lines, budget, zulipRuneCost) {
			out = append(out, msgUnit{text: open + piece + closeFence, sep: nextSep(out)})
		}
		if len(out) == 0 {
			out = append(out, msgUnit{text: open + closeFence, sep: sep})
		}
		return out
	case mdQuote:
		var out []msgUnit
		for _, l := range b.lines {
			out = append(out, zulipLineUnits("> "+renderZulipText(trimHardBreak(l)), limit, nextSep(out))...)
		}
		return out
	default: // paragraph, list, table: Zulip renders Markdown, tables included
		var out []msgUnit
		for _, l := range b.lines {
			out = append(out, zulipLineUnits(renderZulipText(trimHardBreak(l)), limit, nextSep(out))...)
		}
		return out
	}
}

// RenderZulip adapts provider-neutral Markdown for Zulip (Slack-style links
// become Markdown links, mentions become silent) and splits it into messages
// of at most limit characters. Splits happen between blocks or lines; a code
// block that is too long is closed and reopened in the next message.
func RenderZulip(markdown string, limit int) []string {
	if limit <= 0 {
		limit = ZulipMaxMessageChars
	}
	var units []msgUnit
	for _, b := range parseMarkdownBlocks(markdown) {
		units = append(units, zulipBlockUnits(b, limit, "\n\n")...)
	}
	return packUnits(units, limit)
}

// zulipTopicName trims a topic to Zulip's topic limit.
func zulipTopicName(topic string) string {
	topic = strings.Join(strings.Fields(topic), " ")
	if r := []rune(topic); len(r) > ZulipMaxTopicChars {
		topic = strings.TrimSpace(string(r[:ZulipMaxTopicChars-1])) + "…"
	}
	return topic
}
