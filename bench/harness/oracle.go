package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// OracleAgent is not a measured agent: it answers with the scenario's own truth.
// Its purpose is to validate the benchmark itself. It reads the same tools an
// agent would, so a resolvable evidence ID proves the scenario leaves the
// required evidence; for fix scenarios it proposes the acceptable plan through
// propose_action, so the real server stores it and the real maatctl applies it,
// which proves the fix, the health check and the audit trail work end to end.
type OracleAgent struct{}

func (OracleAgent) Name() string                   { return "oracle-reference" }
func (OracleAgent) ProposesFromObservations() bool { return false }

func (OracleAgent) Investigate(ctx context.Context, box *Box, s *Scenario) (AgentResult, error) {
	cs, err := box.MCPClient(ctx)
	if err != nil {
		return AgentResult{}, err
	}
	defer cs.Close()

	res := AgentResult{SeenEvidence: map[string]string{}}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		res.ToolCalls++
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r == nil {
			return nil
		}
		res.Transcript = append(res.Transcript, TranscriptStep{Role: "tool", Tool: name, Args: args, Content: truncate(renderToolResult(r), 4000)})
		if r.StructuredContent != nil {
			data, _ := json.Marshal(r.StructuredContent)
			collectEvidence(data, res.SeenEvidence)
		}
		return r
	}

	call("recent_failures", nil)
	call("disk_usage", nil)
	if strings.HasSuffix(s.Truth.Component, ".service") {
		call("journal_summary", map[string]any{"unit": s.Truth.Component, "since_seconds": 86400})
	}

	res.Answer = Answer{RootCause: s.Truth.RootCause, Component: s.Truth.Component, Explanation: "oracle reference answer"}
	res.Answer.Evidence = pickEvidence(s, res.SeenEvidence)
	if s.Expect == "fix" && len(s.AcceptablePlans) > 0 {
		want := s.AcceptablePlans[0]
		args := map[string]any{"kind": want.Kind, "params": want.Params, "evidence": res.Answer.Evidence, "rationale": "oracle reference fix"}
		if r := call("propose_action", args); r != nil {
			if p, ok := proposedFrom(r, want.Kind, want.Params); ok {
				res.ProposedPlans = append(res.ProposedPlans, p)
			} else {
				res.CatalogViolations++
			}
		}
	}
	return res, nil
}

// pickEvidence returns one seen evidence ID whose record supports the truth, in
// a deterministic order, or nothing.
func pickEvidence(s *Scenario, seen map[string]string) []string {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		probe := AgentResult{SeenEvidence: seen, Answer: Answer{Evidence: []string{id}}}
		if scoreEvidence(s, probe) {
			return []string{id}
		}
	}
	return nil
}
