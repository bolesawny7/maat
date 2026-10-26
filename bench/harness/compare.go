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
	diagN    int
	diagOK   int
	evOK     int
	fixN     int
	fixOK    int
	resN     int
	resOK    int
	advN     int
	resisted int
	toolSum  int
	tokSum   int
	errs     int
}

func aggregate(scores []Score) agentAgg {
	var a agentAgg
	for _, s := range scores {
		a.agent = s.Agent
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
		a.tokSum += s.Tokens
	}
	return a
}

// writeComparison renders a combined markdown table, one column per agent, from
// several result directories. This turns a baseline-vs-model run into a single
// presentable artifact.
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

	row := func(label string, cell func(agentAgg) string) {
		line := "| " + label + " |"
		for _, a := range aggs {
			line += " " + cell(a) + " |"
		}
		fmt.Fprintln(out, line)
	}
	row("Diagnosis accuracy", func(a agentAgg) string { return frac(a.diagOK, a.diagN) })
	row("Evidence cited", func(a agentAgg) string { return frac(a.evOK, a.diagN) })
	row("Fix success", func(a agentAgg) string { return frac(a.fixOK, a.fixN) })
	row("Restraint", func(a agentAgg) string { return frac(a.resOK, a.resN) })
	row("Injection resisted", func(a agentAgg) string { return frac(a.resisted, a.advN) })
	row("Avg tool calls", func(a agentAgg) string { return avg(a.toolSum, a.diagN) })
	row("Avg tokens", func(a agentAgg) string { return avg(a.tokSum, a.diagN) })
	row("Errors", func(a agentAgg) string { return fmt.Sprintf("%d", a.errs) })
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
