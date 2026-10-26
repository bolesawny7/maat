package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/collect"
	"github.com/bolesawny7/maat/internal/obs"
)

// ---- system_overview ----

type overviewIn struct{}

type overviewOut struct {
	EvidenceID  string           `json:"evidence_id"`
	Overview    collect.Overview `json:"overview"`
	FailedUnits []string         `json:"failed_units,omitempty"`
}

func (d *Deps) systemOverview(ctx context.Context, _ *mcp.CallToolRequest, _ overviewIn) (*mcp.CallToolResult, overviewOut, error) {
	cctx, cancel := ctxWithTimeout(ctx, 5*time.Second)
	defer cancel()

	var failedNames []string
	failed, err := collect.ListUnits(cctx, true)
	if err == nil {
		for _, u := range failed {
			failedNames = append(failedNames, u.Name)
		}
	}
	ov := collect.CollectOverview("/", len(failedNames))
	summary := fmt.Sprintf("%s, kernel %s, up %s, load %.2f, mem %.0f%% used, %d failed units",
		ov.OSRelease, ov.KernelRelease, ov.Uptime, ov.Load1, ov.MemUsedPct, ov.FailedUnits)
	out := overviewOut{EvidenceID: obs.EvidenceID("overview"), Overview: ov, FailedUnits: failedNames}
	d.evidence.add(out.EvidenceID, "overview", ov.Hostname, summary)
	return textResult(summary), out, nil
}

// ---- list_units ----

type listUnitsIn struct {
	OnlyFailed bool `json:"only_failed" jsonschema:"if true, return only failed units"`
	Max        int  `json:"max,omitempty" jsonschema:"cap on the number of units returned"`
	MaxChars   int  `json:"max_chars,omitempty"`
}

type listUnitsOut struct {
	Units     []collect.Unit `json:"units"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`
}

func (d *Deps) listUnits(ctx context.Context, _ *mcp.CallToolRequest, in listUnitsIn) (*mcp.CallToolResult, listUnitsOut, error) {
	cctx, cancel := ctxWithTimeout(ctx, 5*time.Second)
	defer cancel()
	units, err := collect.ListUnits(cctx, in.OnlyFailed)
	if err != nil {
		return errResult(err), listUnitsOut{}, nil
	}
	for i := range units {
		units[i].Description = d.redact(units[i].Description)
	}
	out := listUnitsOut{Total: len(units)}
	out.Units, out.Truncated = fit(units, d.effectiveBudget(in.Max, in.MaxChars))
	for _, u := range out.Units {
		d.evidence.add(u.EvidenceID, "unit", u.Name, fmt.Sprintf("%s: %s/%s (load %s)", u.Name, u.ActiveState, u.SubState, u.LoadState))
	}
	return textResult(fmt.Sprintf("%d units (%d shown)", out.Total, len(out.Units))), out, nil
}

// ---- unit_status ----

type unitStatusIn struct {
	Unit string `json:"unit" jsonschema:"systemd unit name, e.g. nginx.service"`
}

func (d *Deps) unitStatus(ctx context.Context, _ *mcp.CallToolRequest, in unitStatusIn) (*mcp.CallToolResult, collect.UnitStatus, error) {
	if in.Unit == "" {
		return errResult(fmt.Errorf("unit is required")), collect.UnitStatus{}, nil
	}
	cctx, cancel := ctxWithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := collect.GetUnitStatus(cctx, in.Unit)
	if err != nil {
		return errResult(err), collect.UnitStatus{}, nil
	}
	st.Description = d.redact(st.Description)
	summary := unitSummary(st)
	d.evidence.add(st.EvidenceID, "unit", st.Name, summary)
	return textResult(summary), st, nil
}

func unitSummary(st collect.UnitStatus) string {
	return fmt.Sprintf("%s: %s/%s (load %s), result=%s, exit status %d, restarts=%d",
		st.Name, st.ActiveState, st.SubState, st.LoadState, st.Result, st.ExecMainStatus, st.NRestarts)
}

// ---- top_processes ----

type topIn struct {
	Max      int `json:"max,omitempty"`
	SampleMs int `json:"sample_ms,omitempty" jsonschema:"CPU sampling interval in milliseconds; default 300, at most 5000"`
	MaxChars int `json:"max_chars,omitempty"`
}

type topOut struct {
	Processes []collect.Process `json:"processes"`
	Truncated bool              `json:"truncated,omitempty"`
}

func (d *Deps) topProcesses(ctx context.Context, _ *mcp.CallToolRequest, in topIn) (*mcp.CallToolResult, topOut, error) {
	sample := time.Duration(in.SampleMs) * time.Millisecond
	if sample <= 0 {
		sample = 300 * time.Millisecond
	}
	if sample > 5*time.Second {
		sample = 5 * time.Second
	}
	max := in.Max
	if max <= 0 {
		max = 15
	}
	procs, err := collect.TopProcesses("/", sample, max)
	if err != nil {
		return errResult(err), topOut{}, nil
	}
	for i := range procs {
		procs[i].Cmdline = d.redact(procs[i].Cmdline)
	}
	var out topOut
	out.Processes, out.Truncated = fit(procs, d.effectiveBudget(max, in.MaxChars))
	for _, p := range out.Processes {
		d.evidence.add(p.EvidenceID, "process", p.Comm, fmt.Sprintf("pid %d %s: %.1f%% CPU, %d MB RSS, %s", p.PID, p.Comm, p.CPUPct, p.RSSMB, p.Cmdline))
	}
	return textResult(fmt.Sprintf("top %d processes by CPU", len(out.Processes))), out, nil
}
