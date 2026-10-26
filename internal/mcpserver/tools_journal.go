package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/collect"
	"github.com/bolesawny7/maat/internal/condense"
	"github.com/bolesawny7/maat/internal/obs"
)

// ---- journal_summary ----

type journalSummaryIn struct {
	Unit         string `json:"unit,omitempty" jsonschema:"restrict to a systemd unit"`
	MinPriority  int    `json:"min_priority,omitempty" jsonschema:"most-verbose syslog priority to include (1 alert to 7 debug); default 7"`
	SinceSeconds int    `json:"since_seconds,omitempty" jsonschema:"how far back to read, in seconds"`
	MaxItems     int    `json:"max_items,omitempty"`
	MaxChars     int    `json:"max_chars,omitempty"`
}

func (d *Deps) journalSummary(ctx context.Context, _ *mcp.CallToolRequest, in journalSummaryIn) (*mcp.CallToolResult, condense.JournalSummary, error) {
	cctx, cancel := ctxWithTimeout(ctx, 15*time.Second)
	defer cancel()
	recs, err := collect.ReadJournal(cctx, journalOptions(in.Unit, in.MinPriority, in.SinceSeconds, false))
	if err != nil {
		return errResult(err), condense.JournalSummary{}, nil
	}
	sum := condense.Summarize(recs, d.Redactor, condense.NewDrain(), d.effectiveBudget(in.MaxItems, in.MaxChars))
	d.registerTemplates("journal", sum)
	return textResult(fmt.Sprintf("%d entries across %d templates (%d shown), %d redactions", sum.TotalRecords, sum.TotalTemplates, sum.Shown, sum.Redactions)), sum, nil
}

func (d *Deps) registerTemplates(source string, sum condense.JournalSummary) {
	for _, t := range sum.Templates {
		subject := ""
		if len(t.Units) > 0 {
			subject = t.Units[0]
		}
		d.evidence.add(t.EvidenceID, source, subject, fmt.Sprintf("x%d, %s: %s", t.Count, t.MaxPriority, t.Example))
	}
}

// ---- journal_entries ----

type journalEntriesIn struct {
	Unit         string `json:"unit,omitempty"`
	MinPriority  int    `json:"min_priority,omitempty" jsonschema:"most-verbose syslog priority to include (1 alert to 7 debug); default 7"`
	SinceSeconds int    `json:"since_seconds,omitempty"`
	Lines        int    `json:"lines,omitempty" jsonschema:"max entries to read before budgeting"`
	MaxChars     int    `json:"max_chars,omitempty"`
}

type entry struct {
	EvidenceID string        `json:"evidence_id"`
	Time       time.Time     `json:"time"`
	Unit       string        `json:"unit,omitempty"`
	Priority   string        `json:"priority"`
	Message    obs.Untrusted `json:"message"`
}

type journalEntriesOut struct {
	Entries   []entry `json:"entries"`
	Total     int     `json:"total"`
	Truncated bool    `json:"truncated"`
}

// journalEntries returns the newest entries that fit the budget, oldest first.
func (d *Deps) journalEntries(ctx context.Context, _ *mcp.CallToolRequest, in journalEntriesIn) (*mcp.CallToolResult, journalEntriesOut, error) {
	cctx, cancel := ctxWithTimeout(ctx, 15*time.Second)
	defer cancel()
	opts := journalOptions(in.Unit, in.MinPriority, in.SinceSeconds, false)
	if in.Lines > 0 {
		opts.Lines = in.Lines
	}
	recs, err := collect.ReadJournal(cctx, opts)
	if err != nil {
		return errResult(err), journalEntriesOut{}, nil
	}
	obs.SortRecordsByTime(recs)
	budget := d.effectiveBudget(0, in.MaxChars)
	out := journalEntriesOut{Total: len(recs)}
	chars := 0
	var newestFirst []entry
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		msg := d.redact(obs.Untrusted(r.Message))
		e := entry{
			EvidenceID: obs.EvidenceID("journal-entry", r.Time.Format(time.RFC3339Nano), r.Unit, string(msg)),
			Time:       r.Time, Unit: r.Unit, Priority: r.Priority.String(), Message: msg,
		}
		cost := len(msg) + len(e.Unit) + 90
		if len(newestFirst) > 0 && chars+cost > budget.MaxChars {
			out.Truncated = true
			break
		}
		chars += cost
		newestFirst = append(newestFirst, e)
	}
	for i := len(newestFirst) - 1; i >= 0; i-- {
		e := newestFirst[i]
		out.Entries = append(out.Entries, e)
		d.evidence.add(e.EvidenceID, "journal", e.Unit, string(e.Message))
	}
	return textResult(fmt.Sprintf("%d entries (newest %d shown)", out.Total, len(out.Entries))), out, nil
}

