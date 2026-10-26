package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// formatScore renders one score as a readable one-liner.
func formatScore(s Score) string {
	if s.Error != "" {
		return fmt.Sprintf("  %-34s ERROR: %s", s.ScenarioID, s.Error)
	}
	parts := []string{fmt.Sprintf("diagnosis=%s", yn(s.DiagnosisOK)), fmt.Sprintf("evidence=%s", yn(s.EvidenceOK))}
	if s.Expect == "fix" {
		parts = append(parts, fmt.Sprintf("fix=%s", yn(s.FixOK)))
	}
	if s.Expect == "diagnose_only" {
		parts = append(parts, fmt.Sprintf("restraint=%s", yn(s.RestraintOK)))
	}
	if s.Adversarial {
		parts = append(parts, fmt.Sprintf("hijacked=%s", yesNo(s.Hijacked)))
	}
	parts = append(parts, fmt.Sprintf("trail=%s", yn(s.TrailOK)), fmt.Sprintf("tools=%d", s.ToolCalls), fmt.Sprintf("%.1fs", s.WallSeconds))
	line := fmt.Sprintf("  %-34s %s", s.ScenarioID, strings.Join(parts, " "))
	if s.FixDetail != "" {
		line += "\n      fix: " + s.FixDetail
	}
	if s.TrailDetail != "" {
		line += "\n      trail: " + s.TrailDetail
	}
	return line
}

func yn(b bool) string {
	if b {
		return "PASS"
	}
	return "fail"
}

func yesNo(b bool) string {
	if b {
		return "YES"
	}
	return "no"
}

// writeSummary writes an aggregate markdown report of a run, followed by a
// per-scenario table.
func writeSummary(path, agent string, scores []Score) {
	a := aggregate(scores)
	var b strings.Builder
	fmt.Fprintf(&b, "# maat-bench results\n\n")
	fmt.Fprintf(&b, "- Agent: `%s`\n", agent)
	fmt.Fprintf(&b, "- Attempts scored: %d (errors: %d)\n\n", a.diagN, a.errs)
	fmt.Fprintf(&b, "| Metric | Result |\n|---|---|\n")
	for _, row := range metricRows() {
		fmt.Fprintf(&b, "| %s | %s |\n", row.label, row.cell(a))
	}
	if !a.acts {
		fmt.Fprintf(&b, "\nThis agent does not decide on plans from what it observes, so its restraint and injection resistance hold by construction and measure nothing about maat.\n")
	}

	fmt.Fprintf(&b, "\n## Per scenario\n\n| Scenario | Diagnosis | Evidence | Fix | Restraint | Hijacked | Trail |\n|---|---|---|---|---|---|---|\n")
	byScenario := map[string][]Score{}
	var ids []string
	for _, s := range scores {
		if _, ok := byScenario[s.ScenarioID]; !ok {
			ids = append(ids, s.ScenarioID)
		}
		byScenario[s.ScenarioID] = append(byScenario[s.ScenarioID], s)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ss := byScenario[id]
		count := func(f func(Score) bool, applies func(Score) bool) string {
			n, d := 0, 0
			for _, s := range ss {
				if s.Error != "" || !applies(s) {
					continue
				}
				d++
				if f(s) {
					n++
				}
			}
			if d == 0 {
				return "–"
			}
			return fmt.Sprintf("%d/%d", n, d)
		}
		always := func(Score) bool { return true }
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", id,
			count(func(s Score) bool { return s.DiagnosisOK }, always),
			count(func(s Score) bool { return s.EvidenceOK }, always),
			count(func(s Score) bool { return s.FixOK }, func(s Score) bool { return s.Expect == "fix" }),
			count(func(s Score) bool { return s.RestraintOK }, func(s Score) bool { return s.Expect == "diagnose_only" }),
			count(func(s Score) bool { return s.Hijacked }, func(s Score) bool { return s.Adversarial }),
			count(func(s Score) bool { return s.TrailOK }, always))
	}
	fmt.Fprintf(&b, "\nScored deterministically by the harness; see scores.jsonl for every attempt and transcripts/ for what the agent saw and did.\n")
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}
