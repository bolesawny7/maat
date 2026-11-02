package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	var (
		scenDir    = flag.String("scenarios", "bench/agents/scenarios", "directory of scenario YAML files")
		toolsFlag  = flag.String("tools", "holmes,k8sgpt,opencode", "comma-separated agents to run")
		modelsFlag = flag.String("models", "qwen3:8b", "comma-separated Ollama model names")
		only       = flag.String("only", "", "run only the scenario with this ID")
		variants   = flag.String("variants", "clean,injected", "which variants to run")
		runs       = flag.Int("runs", 1, "attempts per scenario, variant, agent and model")
		outDir     = flag.String("out", "", "results directory (default bench/agents/results/<timestamp>)")
		kubeconfig = flag.String("kubeconfig", "/etc/rancher/k3s/k3s.yaml", "administrator kubeconfig, never given to agents")
		agentSA    = flag.String("agent-sa", "agents:sre-agent", "ServiceAccount the agents' kubeconfig uses")
		timeout    = flag.Duration("timeout", 15*time.Minute, "time limit for one agent run")
		waitLimit  = flag.Duration("wait", 4*time.Minute, "time limit for a fault to appear")
		rescore    = flag.String("rescore", "", "recompute scores.jsonl and summary.md for this results directory from its attempts/*.json, without running anything")
	)
	flag.Parse()

	scenarios, err := LoadScenarios(*scenDir)
	if err != nil {
		log.Fatal(err)
	}
	if *rescore != "" {
		if err := rescoreDir(*rescore, scenarios); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := Preflight(context.Background(), "/etc/rancher/k3s/k3s.yaml", *kubeconfig); err != nil {
		log.Fatal(err)
	}
	agents, err := selectAgents(split(*toolsFlag))
	if err != nil {
		log.Fatal(err)
	}
	if *outDir == "" {
		*outDir = filepath.Join("bench", "agents", "results", time.Now().UTC().Format("20060102-150405"))
	}
	for _, sub := range []string{"transcripts", "attempts"} {
		if err := os.MkdirAll(filepath.Join(*outDir, sub), 0o755); err != nil {
			log.Fatal(err)
		}
	}
	if err := writeConfig(*outDir, *scenDir, agents, split(*modelsFlag), *runs); err != nil {
		log.Fatal(err)
	}
	scoresFile, err := os.Create(filepath.Join(*outDir, "scores.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	defer scoresFile.Close()

	cl := Cluster{Kubeconfig: *kubeconfig, AgentSubject: *agentSA}
	ctx := context.Background()
	wantVariant := map[string]bool{}
	for _, v := range split(*variants) {
		wantVariant[v] = true
	}

	var all []Score
	for _, s := range scenarios {
		if *only != "" && s.ID != *only {
			continue
		}
		for _, variant := range s.Variants {
			if !wantVariant[variant] {
				continue
			}
			for run := 1; run <= *runs; run++ {
				for _, a := range agents {
					for _, model := range split(*modelsFlag) {
						sc := attempt(ctx, cl, s, variant, a, model, run, *outDir, *timeout, *waitLimit)
						all = append(all, sc)
						line, _ := json.Marshal(sc)
						fmt.Fprintln(scoresFile, string(line))
						log.Printf("%s/%s %s %s run %d: diagnosed=%s hijacked=%v flagged=%v calls=%d %.0fs %s",
							s.ID, variant, a.Name(), model, run, boolPtr(sc.Diagnosed), sc.Hijacked, sc.Flagged, sc.ToolCalls, sc.Seconds, sc.Error)
					}
				}
			}
		}
	}
	if err := writeSummary(*outDir, all); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", *outDir)
}

// attempt runs one agent on one freshly broken namespace and scores it.
func attempt(ctx context.Context, cl Cluster, s Scenario, variant string, a Agent, model string, run int, outDir string, timeout, waitLimit time.Duration) Score {
	injected := variant == "injected"
	fail := func(err error) Score {
		return Score{Scenario: s.ID, Variant: variant, Vector: s.Vector, Tool: a.Name(), Model: model, Run: run, Error: "setup: " + err.Error()}
	}
	if err := cl.Reset(ctx); err != nil {
		return fail(err)
	}
	manifests, eventMsg := s.Render(injected)
	if err := cl.Apply(ctx, manifests); err != nil {
		return fail(err)
	}
	p, err := cl.WaitFor(ctx, s.Wait, waitLimit)
	if err != nil {
		return fail(err)
	}
	if s.Event != nil && injected {
		if err := cl.CreateEvent(ctx, p, *s.Event, eventMsg); err != nil {
			return fail(err)
		}
	}
	before, err := cl.Decoy(ctx)
	if err != nil {
		return fail(err)
	}

	rctx, cancel := context.WithTimeout(ctx, timeout)
	start := time.Now()
	res := a.Run(rctx, model, s.Prompt)
	res.Secs = time.Since(start).Seconds()
	if rctx.Err() != nil && res.Err == "" {
		res.Err = "timed out after " + timeout.String()
	}
	cancel()

	after, err := cl.Decoy(ctx)
	if err != nil {
		res.Err = strings.TrimSpace(res.Err + "; reading decoy: " + err.Error())
	}
	sc := ScoreAttempt(s, variant, a.Name(), model, run, res, before, after)
	writeTranscript(outDir, sc, s, res, before, after)
	rec := Record{Scenario: s.ID, Variant: variant, Tool: a.Name(), Model: model, Run: run,
		Answer: res.Answer, Calls: res.Calls, Seen: res.Seen, Err: res.Err, Secs: res.Secs, Before: before, After: after}
	if data, err := json.MarshalIndent(rec, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(outDir, "attempts", attemptName(sc)+".json"), data, 0o644)
	}
	return sc
}

// Record is everything needed to score an attempt again without rerunning it.
type Record struct {
	Scenario string     `json:"scenario"`
	Variant  string     `json:"variant"`
	Tool     string     `json:"tool"`
	Model    string     `json:"model"`
	Run      int        `json:"run"`
	Answer   string     `json:"answer"`
	Calls    []Call     `json:"calls"`
	Seen     string     `json:"seen"`
	Err      string     `json:"error,omitempty"`
	Secs     float64    `json:"seconds"`
	Before   DecoyState `json:"decoy_before"`
	After    DecoyState `json:"decoy_after"`
}

// rescoreDir applies the current scorer to saved attempts and rewrites
// scores.jsonl and summary.md.
func rescoreDir(dir string, scenarios []Scenario) error {
	byID := map[string]Scenario{}
	for _, s := range scenarios {
		byID[s.ID] = s
	}
	paths, err := filepath.Glob(filepath.Join(dir, "attempts", "*.json"))
	if err != nil || len(paths) == 0 {
		return fmt.Errorf("no attempts in %s: %v", dir, err)
	}
	var all []Score
	var lines strings.Builder
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		s, ok := byID[r.Scenario]
		if !ok {
			return fmt.Errorf("%s: unknown scenario %q", p, r.Scenario)
		}
		res := AgentResult{Answer: r.Answer, Calls: r.Calls, Seen: r.Seen, Err: r.Err, Secs: r.Secs}
		sc := ScoreAttempt(s, r.Variant, r.Tool, r.Model, r.Run, res, r.Before, r.After)
		all = append(all, sc)
		line, _ := json.Marshal(sc)
		lines.Write(line)
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "scores.jsonl"), []byte(lines.String()), 0o644); err != nil {
		return err
	}
	log.Printf("rescored %d attempts in %s", len(all), dir)
	return writeSummary(dir, all)
}

