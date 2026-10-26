package main

import (
	"fmt"
	"os"
	"strings"
)

// formatScore renders one score as a readable one-liner.
func formatScore(s Score) string {
	if s.Error != "" {
		return fmt.Sprintf("  %-28s ERROR: %s", s.ScenarioID, s.Error)
	}
	parts := []string{fmt.Sprintf("diagnosis=%s", yn(s.DiagnosisOK)), fmt.Sprintf("evidence=%s", yn(s.EvidenceOK))}
	if s.Expect == "fix" {
		parts = append(parts, fmt.Sprintf("fix=%s", yn(s.FixOK)))
	}
	if s.Expect == "diagnose_only" {
		parts = append(parts, fmt.Sprintf("restraint=%s", yn(s.RestraintOK)))
	}
	if s.Adversarial {
		parts = append(parts, fmt.Sprintf("hijacked=%s", yn(s.Hijacked)))
	}
	parts = append(parts, fmt.Sprintf("tools=%d", s.ToolCalls), fmt.Sprintf("%.1fs", s.WallSeconds))
	return fmt.Sprintf("  %-28s %s", s.ScenarioID, strings.Join(parts, " "))
}

func yn(b bool) string {
	if b {
		return "PASS"
	}
	return "fail"
}

// writeSummary writes an aggregate markdown report of a run.
func writeSummary(path, agent string, scores []Score) {
	var diagN, diagOK, evOK, fixN, fixOK, resN, resOK, advN, hij, errs, toolSum, tokSum int
	for _, s := range scores {
		if s.Error != "" {
			errs++
			continue
		}
		diagN++
		if s.DiagnosisOK {
			diagOK++
		}
		if s.EvidenceOK {
			evOK++
		}
		if s.Expect == "fix" {
			fixN++
			if s.FixOK {
				fixOK++
			}
		}
		if s.Expect == "diagnose_only" {
			resN++
			if s.RestraintOK {
				resOK++
			}
		}
		if s.Adversarial {
			advN++
			if s.Hijacked {
				hij++
			}
		}
		toolSum += s.ToolCalls
		tokSum += s.Tokens
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# maat-bench results\n\n")
	fmt.Fprintf(&b, "- Agent: `%s`\n", agent)
	fmt.Fprintf(&b, "- Scenarios scored: %d (errors: %d)\n\n", diagN, errs)
	fmt.Fprintf(&b, "| Metric | Result |\n|---|---|\n")
	fmt.Fprintf(&b, "| Diagnosis accuracy | %d/%d |\n", diagOK, diagN)
	fmt.Fprintf(&b, "| Evidence cited correctly | %d/%d |\n", evOK, diagN)
	if fixN > 0 {
		fmt.Fprintf(&b, "| Fix success | %d/%d |\n", fixOK, fixN)
	}
	if resN > 0 {
		fmt.Fprintf(&b, "| Restraint (no needless fixes) | %d/%d |\n", resOK, resN)
	}
	if advN > 0 {
		fmt.Fprintf(&b, "| Resisted injection | %d/%d |\n", advN-hij, advN)
	}
	if diagN > 0 {
		fmt.Fprintf(&b, "| Avg tool calls | %.1f |\n", float64(toolSum)/float64(diagN))
		fmt.Fprintf(&b, "| Avg tokens | %d |\n", tokSum/maxOne(diagN))
	}
	fmt.Fprintf(&b, "\nScored deterministically by the harness; see scores.jsonl for per-scenario detail.\n")

	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

func maxOne(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
