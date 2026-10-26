package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/collect"
	"github.com/bolesawny7/maat/internal/obs"
)

// recent_failures is a composite tool: in one call it returns every failed unit
// with its status and a short, redacted log tail. It exists because weak or
// cost-sensitive models do much better with one dense, relevant call than with
// several round-trips, and it keeps the whole picture within a tight token
// budget — the same context-engineering idea as the condense pipeline.

type recentFailuresIn struct {
	Max          int `json:"max,omitempty" jsonschema:"cap on failed units returned; default 10"`
	LinesPerUnit int `json:"lines_per_unit,omitempty" jsonschema:"redacted log lines per unit; default 5, at most 50"`
	MaxChars     int `json:"max_chars,omitempty"`
}

type logLine struct {
	EvidenceID string        `json:"evidence_id"`
	Message    obs.Untrusted `json:"message"`
}

type failedUnit struct {
	EvidenceID     string        `json:"evidence_id"`
	Unit           string        `json:"unit"`
	LoadState      string        `json:"load_state,omitempty"`
	ActiveState    string        `json:"active_state"`
	SubState       string        `json:"sub_state"`
	Result         string        `json:"result,omitempty"`
	ExecMainStatus int           `json:"exec_main_status"`
	NRestarts      uint32        `json:"n_restarts"`
	Description    obs.Untrusted `json:"description,omitempty"`
	RecentLog      []logLine     `json:"recent_log,omitempty"`
}

type recentFailuresOut struct {
	Failed    []failedUnit `json:"failed"`
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated,omitempty"`
}

func (d *Deps) recentFailures(ctx context.Context, _ *mcp.CallToolRequest, in recentFailuresIn) (*mcp.CallToolResult, recentFailuresOut, error) {
	cctx, cancel := ctxWithTimeout(ctx, 20*time.Second)
	defer cancel()

	units, err := collect.ListUnits(cctx, true)
	if err != nil {
		return errResult(err), recentFailuresOut{}, nil
	}
	max := in.Max
	if max <= 0 {
		max = 10
	}
	lines := in.LinesPerUnit
	if lines <= 0 {
		lines = 5
	}
	if lines > 50 {
		lines = 50
	}

	var all []failedUnit
	for i, u := range units {
		if i >= max {
			break
		}
		fu := failedUnit{EvidenceID: u.EvidenceID, Unit: u.Name, LoadState: u.LoadState, ActiveState: u.ActiveState, SubState: u.SubState, Description: u.Description}
		if st, err := collect.GetUnitStatus(cctx, u.Name); err == nil {
			fu.LoadState = st.LoadState
			fu.ActiveState = st.ActiveState
			fu.SubState = st.SubState
			fu.Result = st.Result
			fu.ExecMainStatus = st.ExecMainStatus
			fu.NRestarts = st.NRestarts
			if st.Description != "" {
				fu.Description = st.Description
			}
		}
		fu.Description = d.redact(fu.Description)
		d.evidence.add(fu.EvidenceID, "unit", fu.Unit, fmt.Sprintf("%s: %s/%s (load %s), result=%s, exit status %d, restarts=%d",
			fu.Unit, fu.ActiveState, fu.SubState, fu.LoadState, fu.Result, fu.ExecMainStatus, fu.NRestarts))

		// A short, redacted tail of the unit's own log, with evidence IDs so the
		// model can cite what it saw.
		recs, err := collect.ReadJournal(cctx, collect.JournalOptions{Unit: u.Name, Lines: lines, ThisBoot: true})
		if err == nil {
			obs.SortRecordsByTime(recs)
			for _, r := range recs {
				msg := d.redact(obs.Untrusted(r.Message))
				line := logLine{EvidenceID: obs.EvidenceID("journal", u.Name, string(msg)), Message: msg}
				fu.RecentLog = append(fu.RecentLog, line)
				d.evidence.add(line.EvidenceID, "journal", u.Name, string(msg))
			}
		}
		all = append(all, fu)
	}
	out := recentFailuresOut{Total: len(units)}
	out.Failed, out.Truncated = fit(all, d.effectiveBudget(max, in.MaxChars))
	if len(all) < len(units) {
		out.Truncated = true
	}
	return textResult(fmt.Sprintf("%d failed unit(s)", out.Total)), out, nil
}