// ---- kernel_messages ----

type kernelIn struct {
	SinceSeconds int `json:"since_seconds,omitempty"`
	MaxItems     int `json:"max_items,omitempty"`
	MaxChars     int `json:"max_chars,omitempty"`
}

func (d *Deps) kernelMessages(ctx context.Context, _ *mcp.CallToolRequest, in kernelIn) (*mcp.CallToolResult, condense.JournalSummary, error) {
	cctx, cancel := ctxWithTimeout(ctx, 15*time.Second)
	defer cancel()
	opts := collect.JournalOptions{Kernel: true}
	if in.SinceSeconds > 0 {
		opts.Since = time.Duration(in.SinceSeconds) * time.Second
	}
	recs, err := collect.ReadJournal(cctx, opts)
	if err != nil {
		return errResult(err), condense.JournalSummary{}, nil
	}
	sum := condense.Summarize(recs, d.Redactor, condense.NewDrain(), d.effectiveBudget(in.MaxItems, in.MaxChars))
	d.registerTemplates("kernel", sum)
	return textResult(fmt.Sprintf("%d kernel messages across %d templates", sum.TotalRecords, sum.TotalTemplates)), sum, nil
}

// ---- apparmor_denials ----

type apparmorIn struct {
	SinceSeconds int `json:"since_seconds,omitempty"`
	Max          int `json:"max,omitempty"`
	MaxChars     int `json:"max_chars,omitempty"`
}

type apparmorOut struct {
	Denials   []collect.AppArmorDenial `json:"denials"`
	Total     int                      `json:"total"`
	Truncated bool                     `json:"truncated,omitempty"`
}

func (d *Deps) apparmorDenials(ctx context.Context, _ *mcp.CallToolRequest, in apparmorIn) (*mcp.CallToolResult, apparmorOut, error) {
	cctx, cancel := ctxWithTimeout(ctx, 15*time.Second)
	defer cancel()
	opts := collect.JournalOptions{Kernel: true}
	if in.SinceSeconds > 0 {
		opts.Since = time.Duration(in.SinceSeconds) * time.Second
	}
	recs, err := collect.ReadJournal(cctx, opts)
	if err != nil {
		return errResult(err), apparmorOut{}, nil
	}
	denials := collect.AppArmorDenials(recs)
	for i := range denials {
		denials[i].Name = d.redact(denials[i].Name)
		denials[i].Comm = d.redact(denials[i].Comm)
	}
	out := apparmorOut{Total: len(denials)}
	out.Denials, out.Truncated = fit(denials, d.effectiveBudget(in.Max, in.MaxChars))
	for _, a := range out.Denials {
		d.evidence.add(a.EvidenceID, "apparmor", a.Profile, fmt.Sprintf("x%d %s denied %s on %s (comm %s)", a.Count, a.Profile, a.Operation, a.Name, a.Comm))
	}
	return textResult(fmt.Sprintf("%d distinct AppArmor denials (%d shown)", out.Total, len(out.Denials))), out, nil
}

// journalOptions builds collector options from the common tool inputs.
func journalOptions(unit string, minPriority, sinceSeconds int, kernel bool) collect.JournalOptions {
	o := collect.JournalOptions{Unit: unit, Kernel: kernel, ThisBoot: true}
	if minPriority > 0 && minPriority <= 7 {
		o.Priority = obs.Severity(minPriority)
	}
	if sinceSeconds > 0 {
		o.Since = time.Duration(sinceSeconds) * time.Second
	}
	return o
}
