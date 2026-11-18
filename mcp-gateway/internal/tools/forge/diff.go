package forge

import (
	"fmt"
	"strconv"
	"strings"
)

// SplitDiff returns the per-file sections of a unified git diff, keyed by the
// new path (and the old path, for renames and deletions). order lists the
// new paths in diff order.
func SplitDiff(diff string) (order []string, sections map[string]string) {
	sections = map[string]string{}
	var cur strings.Builder
	var names []string
	flush := func() {
		if len(names) == 0 {
			return
		}
		text := cur.String()
		order = append(order, names[0])
		for _, n := range names {
			if _, ok := sections[n]; !ok {
				sections[n] = text
			}
		}
	}
	for _, line := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur.Reset()
			names = diffGitNames(strings.TrimRight(line, "\r\n"))
		}
		if len(names) > 0 {
			cur.WriteString(line)
		}
	}
	flush()
	return order, sections
}

// diffGitNames reads `diff --git a/<old> b/<new>` and returns the new path
// first. Git quotes each path on its own (C-style, when it has special or
// non-ASCII characters), so either side can be quoted. An unquoted old path
// with spaces is ambiguous; it is split at the first " b/" (or ` "b/`).
func diffGitNames(header string) []string {
	rest := strings.TrimPrefix(header, "diff --git ")
	var oldRaw, newRaw string
	if strings.HasPrefix(rest, "\"") {
		end := quotedEnd(rest)
		if end < 0 || end+1 >= len(rest) || rest[end+1] != ' ' {
			return []string{rest}
		}
		oldRaw, newRaw = rest[:end+1], rest[end+2:]
	} else {
		i := strings.Index(rest, " b/")
		if j := strings.Index(rest, " \"b/"); j >= 0 && (i < 0 || j < i) {
			i = j
		}
		if i < 0 {
			return []string{rest}
		}
		oldRaw, newRaw = rest[:i], rest[i+1:]
	}
	oldP, ok1 := diffPath(oldRaw, "a/")
	newP, ok2 := diffPath(newRaw, "b/")
	if !ok1 || !ok2 {
		return []string{rest}
	}
	if newP == oldP {
		return []string{newP}
	}
	return []string{newP, oldP}
}

// quotedEnd returns the index of the closing quote of the C-style quoted
// string at the start of s, or -1.
func quotedEnd(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// diffPath unquotes one header path (if quoted) and drops its a/ or b/ prefix.
func diffPath(raw, prefix string) (string, bool) {
	if strings.HasPrefix(raw, "\"") {
		u, err := strconv.Unquote(raw)
		if err != nil {
			return "", false
		}
		raw = u
	}
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	return raw[len(prefix):], true
}

// DiffView describes one get_pull_request_diff answer.
type DiffView struct {
	PR   PR
	Diff string // the full (or partly read) unified diff
	// Over reports that the download stopped at MaxRawResponseBytes.
	Over bool
	// File selects one file's section; empty = the whole diff.
	File     string
	MaxBytes int
	// Note is an extra line under the header (e.g. how the diff was built).
	Note string
}

// RenderDiff cuts the diff (or the selected file's section) at MaxBytes and
// adds a [TRUNCATED ...] marker with the next step for the agent.
func RenderDiff(v DiffView) (string, error) {
	order, sections := SplitDiff(v.Diff)
	diff := v.Diff
	header := fmt.Sprintf("# %s#%d diff", v.PR.Repo(), v.PR.Number)
	partial := false
	if v.File != "" {
		sec, ok := sections[v.File]
		if !ok {
			hint := ""
			if v.Over {
				hint = fmt.Sprintf(" (the diff is over %d MB and was read only in part; the file may be in the unread part)", MaxRawResponseBytes/(1024*1024))
			}
			names := order
			if len(names) > 30 {
				names = names[:30]
			}
			return "", fmt.Errorf("file %q is not in the diff of %s#%d%s; files: %s", v.File, v.PR.Repo(), v.PR.Number, hint, strings.Join(names, ", "))
		}
		header += " — file " + v.File
		diff = sec
		// The download cap stops inside the last section, so only that file
		// can be partial.
		partial = v.Over && len(order) > 0 && sections[order[len(order)-1]] == sec
	} else {
		partial = v.Over
		header += fmt.Sprintf(" — %d files", len(order))
	}

	total := len(diff)
	body, truncated := Cut(diff, v.MaxBytes)
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	if v.Note != "" {
		b.WriteString(v.Note)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(body)
	if truncated || partial {
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		size := fmt.Sprintf("%d bytes", total)
		if partial {
			size = fmt.Sprintf("over %d bytes", total)
		}
		hint := fmt.Sprintf("Call list_pull_request_files, then get_pull_request_diff with file=<path> for each file, or raise max_bytes (max %d).", MaxMaxBytes)
		switch {
		case v.File != "" && partial && !truncated:
			hint = fmt.Sprintf("The pull request diff is over %d MB and was read only in part, so the end of this file's diff is missing. Read the file with get_file at the head sha instead.", MaxRawResponseBytes/(1024*1024))
		case v.File != "":
			hint = fmt.Sprintf("Raise max_bytes (max %d) to see more of this file.", MaxMaxBytes)
		}
		fmt.Fprintf(&b, "\n[TRUNCATED: showed %d of %s. %s]\n", len(body), size, hint)
	}
	return b.String(), nil
}

// RenderFile lays out a get_file answer. raw must hold up to maxBytes+3 bytes
// (so the cut can end on a rune boundary); over reports that the file is
// longer than what was read.
func RenderFile(repo, path, ref string, raw []byte, over bool, maxBytes int) string {
	refText := ref
	if refText == "" {
		refText = "default branch"
	}
	head := fmt.Sprintf("# %s:%s @ %s\n\n", repo, path, refText)
	sample := raw
	if len(sample) > 8000 {
		sample = sample[:8000]
	}
	if strings.IndexByte(string(sample), 0) >= 0 {
		return head + "[binary file; content not shown]\n"
	}
	text, cutHere := Cut(string(raw), maxBytes)
	if over || cutHere {
		shown := len(text)
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += fmt.Sprintf("\n[TRUNCATED: showed the first %d bytes. Raise max_bytes (max %d) to see more.]\n", shown, MaxMaxBytes)
	}
	return head + text
}
