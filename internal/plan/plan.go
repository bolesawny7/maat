// Package plan defines maat's typed, reviewable action plans and the closed
// catalog of actions that can be proposed. A plan is data, not a command: the
// model chooses a kind, its parameters, the evidence it relied on, and a
// rationale; maat fills in the preconditions, verification, and rollback from
// the catalog so the model cannot weaken the safety steps.
package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/bolesawny7/maat/internal/obs"
)

// SchemaVersion identifies the plan format. Bump it on any breaking change so
// old plan files are rejected rather than misread.
const SchemaVersion = "maat.plan/v0"

// Limits on the model-supplied parts of a plan. A plan is shown to a person, so
// unbounded text is both a denial of service and room for social engineering.
const (
	MaxRationaleBytes = 4000
	MaxEvidence       = 32
)

var (
	uuidRe       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	evidenceIDRe = regexp.MustCompile(`^ev_[0-9a-f]{6,16}$`)
)

// Plan is a proposed change, pending human review.
type Plan struct {
	Schema        string   `json:"schema"`
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Params        Params   `json:"params"`
	Preconditions []Check  `json:"preconditions"`
	Verify        []Check  `json:"verify"`
	Rollback      Rollback `json:"rollback"`
	Evidence      []string `json:"evidence"`
	// EvidenceDetail is the server's record of what each cited ID referred to.
	// IDs in Evidence without an entry here were not issued by the server
	// session that recorded the plan.
	EvidenceDetail []obs.EvidenceRef `json:"evidence_detail,omitempty"`
	Rationale      obs.Untrusted     `json:"rationale"`
	CreatedAt      time.Time         `json:"created_at"`
	Proposer       Proposer          `json:"proposer"`
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
	if err := validateModelText(evidence, rationale); err != nil {
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
// from disk, trusting nothing the server wrote: it confirms the schema, the ID
// format, that the kind is still in the catalog, that the parameters and the
// model-supplied text are valid, and that the preconditions, verification and
// rollback are exactly what the catalog derives for these parameters. A plan
// whose safety steps were edited on disk is rejected rather than executed.
func (p *Plan) Validate() error {
	if p.Schema != SchemaVersion {
		return fmt.Errorf("unsupported plan schema %q (want %q)", p.Schema, SchemaVersion)
	}
	if !uuidRe.MatchString(p.ID) {
		return fmt.Errorf("plan id %q is not a UUID", p.ID)
	}
	spec, ok := Catalog[p.Kind]
	if !ok {
		return fmt.Errorf("unknown action kind %q", p.Kind)
	}
	if err := spec.validateParams(p.Params); err != nil {
		return err
	}
	if err := validateModelText(p.Evidence, p.Rationale); err != nil {
		return err
	}
	if len(p.Proposer.Client) > 128 || len(p.Proposer.Model) > 128 {
		return fmt.Errorf("proposer fields are too long")
	}
	if err := validateEvidenceDetail(p.Evidence, p.EvidenceDetail); err != nil {
		return err
	}
	pre, verify, rb := spec.Build(p.Params)
	if !sameJSON(p.Preconditions, pre) || !sameJSON(p.Verify, verify) || !sameJSON(p.Rollback, rb) {
		return fmt.Errorf("the plan's preconditions, verification or rollback differ from what the catalog derives for %s; the plan file was altered after it was proposed", p.Kind)
	}
	return nil
}

func validateModelText(evidence []string, rationale obs.Untrusted) error {
	if len(evidence) > MaxEvidence {
		return fmt.Errorf("too many evidence IDs (%d, at most %d)", len(evidence), MaxEvidence)
	}
	for _, id := range evidence {
		if !evidenceIDRe.MatchString(id) {
			return fmt.Errorf("evidence %q is not an evidence ID; cite the ev_... values returned by maat's tools", truncateForError(id))
		}
	}
	if len(rationale) > MaxRationaleBytes {
		return fmt.Errorf("rationale is too long (%d bytes, at most %d)", len(rationale), MaxRationaleBytes)
	}
	return nil
}

// MaxExcerptBytes bounds each evidence excerpt stored in a plan.
const MaxExcerptBytes = 1024

// AttachEvidence records what the cited evidence IDs referred to. Only refs for
// IDs the plan actually cites are kept.
func (p *Plan) AttachEvidence(refs []obs.EvidenceRef) error {
	cited := map[string]bool{}
	for _, id := range p.Evidence {
		cited[id] = true
	}
	var keep []obs.EvidenceRef
	for _, r := range refs {
		if cited[r.ID] {
			keep = append(keep, r)
			delete(cited, r.ID)
		}
	}
	if err := validateEvidenceDetail(p.Evidence, keep); err != nil {
		return err
	}
	p.EvidenceDetail = keep
	return nil
}

func validateEvidenceDetail(evidence []string, detail []obs.EvidenceRef) error {
	cited := map[string]bool{}
	for _, id := range evidence {
		cited[id] = true
	}
	if len(detail) > len(cited) {
		return fmt.Errorf("more evidence details than cited evidence")
	}
	for _, d := range detail {
		if !cited[d.ID] {
			return fmt.Errorf("evidence detail for %q, which the plan does not cite", truncateForError(d.ID))
		}
		if len(d.Source) > 64 || len(d.Subject) > 256 || len(d.Excerpt) > MaxExcerptBytes {
			return fmt.Errorf("evidence detail for %s is too long", d.ID)
		}
	}
	return nil
}

// sameJSON compares two values by their JSON encoding, which is how plans are
// stored, so nil and empty optional fields compare equal.
func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func truncateForError(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
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
