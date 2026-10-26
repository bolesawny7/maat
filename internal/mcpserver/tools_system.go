package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/collect"
)

// ---- system_overview ----

type overviewIn struct{}

type overviewOut struct {
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
	out := overviewOut{Overview: ov, FailedUnits: failedNames}
	return textResult(fmt.Sprintf("%s, kernel %s, up %s, load %.2f, mem %.0f%% used, %d failed units",
		ov.OSRelease, ov.KernelRelease, ov.Uptime, ov.Load1, ov.MemUsedPct, ov.FailedUnits)), out, nil
}

// ---- list_units ----

type listUnitsIn struct {
	OnlyFailed bool `json:"only_failed" jsonschema:"if true, return only failed units"`
	Max        int  `json:"max,omitempty" jsonschema:"cap on the number of units returned"`
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
	out := listUnitsOut{Total: len(units), Units: units}
	max := in.Max
	if max <= 0 {
		max = d.effectiveBudget(0, 0).MaxItems
	}
	if len(out.Units) > max {
		out.Units = out.Units[:max]
		out.Truncated = true
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
	return textResult(fmt.Sprintf("%s: %s (%s), result=%s, restarts=%d", st.Name, st.ActiveState, st.SubState, st.Result, st.NRestarts)), st, nil
}

// ---- top_processes ----

type topIn struct {
	Max      int `json:"max,omitempty"`
	SampleMs int `json:"sample_ms,omitempty" jsonschema:"CPU sampling interval in milliseconds; default 300"`
}

type topOut struct {
	Processes []collect.Process `json:"processes"`
}

func (d *Deps) topProcesses(ctx context.Context, _ *mcp.CallToolRequest, in topIn) (*mcp.CallToolResult, topOut, error) {
	sample := time.Duration(in.SampleMs) * time.Millisecond
	if sample <= 0 {
		sample = 300 * time.Millisecond
	}
	max := in.Max
	if max <= 0 {
		max = 15
	}
	procs, err := collect.TopProcesses("/", sample, max)
	if err != nil {
		return errResult(err), topOut{}, nil
	}
	return textResult(fmt.Sprintf("top %d processes by CPU", len(procs))), topOut{Processes: procs}, nil
}
