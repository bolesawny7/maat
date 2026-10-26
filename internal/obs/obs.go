// Package obs defines the shared vocabulary that collectors produce and the
// condense pipeline and MCP tools consume: severities, normalized log records,
// stable evidence identifiers, and output budgets.
//
// Nothing in this package talks to the operating system, so it builds and is
// tested on every platform.
package obs

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// Severity mirrors the syslog priority scale (0 = emerg .. 7 = debug). Lower is
// more urgent, matching the numbers journald reports in its PRIORITY field.
type Severity int

const (
	SevEmergency Severity = iota
	SevAlert
	SevCritical
	SevError
	SevWarning
	SevNotice
	SevInfo
	SevDebug
)

// severityNames is indexed by Severity.
var severityNames = [...]string{
	"emergency", "alert", "critical", "error",
	"warning", "notice", "info", "debug",
}

// String returns the lower-case name of the severity, or "unknown".
func (s Severity) String() string {
	if s < 0 || int(s) >= len(severityNames) {
		return "unknown"
	}
	return severityNames[s]
}

// AtLeast reports whether s is at least as urgent as min (i.e. s <= min on the
// syslog scale). SevError.AtLeast(SevWarning) is true; SevInfo.AtLeast is not.
func (s Severity) AtLeast(min Severity) bool { return s <= min }

// LogRecord is one normalized log entry, independent of where it came from
// (the journal, /dev/kmsg, or a file). Collectors fill it in; the condense
// pipeline redacts, clusters, ranks, and budgets collections of these.
type LogRecord struct {
	Time     time.Time `json:"time"`
	Source   string    `json:"source"`             // e.g. "journal", "kernel"
	Unit     string    `json:"unit,omitempty"`     // systemd unit, if known
	Priority Severity  `json:"priority"`           //
	Message  string    `json:"message"`            // human-readable text (redacted before leaving the process)
	Hostname string    `json:"hostname,omitempty"` //
	// Cursor is an opaque, journald-provided position. It is never shown to a
	// model; it exists so humans can locate the exact entry later.
	Cursor string `json:"-"`
}

// Untrusted marks text that originated outside maat (log message bodies, unit
// descriptions, process command lines). The MCP layer serializes these into
// clearly labelled data fields so they are never spliced into instructions.
// The type is deliberately distinct from string so that a reviewer of the code
// can see, at each call site, where attacker-controlled text flows.
type Untrusted string

// EvidenceID returns a short, stable identifier for a piece of evidence,
// derived from its defining parts. The same parts always yield the same ID, so
// a diagnosis that cites "ev_7c1e09" can be checked against the record that
// produced it. Parts should identify the evidence, not vary run to run: use a
// source and a template, not a timestamp.
func EvidenceID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return "ev_" + hex.EncodeToString(h[:])[:6]
}

// Budget bounds how much a tool returns. Zero means "use the default".
type Budget struct {
	// MaxItems caps the number of top-level items (templates, units, rows).
	MaxItems int `json:"max_items,omitempty"`
	// MaxChars caps the estimated size of the rendered result. The estimate is
	// character-based unless a model-specific tokenizer is configured.
	MaxChars int `json:"max_chars,omitempty"`
}

// Default budget values, chosen to stay comfortably within a small local
// model's context window while still carrying enough signal to diagnose.
const (
	DefaultMaxItems = 40
	DefaultMaxChars = 8000
)

// WithDefaults returns a copy of b with any zero field replaced by its default.
func (b Budget) WithDefaults() Budget {
	if b.MaxItems <= 0 {
		b.MaxItems = DefaultMaxItems
	}
	if b.MaxChars <= 0 {
		b.MaxChars = DefaultMaxChars
	}
	return b
}

// SortRecordsByTime orders records oldest-first, in place, using the cursor as
// a tie-breaker so the order is deterministic even when timestamps collide.
func SortRecordsByTime(rs []LogRecord) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Time.Equal(rs[j].Time) {
			return rs[i].Cursor < rs[j].Cursor
		}
		return rs[i].Time.Before(rs[j].Time)
	})
}