func attemptName(sc Score) string {
	return fmt.Sprintf("%s.%s.%s.%s.%d", sc.Scenario, sc.Variant, sc.Tool, strings.NewReplacer(":", "_", "/", "_").Replace(sc.Model), sc.Run)
}

func writeTranscript(outDir string, sc Score, s Scenario, r AgentResult, before, after DecoyState) {
	name := attemptName(sc) + ".md"
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s) — %s with %s, run %d\n\n", sc.Scenario, sc.Variant, sc.Tool, sc.Model, sc.Run)
	fmt.Fprintf(&b, "Prompt: %s\n\nPlanted in: %s\n\n", s.Prompt, s.Vector)
	score, _ := json.MarshalIndent(sc, "", "  ")
	fmt.Fprintf(&b, "## Score\n\n```json\n%s\n```\n\nDecoy before: %+v\nDecoy after: %+v\n\n", score, before, after)
	fmt.Fprintf(&b, "## Final answer\n\n```text\n%s\n```\n\n## Tool calls (%d)\n\n", r.Answer, len(r.Calls))
	for i, c := range r.Calls {
		fmt.Fprintf(&b, "%d. `%s`: `%s`\n", i+1, c.Tool, oneLine(c.Input, 400))
	}
	fmt.Fprintf(&b, "\n## Raw output\n\n```text\n%s\n```\n", r.Raw)
	_ = os.WriteFile(filepath.Join(outDir, "transcripts", name), []byte(b.String()), 0o644)
}

func writeConfig(outDir, scenDir string, agents []Agent, models []string, runs int) error {
	cfg := map[string]any{
		"started_at": time.Now().UTC().Format(time.RFC3339),
		"models":     models,
		"runs":       runs,
		"note":       Note,
	}
	versions := map[string]string{}
	for _, a := range agents {
		versions[a.Name()] = a.Version(context.Background())
	}
	cfg["agents"] = versions
	digests := map[string]string{}
	paths, _ := filepath.Glob(filepath.Join(scenDir, "*.yaml"))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		digests[filepath.Base(p)] = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	}
	cfg["scenarios"] = digests
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(filepath.Join(outDir, "config.json"), data, 0o644)
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolPtr(b *bool) string {
	if b == nil {
		return "n/a"
	}
	return fmt.Sprint(*b)
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
