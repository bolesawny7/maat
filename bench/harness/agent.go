package main

import "context"

// Answer is the structured diagnosis an agent returns.
type Answer struct {
	RootCause   string   `json:"root_cause"`
	Component   string   `json:"component"`
	Evidence    []string `json:"evidence"`
	Plans       []Plan   `json:"plans"` // fixes described in the answer; never applied
	Explanation string   `json:"explanation"`
}

// ProposedPlan is a plan the agent recorded through propose_action and the
// server accepted. Only these can be applied by maatctl.
type ProposedPlan struct {
	PlanID string            `json:"plan_id"`
	Kind   string            `json:"kind"`
	Params map[string]string `json:"params"`
}

func (p ProposedPlan) asPlan() Plan { return Plan{Kind: p.Kind, Params: p.Params} }

// TranscriptStep is one turn of an investigation, kept so anyone can check a
// score. Tool output is already redacted by maat.
type TranscriptStep struct {
	Role    string         `json:"role"` // "model", "tool", "harness"
	Tool    string         `json:"tool,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Content string         `json:"content"`
}

// AgentResult is an answer plus the bookkeeping the scorer needs.
type AgentResult struct {
	Answer            Answer
	ProposedPlans     []ProposedPlan
	SeenEvidence      map[string]string // evidence ID -> JSON of the record that carried it
	InputTokens       int
	OutputTokens      int
	ToolCalls         int
	CatalogViolations int
	Transcript        []TranscriptStep
}

// allPlans is every fix the agent put forward, proposed or merely described.
func (r AgentResult) allPlans() []Plan {
	out := append([]Plan(nil), r.Answer.Plans...)
	for _, p := range r.ProposedPlans {
		out = append(out, p.asPlan())
	}
	return out
}

// Agent investigates a box and returns a diagnosis. Implementations include a
// deterministic baseline and an OpenAI-compatible LLM agent.
type Agent interface {
	Name() string
	// ProposesFromObservations is false for agents whose plans don't depend on
	// what they observe (the baseline never proposes; the oracle proposes the
	// scenario's own answer). Their restraint and injection resistance hold by
	// construction and measure nothing, and the reports say so.
	ProposesFromObservations() bool
	Investigate(ctx context.Context, box *Box, s *Scenario) (AgentResult, error)
}
