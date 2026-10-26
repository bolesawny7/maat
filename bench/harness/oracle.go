package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// OracleAgent is not a measured agent: it returns the scenario's own truth and
// acceptable fix. Its purpose is to validate the benchmark itself — that each
// scenario's evidence substring really appears in the journal, that fix
// scenarios recover after the acceptable plan, and that the scoring and health
// machinery work end-to-end. A near-perfect oracle score means the harness is
// trustworthy; it says nothing about any model.
type OracleAgent struct{}

func (OracleAgent) Name() string { return "oracle-reference" }

// bSummary mirrors the journal_summary tool output the oracle inspects.
type bSummary struct {
	Templates []struct {
		EvidenceID string `json:"evidence_id"`
		Template   string `json:"template"`
		Example    string `json:"example"`
	} `json:"templates"`
}

func (OracleAgent) Investigate(ctx context.Context, box *Box, s *Scenario) (AgentResult, error) {
	cs, err := box.MCPClient(ctx)
	if err != nil {
		return AgentResult{}, err
	}
	defer cs.Close()

	res := AgentResult{SeenEvidence: map[string]string{}}

	// Pull journal evidence for the component so the oracle can cite a real,
	// resolvable evidence ID (this is what proves the evidence path works).
	if s.Truth.Component != "" && strings.Contains(s.Truth.Component, ".") {
		var sum bSummary
		res.ToolCalls++
		if r, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name:      "journal_summary",
			Arguments: map[string]any{"unit": s.Truth.Component, "since_seconds": 86400},
		}); err == nil && r.StructuredContent != nil {
			data, _ := json.Marshal(r.StructuredContent)
			_ = json.Unmarshal(data, &sum)
			for _, t := range sum.Templates {
				res.SeenEvidence[t.EvidenceID] = t.Template + " " + t.Example
			}
		}
	}

	ans := Answer{
		RootCause:   s.Truth.RootCause,
		Component:   s.Truth.Component,
		Explanation: "oracle reference answer",
	}
	// Cite an evidence ID whose text matches a required substring, if any.
	ans.Evidence = pickEvidence(s, res.SeenEvidence)
	// For fix scenarios, propose the first acceptable plan.
	if s.Expect == "fix" && len(s.AcceptablePlans) > 0 {
		ans.Plans = []Plan{s.AcceptablePlans[0]}
	}
	res.Answer = ans
	return res, nil
}

// pickEvidence returns one seen evidence ID supporting the truth, or any seen ID.
func pickEvidence(s *Scenario, seen map[string]string) []string {
	if len(s.Truth.Evidence) > 0 {
		for id, text := range seen {
			lower := strings.ToLower(text)
			for _, ev := range s.Truth.Evidence {
				if ev.Contains != "" && strings.Contains(lower, strings.ToLower(ev.Contains)) {
					return []string{id}
				}
			}
		}
	}
	for id := range seen {
		return []string{id}
	}
	return nil
}
