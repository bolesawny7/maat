package mcpserver

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/obs"
	"github.com/bolesawny7/maat/internal/plan"
	"github.com/bolesawny7/maat/internal/trail"
)

type proposeIn struct {
	Kind      string            `json:"kind" jsonschema:"action kind, one of the catalog kinds (e.g. service.restart)"`
	Params    map[string]string `json:"params" jsonschema:"action parameters, e.g. a unit name"`
	Evidence  []string          `json:"evidence,omitempty" jsonschema:"evidence IDs from earlier tool results that justify this action"`
	Rationale string            `json:"rationale" jsonschema:"why this action addresses the diagnosed problem"`
}

type proposeOut struct {
	PlanID      string `json:"plan_id"`
	Kind        string `json:"kind"`
	DangerLevel string `json:"danger_level"`
	Preview     string `json:"preview"`
	Note        string `json:"note"`
}

func (d *Deps) proposeAction(ctx context.Context, _ *mcp.CallToolRequest, in proposeIn) (*mcp.CallToolResult, proposeOut, error) {
	p, err := plan.NewProposal(in.Kind, plan.Params(in.Params), in.Evidence, obs.Untrusted(in.Rationale), d.Proposer)
	if err != nil {
		return errResult(err), proposeOut{}, nil
	}
	if d.Plans != nil {
		if err := d.Plans.Save(p); err != nil {
			return errResult(fmt.Errorf("save plan: %w", err)), proposeOut{}, nil
		}
	}
	d.recordProposal(p)

	spec := plan.Catalog[p.Kind]
	out := proposeOut{
		PlanID:      p.ID,
		Kind:        p.Kind,
		DangerLevel: spec.DangerLevel,
		Preview:     previewText(p, spec),
		Note:        "No change has been made. A human must run `maatctl show " + p.ID + "` and `maatctl apply " + p.ID + "` to execute this, authorizing it through polkit.",
	}
	return textResult("Recorded plan " + p.ID + " for human review. The system has not been changed."), out, nil
}

func (d *Deps) recordProposal(p *plan.Plan) {
	if d.Trail == nil {
		return
	}
	data, _ := p.Marshal()
	_, _ = d.Trail.AppendAndCheckpoint(trail.Record{
		Type:    trail.TypeProposal,
		Actor:   "maat-mcp",
		Plan:    p.ID,
		Digest:  trail.DigestOf(data),
		Summary: fmt.Sprintf("proposed %s %v", p.Kind, redactParams(p.Params)),
	})
}

func previewText(p *plan.Plan, spec plan.Spec) string {
	s := fmt.Sprintf("Action: %s\nSummary: %s\nParameters: %v\n", p.Kind, spec.Summary, p.Params)
	if len(p.Preconditions) > 0 {
		s += fmt.Sprintf("Preconditions: %d check(s)\n", len(p.Preconditions))
	}
	if len(p.Verify) > 0 {
		s += fmt.Sprintf("Verification: %d check(s)\n", len(p.Verify))
	}
	s += "Rollback: " + p.Rollback.Kind
	if p.Rollback.Reason != "" {
		s += " (" + p.Rollback.Reason + ")"
	}
	return s
}

// redactParams returns params sorted and with sensitive-looking values masked,
// so a parameter such as a secret never reaches the trail summary.
func redactParams(p plan.Params) map[string]string {
	out := map[string]string{}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = p[k]
	}
	return out
}
