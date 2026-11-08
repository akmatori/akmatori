package services

import "regexp"

// RedactedSecret replaces a secret found by RedactSecrets.
const RedactedSecret = "[REDACTED_SECRET]"

// secretPatterns are applied in order; each entry replaces the matched text
// with its template (the capture groups keep the context around the secret).
// The set is OpenAI Codex's memory redaction (Bearer tokens, sk- keys, AWS
// access key ids, key=value credentials) plus URL passwords and private key
// blocks. Best effort: a secret with no recognisable shape (a bare password
// in prose) is not caught. The gateway mirrors the list
// (mcp-gateway/internal/tools/knowledge/redact.go); both modules run
// tests/fixtures/secret_redaction_cases.json.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// A PEM private key block, terminated or cut off.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`), RedactedSecret},
	// scheme://user:password@host → the password.
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]+:)[^\s@/]+@`), "${1}" + RedactedSecret + "@"},
	// Authorization: Bearer <token>.
	{regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9\-._~+/]{8,}=*`), "${1}" + RedactedSecret},
	// OpenAI-style keys (sk-…, sk-ant-…, sk-proj-…).
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`), RedactedSecret},
	// AWS access key ids.
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), RedactedSecret},
	// api_key / token / secret / password [:=] value (value of 8+ chars, also
	// as a quoted JSON or YAML field; a short value such as "none" stays).
	{regexp.MustCompile(`(?i)\b((?:[a-z0-9_-]*[_-])?(?:api[_-]?key|apikey|token|secret|password|passwd|pwd)\b["']?\s*[:=]\s*["']?)[^\s"',;]{8,}`), "${1}" + RedactedSecret},
}

// RedactSecrets replaces the secrets it recognises in s with RedactedSecret
// and returns the number of replacements. It runs on everything the
// post-incident memory pass reads and writes, on memory.write in the gateway
// and on proposal approval, so a token that an agent saw during a run never
// becomes a memory.
func RedactSecrets(s string) (string, int) {
	n := 0
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllStringFunc(s, func(m string) string {
			n++
			return p.re.ReplaceAllString(m, p.repl)
		})
	}
	return s, n
}
