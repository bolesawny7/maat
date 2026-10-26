package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BaselineAgent is a deterministic, LLM-free agent. It calls a small, fixed set
// of maat tools and applies keyword rules to reach a diagnosis. It is the
// benchmark's floor: it shows how far maat's condensed, structured output gets
// you with no model at all, and it lets the whole harness run without an
// endpoint. It never proposes a fix, so its restraint and injection resistance
// hold by construction and are reported as such.
type BaselineAgent struct{}

func (BaselineAgent) Name() string                   { return "baseline-heuristic" }
func (BaselineAgent) ProposesFromObservations() bool { return false }

// mirror types for the tool outputs the baseline inspects.
type bDisks struct {
	Filesystems []struct {
		EvidenceID    string  `json:"evidence_id"`
		MountPoint    string  `json:"mount_point"`
		ReadOnly      bool    `json:"read_only"`
		UsedPct       float64 `json:"used_pct"`
		InodesUsedPct float64 `json:"inodes_used_pct"`
	} `json:"filesystems"`
}

type bApparmor struct {
	Denials []struct {
		EvidenceID string `json:"evidence_id"`
		Profile    string `json:"profile"`
		Operation  string `json:"operation"`
	} `json:"denials"`
}

type bLogLine struct {
	EvidenceID string `json:"evidence_id"`
	Message    string `json:"message"`
}

type bFailures struct {
	Failed []struct {
		EvidenceID string     `json:"evidence_id"`
		Unit       string     `json:"unit"`
		LoadState  string     `json:"load_state"`
		Result     string     `json:"result"`
		RecentLog  []bLogLine `json:"recent_log"`
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
		if err != nil || r == nil {
			return
		}
		res.Transcript = append(res.Transcript, TranscriptStep{Role: "tool", Tool: name, Args: args, Content: truncate(renderToolResult(r), 4000)})
		if r.StructuredContent == nil {
			return
		}
		data, _ := json.Marshal(r.StructuredContent)
		collectEvidence(data, res.SeenEvidence)
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

	res.Answer = baselineDiagnose(disks, aa, fails)
	return res, nil
}

// baselineDiagnose applies ordered rules to reach a diagnosis. The rules are
// intentionally simple; the baseline is a floor, not a ceiling.
func baselineDiagnose(disks bDisks, aa bApparmor, fails bFailures) Answer {
	// 1. Inode/space pressure on a writable filesystem (inodes first: a
	// full-inode disk often still shows free space).
	for _, fs := range disks.Filesystems {
		if !fs.ReadOnly && fs.InodesUsedPct >= 95 {
			return Answer{RootCause: "resource.inodes_exhausted", Component: fs.MountPoint, Evidence: []string{fs.EvidenceID}, Explanation: "inodes exhausted on " + fs.MountPoint}
		}
	}
	for _, fs := range disks.Filesystems {
		if !fs.ReadOnly && fs.UsedPct >= 95 {
			return Answer{RootCause: "resource.disk_full", Component: fs.MountPoint, Evidence: []string{fs.EvidenceID}, Explanation: "filesystem full: " + fs.MountPoint}
		}
	}

	// 2. AppArmor denial.
	if len(aa.Denials) > 0 {
		d := aa.Denials[0]
		return Answer{RootCause: "security.apparmor_denial", Component: d.Profile, Evidence: []string{d.EvidenceID}, Explanation: "AppArmor denied " + d.Operation}
	}

	// 3. A failed unit: classify from its state and log tail, citing that evidence.
	if len(fails.Failed) > 0 {
		f := fails.Failed[0]
		var text strings.Builder
		for _, l := range f.RecentLog {
			text.WriteString(strings.ToLower(l.Message))
			text.WriteByte('\n')
		}
		log := text.String()
		cite := func(substrs ...string) []string { return firstEvidence(f.RecentLog, substrs...) }
		switch {
		case f.LoadState == "not-found":
			return Answer{RootCause: "service.stale_failure", Component: f.Unit, Evidence: []string{f.EvidenceID}, Explanation: f.Unit + " is failed but its unit file is gone"}
		case strings.Contains(log, "address already in use"):
			return Answer{RootCause: "network.port_conflict", Component: f.Unit, Evidence: cite("address already in use"), Explanation: "port conflict for " + f.Unit}
		case strings.Contains(log, "permission denied"):
			return Answer{RootCause: "permissions.wrong_owner", Component: f.Unit, Evidence: cite("permission denied"), Explanation: "permission problem for " + f.Unit}
		case strings.Contains(log, "out of memory") || strings.Contains(log, "oom-kill"):
			return Answer{RootCause: "resource.memory_oom", Component: f.Unit, Evidence: cite("memory", "oom"), Explanation: "out of memory: " + f.Unit}
		default:
			return Answer{RootCause: "service.bad_exec", Component: f.Unit, Evidence: cite("203/exec", "exit-code", "failed"), Explanation: f.Unit + " is failing"}
		}
	}

	// 4. Nothing wrong detected.
	return Answer{RootCause: "healthy", Explanation: "no failed units, no disk pressure, no denials"}
}

// firstEvidence returns the evidence ID of the first log line that contains any
// of the substrings, trying them in priority order.
func firstEvidence(lines []bLogLine, substrs ...string) []string {
	for _, substr := range substrs {
		substr = strings.ToLower(substr)
		for _, l := range lines {
			if strings.Contains(strings.ToLower(l.Message), substr) {
				return []string{l.EvidenceID}
			}
		}
	}
	return nil
}
