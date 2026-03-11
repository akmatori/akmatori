package utils

import (
	"regexp"
	"strings"
)

// Pre-compiled regexes for Slack mrkdwn stripping
var (
	// Links with display text: <url|text> → text
	slackLinkWithText = regexp.MustCompile(`<([^|>]+)\|([^>]+)>`)
	// User mentions: <@U...> → remove
	slackUserMention = regexp.MustCompile(`<@U[A-Z0-9]+>`)
	// Channel mentions with name: <#C...|name> → #name
	slackChannelWithName = regexp.MustCompile(`<#C[A-Z0-9]+\|([^>]+)>`)
	// Channel mentions without name: <#C...> → remove
	slackChannelNoName = regexp.MustCompile(`<#C[A-Z0-9]+>`)
	// Bare links: <url> → url
	slackBareLink = regexp.MustCompile(`<([^>]+)>`)
	// Emoji codes: :word: → remove
	slackEmoji = regexp.MustCompile(`:[a-z0-9_+-]+:`)
	// Bold: *text* → text (non-greedy, no newlines)
	slackBold = regexp.MustCompile(`\*([^*\n]+)\*`)
	// Strikethrough: ~text~ → text (non-greedy, no newlines)
	slackStrike = regexp.MustCompile(`~([^~\n]+)~`)
	// Inline code: `text` → text (non-greedy, no newlines)
	slackCode = regexp.MustCompile("`([^`\n]+)`")
	// Multiple spaces → single space
	multipleSpaces = regexp.MustCompile(`\s{2,}`)
)

// StripSlackMrkdwn removes Slack mrkdwn formatting from text, returning clean plaintext.
// Transformation order matters — links must be processed before bare angle brackets.
func StripSlackMrkdwn(text string) string {
	if text == "" {
		return ""
	}

	// 1. User mentions: <@U...> → remove (before generic link patterns)
	text = slackUserMention.ReplaceAllString(text, "")

	// 2. Channel mentions with name: <#C...|name> → #name (before generic link patterns)
	text = slackChannelWithName.ReplaceAllString(text, "#$1")

	// 3. Channel mentions without name: <#C...> → remove
	text = slackChannelNoName.ReplaceAllString(text, "")

	// 4. Links with display text: <url|text> → text
	text = slackLinkWithText.ReplaceAllString(text, "$2")

	// 5. Bare links (must come after all specific patterns): <url> → url
	text = slackBareLink.ReplaceAllString(text, "$1")

	// 6. Emoji codes: :emoji: → remove
	text = slackEmoji.ReplaceAllString(text, "")

	// 7. Bold: *text* → text
	text = slackBold.ReplaceAllString(text, "$1")

	// 8. Strikethrough: ~text~ → text
	text = slackStrike.ReplaceAllString(text, "$1")

	// 9. Inline code: `text` → text
	text = slackCode.ReplaceAllString(text, "$1")

	// 10. HTML entities
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")

	// 11. Collapse whitespace and trim
	text = multipleSpaces.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	return text
}
