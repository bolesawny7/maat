package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// invokeMCPTool calls one MCP tool for the model, records the call in the
// transcript, collects the evidence it returned, and captures a plan the server
// accepted from propose_action. It returns the text to feed back to the model.
func invokeMCPTool(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any, res *AgentResult) string {
	res.ToolCalls++
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		out := "error: " + err.Error()
		res.Transcript = append(res.Transcript, TranscriptStep{Role: "tool", Tool: name, Args: args, Content: out})
		return out
	}
	out := renderToolResult(r)
	res.Transcript = append(res.Transcript, TranscriptStep{Role: "tool", Tool: name, Args: args, Content: truncate(out, 4000)})
	if r.StructuredContent != nil {
		data, _ := json.Marshal(r.StructuredContent)
		collectEvidence(data, res.SeenEvidence)
	}
	if name == "propose_action" {
		kind, _ := args["kind"].(string)
		params := map[string]string{}
		if raw, ok := args["params"].(map[string]any); ok {
			for k, v := range raw {
				params[k] = fmt.Sprint(v)
			}
		}
		if p, ok := proposedFrom(r, kind, params); ok {
			res.ProposedPlans = append(res.ProposedPlans, p)
		} else {
			res.CatalogViolations++
		}
	}
	return out
}

// proposedFrom extracts the stored plan from a propose_action result. A result
// without a plan ID is a proposal the server rejected.
func proposedFrom(r *mcp.CallToolResult, kind string, params map[string]string) (ProposedPlan, bool) {
	if r.IsError || r.StructuredContent == nil {
		return ProposedPlan{}, false
	}
	data, _ := json.Marshal(r.StructuredContent)
	var out struct {
		PlanID string `json:"plan_id"`
		Kind   string `json:"kind"`
	}
	if json.Unmarshal(data, &out) != nil || out.PlanID == "" {
		return ProposedPlan{}, false
	}
	if out.Kind != "" {
		kind = out.Kind
	}
	return ProposedPlan{PlanID: out.PlanID, Kind: kind, Params: params}, true
}

func renderToolResult(r *mcp.CallToolResult) string {
	var text strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if r.IsError {
		return "error: " + text.String()
	}
	if r.StructuredContent != nil {
		data, _ := json.Marshal(r.StructuredContent)
		return string(data)
	}
	return text.String()
}

// collectEvidence walks arbitrary JSON and records, for every object that
// carries an evidence_id, the object's own JSON: the record the ID stands for.
// A record nested inside another (a log line inside a failed unit) also gets
// the enclosing object's unit or mount point, which it doesn't repeat itself.
// Scoring then checks a citation against everything that record said.
func collectEvidence(data []byte, seen map[string]string) {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return
	}
	var walk func(v any, context string)
	walk = func(v any, context string) {
		switch t := v.(type) {
		case map[string]any:
			for _, key := range []string{"unit", "mount_point"} {
				if s, ok := t[key].(string); ok && s != "" {
					context = key + "=" + s
				}
			}
			if id, ok := t["evidence_id"].(string); ok && id != "" {
				rec, _ := json.Marshal(t)
				seen[id] = string(rec)
				if context != "" {
					seen[id] += " " + context
				}
			}
			for _, sub := range t {
				walk(sub, context)
			}
		case []any:
			for _, sub := range t {
				walk(sub, context)
			}
		}
	}
	walk(root, "")
}
