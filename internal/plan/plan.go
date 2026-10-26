// Package plan defines maat's typed, reviewable action plans and the closed
// catalog of actions that can be proposed. A plan is data, not a command: the
// model chooses a kind, its parameters, the evidence it relied on, and a
// rationale; maat fills in the preconditions, verification, and rollback from
// the catalog so the model cannot weaken the safety steps.
package plan

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bolesawny7/maat/internal/obs"
)

// SchemaVersion identifies the plan format. Bump it on any breaking change so
// old plan files are rejected rather than misread.
const SchemaVersion = "maat.plan/v0"

// Plan is a proposed change, pending human review.
type Plan struct {
	Schema        string        `json:"schema"`
	ID            string        `json:"id"`
	Kind          string        `json:"kind"`
	Params        Params        `json:"params"`
	Preconditions []Check       `json:"preconditions"`
	Verify        []Check       `json:"verify"`
	Rollback      Rollback      `json:"rollback"`
	Evidence      []string      `json:"evidence"`
	Rationale     obs.Untrusted `json:"rationale"`
	CreatedAt     time.Time     `json:"created_at"`
	Proposer      Proposer      `json:"proposer"`
}

// Params holds the parameters of an action. It is a string map so the MCP tool
// can accept it directly; each kind validates the specific keys it needs.
type Params map[string]string

// Proposer records who proposed the plan, for the audit trail.
type Proposer struct {
	Client string `json:"client,omitempty"`
	Model  string `json:"model,omitempty"`
}

// Check is a condition the actuator can evaluate against live system state,
// either before acting (a precondition) or after (verification).
type Check struct {
	Type          string `json:"type"`                     // e.g. "unit.active_state"
	Unit          string `json:"unit,omitempty"`           //
	Snap          string `json:"snap,omitempty"`           //
	Equals        string `json:"equals,omitempty"`         // expected value
	NotEquals     string `json:"not_equals,omitempty"`     //
	WithinSeconds int    `json:"within_seconds,omitempty"` // poll window for verification
}

// Rollback describes how to undo an action, or records that no undo is needed.
type Rollback struct {
	Kind   string `json:"kind"`             // "none", "snap.refresh", ...
	Reason string `json:"reason,omitempty"` // why none is needed, when Kind == "none"
	Params Params `json:"params,omitempty"`
}

// NewProposal builds a complete, validated plan from the parts a model supplies:
// the kind, its parameters, the cited evidence, and a rationale. maat derives
// the preconditions, verification, and rollback from the catalog. It returns an
// error if the kind is unknown or the parameters are invalid.
func NewProposal(kind string, params Params, evidence []string, rationale obs.Untrusted, proposer Proposer) (*Plan, error) {
	spec, ok := Catalog[kind]
	if !ok {
		return nil, fmt.Errorf("unknown action kind %q", kind)
	}
	if err := spec.validateParams(params); err != nil {
		return nil, err
	}
	pre, verify, rb := spec.Build(params)
	p := &Plan{
		Schema:        SchemaVersion,
		ID:            newUUIDv7(),
		Kind:          kind,
		Params:        params,
		Preconditions: pre,
		Verify:        verify,
		Rollback:      rb,
		Evidence:      evidence,
		Rationale:     rationale,
		CreatedAt:     time.Now().UTC(),
		Proposer:      proposer,
	}
	return p, nil
}

// Validate re-checks a plan from scratch. The actuator calls this on a plan read
// from disk, trusting nothing the server wrote: it confirms the schema, that the
// kind is still in the catalog, and that the parameters are valid.
func (p *Plan) Validate() error {
	if p.Schema != SchemaVersion {
		return fmt.Errorf("unsupported plan schema %q (want %q)", p.Schema, SchemaVersion)
	}
	if p.ID == "" {
		return fmt.Errorf("plan has no id")
	}
	spec, ok := Catalog[p.Kind]
	if !ok {
		return fmt.Errorf("unknown action kind %q", p.Kind)
	}
	return spec.validateParams(p.Params)
}

// Marshal renders the plan as indented JSON for storage.
func (p *Plan) Marshal() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// Unmarshal parses a plan from JSON without validating it; call Validate after.
func Unmarshal(data []byte) (*Plan, error) {
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
