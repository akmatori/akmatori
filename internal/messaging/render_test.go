package messaging

import (
	"strings"
	"testing"
)

func TestRenderTelegram(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"escape", "a < b && c > d", "a &lt; b &amp;&amp; c &gt; d"},
		{"bold", "**Status:** ok", "<b>Status:</b> ok"},
		{"italic star and underscore", "*slow* and _fast_", "<i>slow</i> and <i>fast</i>"},
		{"snake_case untouched", "check host_name_x now", "check host_name_x now"},
		{"strike", "~~old~~ new", "<s>old</s> new"},
		{"inline code escaped, no formatting inside", "run `a<b> **x**`", "run <code>a&lt;b&gt; **x**</code>"},
		{"link", "see [the *run*](https://x.example/r?a=1&b=2)", `see <a href="https://x.example/r?a=1&amp;b=2">the <i>run</i></a>`},
		{"slack link", "<https://x.example|incident>", `<a href="https://x.example">incident</a>`},
		{"heading", "## Root cause", "<b>Root cause</b>"},
		{"heading with bold", "### **Done**", "<b>Done</b>"},
		{"list", "- one\n- two\n  - nested\n1. first", "• one\n• two\n    • nested\n1. first"},
		{"quote", "> line 1\n> **line 2**", "<blockquote>line 1\n<b>line 2</b></blockquote>"},
		{"code block", "```go\nif a < b {}\n```", `<pre><code class="language-go">if a &lt; b {}</code></pre>`},
		{"code block no lang", "```\nx\n```", "<pre>x</pre>"},
		{"table", "| Host | CPU |\n|---|---|\n| web-1 | 90% |", "• <b>Host:</b> web-1 | <b>CPU:</b> 90%"},
		{"rule and paragraphs", "a\n\n---\n\nb", "a\n\n———\n\nb"},
		{"hard breaks", "**A:** 1  \n**B:** 2  ", "<b>A:</b> 1\n<b>B:</b> 2"},
		{"emoji shortcode", ":red_circle: **Alert** at 10:30:00", "🔴 <b>Alert</b> at 10:30:00"},
		{"crossing emphasis stays balanced", "**a ~~b** c~~", "**a <s>b** c</s>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderTelegram(tt.in, TelegramMaxMessageChars)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("RenderTelegram(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// assertBalanced checks every opened Telegram tag is closed in each chunk and
// no entity is cut.
func assertTelegramChunkOK(t *testing.T, chunk string, limit int) {
	t.Helper()
	if n := utf16Len(chunk); n > limit {
		t.Errorf("chunk len %d > limit %d", n, limit)
	}
	for _, tag := range []string{"b", "i", "s", "code", "pre", "blockquote", "a"} {
		opens := strings.Count(chunk, "<"+tag+">") + strings.Count(chunk, "<"+tag+" ")
		closes := strings.Count(chunk, "</"+tag+">")
		if opens != closes {
			t.Errorf("unbalanced <%s> (%d open, %d close) in chunk %q", tag, opens, closes, chunk)
		}
	}
	if i := strings.LastIndex(chunk, "&"); i >= 0 && !strings.Contains(chunk[i:], ";") {
		t.Errorf("chunk ends inside an entity: %q", chunk[i:])
	}
}

func TestRenderTelegram_Splitting(t *testing.T) {
	const limit = 60
	tests := []struct {
		name      string
		in        string
		minChunks int
	}{
		{"paragraphs", strings.Repeat("**bold** words & more words here.\n\n", 10), 3},
		{"long single line", strings.Repeat("word&word ", 40), 3},
		{"code block", "```\n" + strings.Repeat("x < y\n", 40) + "```", 3},
		{"quote", strings.Repeat("> quoted **line** here\n", 20), 3},
		{"list", strings.Repeat("- item with [link](https://e.x/a)\n", 20), 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := RenderTelegram(tt.in, limit)
			if len(chunks) < tt.minChunks {
				t.Fatalf("got %d chunks, want >= %d: %q", len(chunks), tt.minChunks, chunks)
			}
			for _, c := range chunks {
				assertTelegramChunkOK(t, c, limit)
			}
		})
	}
}

func TestRenderTelegram_CodeBlockSplitReopens(t *testing.T) {
	in := "```sh\n" + strings.Repeat("echo hello\n", 30) + "```"
	chunks := RenderTelegram(in, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected split, got %q", chunks)
	}
	for _, c := range chunks {
		if !strings.HasPrefix(c, `<pre><code class="language-sh">`) || !strings.HasSuffix(c, "</code></pre>") {
			t.Errorf("chunk not a complete pre block: %q", c)
		}
	}
}

func TestTelegramHTMLToPlain(t *testing.T) {
	in := RenderTelegram("**A** & [run](https://x.example/r) `c<d`", TelegramMaxMessageChars)[0]
	got := telegramHTMLToPlain(in)
	want := "A & run (https://x.example/r) c<d"
	if got != want {
		t.Errorf("plain = %q, want %q", got, want)
	}
}

func TestRenderDiscord(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"markdown passthrough", "**bold** *it* [l](https://x) `c`", "**bold** *it* [l](https://x) `c`"},
		{"headings 1-3 kept", "## Title", "## Title"},
		{"heading 4 to bold", "#### Deep", "**Deep**"},
		{"table", "| Host | CPU |\n|---|---|\n| web-1 | 90% |", "• **Host:** web-1 | **CPU:** 90%"},
		{"rule", "a\n\n***\n\nb", "a\n\n———\n\nb"},
		{"nested list kept", "- a\n  - b", "- a\n  - b"},
		{"emoji", ":warning: hot", "⚠️ hot"},
		{"slack link", "see <https://x.example|incident>", "see [incident](https://x.example)"},
		{"hard break", "a  \nb", "a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderDiscord(tt.in, DiscordMaxMessageChars)
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("RenderDiscord(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderDiscord_SplitsAndReopensFence(t *testing.T) {
	in := "Intro line\n\n```py\n" + strings.Repeat("print('hello world')\n", 200) + "```\n\nOutro"
	chunks := RenderDiscord(in, DiscordMaxMessageChars)
	if len(chunks) < 3 {
		t.Fatalf("expected >= 3 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := utf16Len(c); n > DiscordMaxMessageChars {
			t.Errorf("chunk %d len %d > limit", i, n)
		}
		if strings.Count(c, "```")%2 != 0 {
			t.Errorf("chunk %d has an unclosed fence: %q", i, c)
		}
	}
	if !strings.HasPrefix(chunks[0], "Intro line") || !strings.HasSuffix(chunks[len(chunks)-1], "Outro") {
		t.Errorf("order not preserved: first %q last %q", chunks[0][:20], chunks[len(chunks)-1])
	}
	if !strings.Contains(chunks[1], "```py\n") {
		t.Errorf("reopened fence lost its language: %q", chunks[1][:20])
	}
}

func TestRenderDiscord_LongLineHardSplit(t *testing.T) {
	chunks := RenderDiscord(strings.Repeat("abcdefghij ", 500), DiscordMaxMessageChars)
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	for _, c := range chunks {
		if utf16Len(c) > DiscordMaxMessageChars {
			t.Errorf("chunk too long: %d", utf16Len(c))
		}
	}
}

func TestHardSplit_NeverSplitsRunes(t *testing.T) {
	parts := hardSplit(strings.Repeat("😀", 10), 4, utf16RuneCost)
	if len(parts) != 5 {
		t.Fatalf("got %d parts, want 5: %q", len(parts), parts)
	}
	for _, p := range parts {
		if p != "😀😀" {
			t.Errorf("bad part %q", p)
		}
	}
}
