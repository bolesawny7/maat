package main

import "context"

// Answer is the structured diagnosis an agent returns.
type Answer struct {
	RootCause   string   `json:"root_cause"`
	Component   string   `json:"component"`
	Evidence    []string `json:"evidence"`
	Plans       []Plan   `json:"plans"`
	Explanation string   `json:"explanation"`
}

// AgentResult is an answer plus the bookkeeping the scorer needs.
type AgentResult struct {
	Answer            Answer
	SeenEvidence      map[string]string // evidence ID -> template/example text seen in tool output
	Tokens            int
	ToolCalls         int
	CatalogViolations int
}

// Agent investigates a box and returns a diagnosis. Implementations include a
// deterministic baseline and an OpenAI-compatible LLM agent.
type Agent interface {
	Name() string
	Investigate(ctx context.Context, box *Box, s *Scenario) (AgentResult, error)
}
