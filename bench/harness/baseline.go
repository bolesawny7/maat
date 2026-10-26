package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BaselineAgent is a deterministic, LLM-free agent. It calls a small, fixed set
// of maat tools and applies keyword rules to reach a diagnosis. It is the
// benchmark's baseline: it shows how far maat's condensed, structured output
// gets you with no model at all, and it lets the whole harness run in CI without
// an endpoint. Its behaviour is deterministic so scores are reproducible.
type BaselineAgent struct{}

func (BaselineAgent) Name() string { return "baseline-heuristic" }

// mirror types for the tool outputs the baseline inspects.
type bDisks struct {
	Filesystems []struct {
		MountPoint    string  `json:"mount_point"`
		UsedPct       float64 `json:"used_pct"`
		InodesUsedPct float64 `json:"inodes_used_pct"`
	} `json:"filesystems"`
}

type bApparmor struct {
	Denials []struct {
		Profile   string `json:"profile"`
		Operation string `json:"operation"`
	} `json:"denials"`
}

type bFailures struct {
	Failed []struct {
		Unit        string   `json:"unit"`
		Result      string   `json:"result"`
		RecentLog   []string `json:"recent_log"`
		LogEvidence []string `json:"log_evidence"`
	} `json:"failed"`
}

func (BaselineAgent) Investigate(ctx context.Context, box *Box, s *Scenario) (AgentResult, error) {
	cs, err := box.MCPClient(ctx)
	if err != nil {
		return AgentResult{}, err
	}
	defer cs.Close()

	res := AgentResult{SeenEvidence: map[string]string{}}
	call := func(name string, args map[string]any, out any) {
		res.ToolCalls++
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r == nil || r.StructuredContent == nil {
			return
		}
		data, _ := json.Marshal(r.StructuredContent)
		_ = json.Unmarshal(data, out)
	}

	var disks bDisks
	call("disk_usage", nil, &disks)

	var aa bApparmor
	call("apparmor_denials", nil, &aa)

	// One dense call for failed units, their result, and a redacted log tail with
	// evidence IDs — the tool built for exactly this.
	var fails bFailures
	call("recent_failures", map[string]any{"lines_per_unit": 8}, &fails)
	for _, f := range fails.Failed {
		for i, id := range f.LogEvidence {
			if i < len(f.RecentLog) {
				res.SeenEvidence[id] = f.RecentLog[i]
			}
		}
	}

	res.Answer = baselineDiagnose(disks, aa, fails)
	return res, nil
}

// baselineDiagnose applies ordered rules to reach a diagnosis. The rules are
// intentionally simple; the baseline is a floor, not a ceiling.
func baselineDiagnose(disks bDisks, aa bApparmor, fails bFailures) Answer {
	// 1. Inode/space pressure (inodes first: a full-inode disk often still shows
	// free space).
	for _, fs := range disks.Filesystems {
		if fs.InodesUsedPct >= 95 {
			return Answer{RootCause: "resource.inodes_exhausted", Component: fs.MountPoint, Explanation: "inodes exhausted on " + fs.MountPoint}
		}
	}
	for _, fs := range disks.Filesystems {
		if fs.UsedPct >= 95 {
			return Answer{RootCause: "resource.disk_full", Component: fs.MountPoint, Explanation: "filesystem full: " + fs.MountPoint}
		}
	}

	// 2. AppArmor denial.
	if len(aa.Denials) > 0 {
		return Answer{RootCause: "security.apparmor_denial", Component: aa.Denials[0].Profile, Explanation: "AppArmor denied " + aa.Denials[0].Operation}
	}

	// 3. A failed unit: classify from its log tail and cite that evidence.
	if len(fails.Failed) > 0 {
		f := fails.Failed[0]
		text := strings.ToLower(strings.Join(f.RecentLog, "\n"))
		cite := firstEvidence(f.LogEvidence, f.RecentLog, "203/exec", "exit-code", "failed")
		switch {
		case strings.Contains(text, "address already in use"):
			return Answer{RootCause: "network.port_conflict", Component: f.Unit,
				Evidence: firstEvidence(f.LogEvidence, f.RecentLog, "address already in use"), Explanation: "port conflict for " + f.Unit}
		case strings.Contains(text, "permission denied"):
			return Answer{RootCause: "permissions.wrong_owner", Component: f.Unit,
				Evidence: firstEvidence(f.LogEvidence, f.RecentLog, "permission denied"), Explanation: "permission problem for " + f.Unit}
		case strings.Contains(text, "out of memory") || strings.Contains(text, "oom-kill"):
			return Answer{RootCause: "resource.memory_oom", Component: f.Unit,
				Evidence: firstEvidence(f.LogEvidence, f.RecentLog, "memory", "oom"), Explanation: "out of memory: " + f.Unit}
		default:
			return Answer{RootCause: "service.bad_exec", Component: f.Unit, Evidence: cite, Explanation: f.Unit + " is failing"}
		}
	}

	// 4. Nothing wrong detected.
	return Answer{RootCause: "healthy", Component: "", Explanation: "no failed units, no disk pressure, no denials"}
}

// firstEvidence returns the first evidence ID whose paired log line contains any
// of the substrings (in priority order). Deterministic: ids and their lines are
// taken in the order recent_failures returned them.
func firstEvidence(ids, lines []string, substrs ...string) []string {
	for _, substr := range substrs {
		substr = strings.ToLower(substr)
		for i, id := range ids {
			if i < len(lines) && strings.Contains(strings.ToLower(lines[i]), substr) {
				return []string{id}
			}
		}
	}
	return nil
}
