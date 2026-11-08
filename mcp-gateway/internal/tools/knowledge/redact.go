package knowledge

import "regexp"

// redactedSecret replaces a secret found by redactSecrets.
const redactedSecret = "[REDACTED_SECRET]"

// secretPatterns mirrors the API's services.RedactSecrets
// (internal/services/secret_redaction.go): PEM private key blocks, URL
// passwords, Bearer tokens, sk- keys, AWS access key ids and key=value
// credentials. Both modules run tests/fixtures/secret_redaction_cases.json,
// so a drift fails both.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`), redactedSecret},
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]+:)[^\s@/]+@`), "${1}" + redactedSecret + "@"},
	{regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9\-._~+/]{8,}=*`), "${1}" + redactedSecret},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`), redactedSecret},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), redactedSecret},
	{regexp.MustCompile(`(?i)\b((?:[a-z0-9_-]*[_-])?(?:api[_-]?key|apikey|token|secret|password|passwd|pwd)\b["']?\s*[:=]\s*["']?)[^\s"',;]{8,}`), "${1}" + redactedSecret},
}

// redactSecrets replaces the secrets it recognises in s and returns the
// number of replacements. memory.write runs it on the description, body and
// evidence note, so a request run or Dreaming cannot store a token.
func redactSecrets(s string) (string, int) {
	n := 0
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllStringFunc(s, func(m string) string {
			n++
			return p.re.ReplaceAllString(m, p.repl)
		})
	}
	return s, n
}

// redactMemoryInput redacts the three text fields of a write and returns
// the total number of replacements.
func redactMemoryInput(description, body, note string) (string, string, string, int) {
	var n1, n2, n3 int
	description, n1 = redactSecrets(description)
	body, n2 = redactSecrets(body)
	note, n3 = redactSecrets(note)
	return description, body, note, n1 + n2 + n3
}
