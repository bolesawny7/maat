// Package condense turns raw, voluminous system data into compact, safe input
// for a language model: it redacts secrets, fences untrusted text, clusters log
// lines into templates (Drain), ranks them, and trims the result to a budget.
//
// It has no operating-system dependencies and is tested on every platform.
package condense

import (
	"regexp"
	"strings"

	"github.com/bolesawny7/maat/internal/obs"
)

// Redactor removes secrets from text before it leaves the maat process.
//
// Redaction is best-effort and pattern-based: it catches common shapes of
// credential, not every possible secret. It is one defence among several. The
// kernel-enforced absence of a network plug (the server cannot exfiltrate
// anything) and the option to run a fully local model are what actually
// contain a leak; redaction reduces how much a human reviewer or a hosted
// model ever sees.
type Redactor struct {
	rules []redactRule
}

type redactRule struct {
	name string
	re   *regexp.Regexp
	// repl builds the replacement for a match. It receives the full match and
	// the submatch indices so a rule can keep a non-secret prefix (e.g. the
	// user and host of a URL) and mask only the secret group.
	repl func(full string, groups []string) string
}

// placeholder is what every redacted secret becomes.
const placeholder = "«redacted»"

// NewRedactor returns a Redactor with maat's default rule set plus any
// user-supplied extra patterns. Each extra pattern is a regexp; the whole match
// is replaced with the placeholder. Invalid extra patterns are ignored by
// NewRedactor; use CompileExtra to detect them.
func NewRedactor(extra ...string) *Redactor {
	r := &Redactor{rules: defaultRules()}
	for _, pat := range extra {
		if re, err := regexp.Compile(pat); err == nil {
			r.rules = append(r.rules, redactRule{
				name: "custom",
				re:   re,
				repl: func(string, []string) string { return placeholder },
			})
		}
	}
	return r
}

// CompileExtra validates user-supplied patterns, returning the first error so a
// bad config fails loudly rather than silently skipping a redaction rule.
func CompileExtra(patterns []string) error {
	for _, p := range patterns {
		if _, err := regexp.Compile(p); err != nil {
			return err
		}
	}
	return nil
}

func wholeMatch(string, []string) string { return placeholder }

func defaultRules() []redactRule {
	rules := []redactRule{
		{
			// Credentials embedded in a URL: scheme://user:secret@host.
			// Keep the scheme, user, and host; mask only the password.
			name: "url-credentials",
			re:   regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+):([^\s:/@]+)@`),
			repl: func(_ string, g []string) string { return g[1] + ":" + placeholder + "@" },
		},
		{
			// PEM private-key header. The body spans many lines and journald
			// stores one entry per line; masking the header neutralizes the
			// block for a reader and flags it for redaction counting.
			name: "private-key-header",
			re:   regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )?PRIVATE KEY-----`),
			repl: wholeMatch,
		},
		{
			name: "jwt",
			re:   regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`),
			repl: wholeMatch,
		},
		{
			name: "authorization-header",
			re:   regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:bearer\s+|basic\s+)?[A-Za-z0-9._~+/=-]{8,}`),
			repl: func(_ string, g []string) string { return g[1] + placeholder },
		},
		{
			name: "bearer-token",
			re:   regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
			repl: wholeMatch,
		},
		{
			name: "aws-access-key-id",
			re:   regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA)[A-Z0-9]{16}\b`),
			repl: wholeMatch,
		},
		{
			name: "github-token",
			re:   regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
			repl: wholeMatch,
		},
		{
			name: "slack-token",
			re:   regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
			repl: wholeMatch,
		},
		{
			name: "private-key-pem-oneline",
			re:   regexp.MustCompile(`-----BEGIN [A-Z ]+-----[\s\S]*?-----END [A-Z ]+-----`),
			repl: wholeMatch,
		},
		{
			// Generic "key = value" secrets: a field whose name looks sensitive
			// followed by a long-enough value. Keep the field name and the
			// delimiter so the log stays readable.
			name: "key-value-secret",
			re:   regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|apikey|access[_-]?token|auth[_-]?token|client[_-]?secret|private[_-]?key)\b(\s*[:=]\s*)(?:"[^"]{6,}"|'[^']{6,}'|[^\s"',;]{6,})`),
			repl: func(_ string, g []string) string { return g[1] + g[2] + placeholder },
		},
	}
	return rules
}

// Redact returns the redacted text and the number of substitutions made. It is
// safe to call on any string, including empty strings, and never panics.
func (r *Redactor) Redact(s string) (string, int) {
	count := 0
	for _, rule := range r.rules {
		s = rule.re.ReplaceAllStringFunc(s, func(match string) string {
			count++
			if rule.repl == nil {
				return placeholder
			}
			groups := rule.re.FindStringSubmatch(match)
			return rule.repl(match, groups)
		})
	}
	return s, count
}

// RedactUntrusted redacts and returns a value still typed as obs.Untrusted, so
// the taint survives redaction and the MCP layer keeps fencing it.
func (r *Redactor) RedactUntrusted(s obs.Untrusted) (obs.Untrusted, int) {
	red, n := r.Redact(string(s))
	return obs.Untrusted(red), n
}

// RedactRecords redacts the Message field of each record in place and returns
// the total number of substitutions.
func (r *Redactor) RedactRecords(recs []obs.LogRecord) int {
	total := 0
	for i := range recs {
		red, n := r.Redact(recs[i].Message)
		recs[i].Message = red
		total += n
	}
	return total
}

// looksSensitiveField reports whether a field name suggests its value is a
// secret. Exposed for reuse by structured collectors (e.g. environment dumps).
func looksSensitiveField(name string) bool {
	name = strings.ToLower(name)
	for _, hint := range []string{"password", "passwd", "secret", "token", "apikey", "api_key", "api-key", "private_key", "private-key", "credential"} {
		if strings.Contains(name, hint) {
			return true
		}
	}
	return false
}
