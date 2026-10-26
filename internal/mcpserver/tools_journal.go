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
	MinPriority  int    `json:"min_priority,omitempty" jsonschema:"most-verbose syslog priority to include (0 emerg to 7 debug); default 7"`
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
	return textResult(fmt.Sprintf("%d entries across %d templates (%d shown), %d redactions", sum.TotalRecords, sum.TotalTemplates, sum.Shown, sum.Redactions)), sum, nil
}

// ---- journal_entries ----

type journalEntriesIn struct {
	Unit         string `json:"unit,omitempty"`
	MinPriority  int    `json:"min_priority,omitempty" jsonschema:"most-verbose syslog priority to include (0 emerg to 7 debug); default 7"`
	SinceSeconds int    `json:"since_seconds,omitempty"`
	Lines        int    `json:"lines,omitempty" jsonschema:"max entries to read before budgeting"`
	MaxChars     int    `json:"max_chars,omitempty"`
}

type entry struct {
	Time     time.Time     `json:"time"`
	Unit     string        `json:"unit,omitempty"`
	Priority string        `json:"priority"`
	Message  obs.Untrusted `json:"message"`
}

type journalEntriesOut struct {
	Entries   []entry `json:"entries"`
	Total     int     `json:"total"`
	Truncated bool    `json:"truncated"`
}

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
	budget := d.effectiveBudget(0, in.MaxChars)
	out := journalEntriesOut{Total: len(recs)}
	chars := 0
	for _, r := range recs {
		msg, _ := d.Redactor.Redact(r.Message)
		e := entry{Time: r.Time, Unit: r.Unit, Priority: r.Priority.String(), Message: obs.Untrusted(msg)}
		cost := len(msg) + 40
		if chars+cost > budget.MaxChars && len(out.Entries) > 0 {
			out.Truncated = true
			break
		}
		chars += cost
		out.Entries = append(out.Entries, e)
	}
	return textResult(fmt.Sprintf("%d entries (%d shown)", out.Total, len(out.Entries))), out, nil
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
	return textResult(fmt.Sprintf("%d kernel messages across %d templates", sum.TotalRecords, sum.TotalTemplates)), sum, nil
}

// ---- apparmor_denials ----

type apparmorIn struct {
	SinceSeconds int `json:"since_seconds,omitempty"`
	Max          int `json:"max,omitempty"`
}

type apparmorOut struct {
	Denials []collect.AppArmorDenial `json:"denials"`
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
	max := in.Max
	if max <= 0 {
		max = d.effectiveBudget(0, 0).MaxItems
	}
	if len(denials) > max {
		denials = denials[:max]
	}
	return textResult(fmt.Sprintf("%d distinct AppArmor denials", len(denials))), apparmorOut{Denials: denials}, nil
}

// journalOptions builds collector options from the common tool inputs.
func journalOptions(unit string, minPriority, sinceSeconds int, kernel bool) collect.JournalOptions {
	o := collect.JournalOptions{Unit: unit, Kernel: kernel, ThisBoot: true}
	if minPriority > 0 {
		o.Priority = obs.Severity(minPriority)
	}
	if sinceSeconds > 0 {
		o.Since = time.Duration(sinceSeconds) * time.Second
	}
	return o
}
