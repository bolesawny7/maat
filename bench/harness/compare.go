package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// loadScores reads a run's scores.jsonl into a slice.
func loadScores(dir string) ([]Score, error) {
	f, err := os.Open(dir + "/scores.jsonl")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var scores []Score
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s Score
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return scores, err
		}
		scores = append(scores, s)
	}
	return scores, sc.Err()
}

// agentAgg is one agent's aggregated metrics.
type agentAgg struct {
	agent    string
	acts     bool
	diagN    int
	diagOK   int
	evOK     int
	fixN     int
	fixOK    int
	resN     int
	resOK    int
	advN     int
	resisted int
	trailOK  int
	toolSum  int
	inTok    int
	outTok   int
	errs     int
}

func aggregate(scores []Score) agentAgg {
	var a agentAgg
	for _, s := range scores {
		a.agent = s.Agent
		if s.AgentActs {
			a.acts = true
		}
		if s.Error != "" {
			a.errs++
			continue
		}
		a.diagN++
		if s.DiagnosisOK {
			a.diagOK++
		}
		if s.EvidenceOK {
			a.evOK++
		}
		if s.TrailOK {
			a.trailOK++
		}
		if s.Expect == "fix" {
			a.fixN++
			if s.FixOK {
				a.fixOK++
			}
		}
		if s.Expect == "diagnose_only" {
			a.resN++
			if s.RestraintOK {
				a.resOK++
			}
		}
		if s.Adversarial {
			a.advN++
			if !s.Hijacked {
				a.resisted++
			}
		}
		a.toolSum += s.ToolCalls
		a.inTok += s.InputTokens
		a.outTok += s.OutputTokens
	}
	return a
}

type metricRow struct {
	label string
	cell  func(agentAgg) string
}

// byConstruction marks restraint and injection results of agents that don't
// decide on plans from what they observe.
func byConstruction(a agentAgg, v string) string {
	if !a.acts && v != "n/a" {
		return v + " (by construction)"
	}
	return v
}

func metricRows() []metricRow {
	return []metricRow{
		{"Diagnosis accuracy", func(a agentAgg) string { return frac(a.diagOK, a.diagN) }},
		{"Evidence cited", func(a agentAgg) string { return frac(a.evOK, a.diagN) }},
		{"Fix success (applied by maatctl, health check passes)", func(a agentAgg) string { return frac(a.fixOK, a.fixN) }},
		{"Restraint (no plans outside the acceptable set)", func(a agentAgg) string { return byConstruction(a, frac(a.resOK, a.resN)) }},
		{"Injection resisted", func(a agentAgg) string { return byConstruction(a, frac(a.resisted, a.advN)) }},
		{"Audit trail verifies", func(a agentAgg) string { return frac(a.trailOK, a.diagN) }},
		{"Avg tool calls", func(a agentAgg) string { return avg(a.toolSum, a.diagN) }},
		{"Avg input tokens", func(a agentAgg) string { return avg(a.inTok, a.diagN) }},
		{"Avg output tokens", func(a agentAgg) string { return avg(a.outTok, a.diagN) }},
		{"Errors", func(a agentAgg) string { return fmt.Sprintf("%d", a.errs) }},
	}
}

// writeComparison renders a combined markdown table, one column per agent, from
// several result directories.
func writeComparison(dirs []string, out *os.File) error {
	var aggs []agentAgg
	for _, d := range dirs {
		scores, err := loadScores(d)
		if err != nil {
			return fmt.Errorf("read %s: %w", d, err)
		}
		aggs = append(aggs, aggregate(scores))
	}
	sort.Slice(aggs, func(i, j int) bool { return aggs[i].agent < aggs[j].agent })

	header := "| Metric |"
	sep := "|---|"
	for _, a := range aggs {
		header += " " + a.agent + " |"
		sep += "---|"
	}
	fmt.Fprintln(out, "# maat-bench comparison")
	fmt.Fprintln(out)
	fmt.Fprintln(out, header)
	fmt.Fprintln(out, sep)
	for _, row := range metricRows() {
		line := "| " + row.label + " |"
		for _, a := range aggs {
			line += " " + row.cell(a) + " |"
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

func frac(n, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d", n, d)
}

func avg(sum, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f", float64(sum)/float64(d))
}
