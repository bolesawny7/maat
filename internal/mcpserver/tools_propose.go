package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/obs"
	"github.com/bolesawny7/maat/internal/plan"
	"github.com/bolesawny7/maat/internal/trail"
)

type proposeIn struct {
	Kind      string            `json:"kind" jsonschema:"action kind: service.restart, service.reset-failed or snap.revert"`
	Params    map[string]string `json:"params" jsonschema:"action parameters, e.g. {\"unit\": \"nginx.service\"} or {\"snap\": \"lxd\"}"`
	Evidence  []string          `json:"evidence,omitempty" jsonschema:"evidence_id values from earlier tool results that justify this action"`
	Rationale string            `json:"rationale" jsonschema:"why this action addresses the diagnosed problem"`
}

type proposeOut struct {
	PlanID             string   `json:"plan_id"`
	Kind               string   `json:"kind"`
	DangerLevel        string   `json:"danger_level"`
	Preview            string   `json:"preview"`
	UnresolvedEvidence []string `json:"unresolved_evidence,omitempty"`
	Note               string   `json:"note"`
}

func (d *Deps) proposeAction(ctx context.Context, _ *mcp.CallToolRequest, in proposeIn) (*mcp.CallToolResult, proposeOut, error) {
	p, err := plan.NewProposal(in.Kind, plan.Params(in.Params), in.Evidence, obs.Untrusted(in.Rationale), d.Proposer)
	if err != nil {
		return errResult(err), proposeOut{}, nil
	}
	found, missing := d.evidence.resolve(p.Evidence)
	if err := p.AttachEvidence(found); err != nil {
		return errResult(err), proposeOut{}, nil
	}
	if d.Plans != nil {
		digest, err := d.Plans.Save(p)
		if err != nil {
			return errResult(fmt.Errorf("save plan: %w", err)), proposeOut{}, nil
		}
		// maatctl only applies a plan whose bytes match its proposal record, so a
		// plan the trail can't record is withdrawn rather than left behind.
		if err := d.recordProposal(p, digest); err != nil {
			os.Remove(filepath.Join(d.Plans.Dir, p.ID+".json"))
			return errResult(fmt.Errorf("could not record the proposal in the audit trail, so no plan was stored: %w", err)), proposeOut{}, nil
		}
	}

	spec := plan.Catalog[p.Kind]
	out := proposeOut{
		PlanID:             p.ID,
		Kind:               p.Kind,
		DangerLevel:        spec.DangerLevel,
		Preview:            previewText(p, spec),
		UnresolvedEvidence: missing,
		Note:               "No change has been made. A human must run `maatctl show " + p.ID + "` and `maatctl apply " + p.ID + "` to execute this, authorizing it through polkit.",
	}
	text := "Recorded plan " + p.ID + " for human review. The system has not been changed."
	if len(missing) > 0 {
		text += fmt.Sprintf(" %d cited evidence ID(s) were not returned by any tool in this session; the reviewer will see them flagged.", len(missing))
	}
	return textResult(text), out, nil
}

func (d *Deps) recordProposal(p *plan.Plan, digest string) error {
	if d.Trail == nil {
		return nil
	}
	_, err := d.Trail.AppendAndCheckpoint(trail.Record{
		Type:    trail.TypeProposal,
		Actor:   "maat-mcp",
		Plan:    p.ID,
		Digest:  digest,
		Summary: fmt.Sprintf("proposed %s %v", p.Kind, p.Params),
	})
	return err
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
