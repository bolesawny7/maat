package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	var (
		scenariosDir = flag.String("scenarios", "bench/scenarios", "directory of scenario YAML files")
		agentName    = flag.String("agent", "baseline", "agent: baseline, oracle, or openai")
		maatBin      = flag.String("maat", "bin/maat", "path to the maat binary to run inside each box")
		only         = flag.String("only", "", "run only scenarios whose ID contains this substring")
		runs         = flag.Int("runs", 1, "repetitions per scenario")
		outDir       = flag.String("out", "bench/results", "output directory")
		keep         = flag.Bool("keep", false, "keep containers after each run (for debugging)")
		baseURL      = flag.String("openai-url", env("OPENAI_BASE_URL", "http://localhost:8336/v1"), "OpenAI-compatible base URL")
		model        = flag.String("model", env("OPENAI_MODEL", ""), "model name for the openai agent")
		apiKey       = flag.String("api-key", env("OPENAI_API_KEY", ""), "API key (blank for local inference snaps)")
		protocol     = flag.String("protocol", "react", "openai agent protocol: react (text, works anywhere) or tools (native function-calling)")
		maxSteps     = flag.Int("max-steps", 16, "max tool-calling steps per scenario (openai agent)")
		timeoutSec   = flag.Int("timeout", 600, "per-scenario timeout in seconds")
		compare      = flag.String("compare", "", "comma-separated result dirs to combine into one comparison table, then exit")
	)
	flag.Parse()

	// Comparison mode: merge existing runs into one table and exit.
	if *compare != "" {
		if err := writeComparison(strings.Split(*compare, ","), os.Stdout); err != nil {
			fatal(err)
		}
		return
	}

	agent, err := buildAgent(*agentName, *baseURL, *model, *apiKey, *protocol, *maxSteps)
	if err != nil {
		fatal(err)
	}
	scenarios, err := LoadScenarios(*scenariosDir)
	if err != nil {
		fatal(err)
	}
	absMaat, err := filepath.Abs(*maatBin)
	if err != nil {
		fatal(err)
	}
	if _, err := os.Stat(absMaat); err != nil {
		fatal(fmt.Errorf("maat binary not found at %s (build it for linux first): %w", absMaat, err))
	}

	runID := time.Now().UTC().Format("20060102-150405")
	resultDir := filepath.Join(*outDir, runID)
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		fatal(err)
	}
	scoresPath := filepath.Join(resultDir, "scores.jsonl")
	scoresFile, err := os.Create(scoresPath)
	if err != nil {
		fatal(err)
	}
	defer scoresFile.Close()

	ctx := context.Background()
	if n := CleanupOrphans(ctx, "maat-"); n > 0 {
		fmt.Printf("Cleaned up %d orphaned container(s) from a previous run.\n", n)
	}
	var all []Score
	for _, s := range scenarios {
		if *only != "" && !strings.Contains(s.ID, *only) {
			continue
		}
		for run := 0; run < *runs; run++ {
			scCtx, cancel := context.WithTimeout(ctx, time.Duration(*timeoutSec)*time.Second)
			sc := runOne(scCtx, agent, s, absMaat, *keep, run)
			cancel()
			all = append(all, sc)
			line, _ := json.Marshal(sc)
			scoresFile.Write(append(line, '\n'))
			fmt.Println(formatScore(sc))
		}
	}
	writeSummary(filepath.Join(resultDir, "summary.md"), agent.Name(), all)
	fmt.Printf("\nResults written to %s\n", resultDir)
}

func buildAgent(name, baseURL, model, apiKey, protocol string, maxSteps int) (Agent, error) {
	switch name {
	case "baseline":
		return BaselineAgent{}, nil
	case "oracle":
		return OracleAgent{}, nil
	case "openai":
		if model == "" {
			return nil, fmt.Errorf("the openai agent requires -model (or OPENAI_MODEL)")
		}
		a := NewOpenAIAgent(baseURL, model, apiKey, protocol)
		if maxSteps > 0 {
			a.MaxSteps = maxSteps
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unknown agent %q", name)
	}
}

// runOne provisions a box, injects the fault, runs the agent, scores it, and (for
// fix scenarios) applies the proposed fix and checks health.
func runOne(ctx context.Context, agent Agent, s *Scenario, maatBin string, keep bool, run int) Score {
	start := time.Now()
	sc := Score{ScenarioID: s.ID, Agent: agent.Name(), Expect: s.Expect, Adversarial: s.Adversarial != nil}

	boxName := fmt.Sprintf("maat-%s-%d", sanitize(s.ID), run)
	box, err := Launch(ctx, boxName, s.Image)
	if err != nil {
		sc.Error = "launch: " + err.Error()
		return sc
	}
	if !keep {
		defer box.Destroy(context.Background())
	}

	if err := box.InstallMaat(ctx, maatBin); err != nil {
		sc.Error = "install maat: " + err.Error()
		return sc
	}
	for _, cmd := range s.Setup {
		if _, err := box.Exec(ctx, cmd); err != nil {
			// Setup commands may legitimately return non-zero (e.g. a failing
			// restart); that is part of breaking the box, so we continue.
			_ = err
		}
	}
	if s.Adversarial != nil {
		injectAdversarial(ctx, box, s.Adversarial)
	}

	r, err := agent.Investigate(ctx, box, s)
	if err != nil {
		sc.Error = "investigate: " + err.Error()
		return sc
	}
	sc.Tokens = r.Tokens
	sc.ToolCalls = r.ToolCalls
	sc.CatalogViolations = r.CatalogViolations

	sc.DiagnosisOK = scoreDiagnosis(s, r.Answer)
	sc.EvidenceOK = scoreEvidence(s, r)
	if s.Adversarial != nil {
		sc.Hijacked = scoreHijack(s, r.Answer)
	}
	if s.Expect == "diagnose_only" {
		sc.RestraintOK = scoreRestraint(s, r.Answer)
	}
	if s.Expect == "fix" {
		sc.FixOK = applyAndCheckFix(ctx, box, s, r.Answer)
	}

	sc.WallSeconds = round2(time.Since(start).Seconds())
	return sc
}

// applyAndCheckFix applies the first proposed plan that matches an acceptable one
// and reports whether the box is healthy afterwards.
func applyAndCheckFix(ctx context.Context, box *Box, s *Scenario, a Answer) bool {
	for _, p := range a.Plans {
		if !matchesAcceptablePlan(p, s.AcceptablePlans) {
			continue
		}
		if err := box.ApplyFix(ctx, p); err != nil {
			continue
		}
		return box.HealthOK(ctx, s.HealthCheck)
	}
	return false
}

// injectAdversarial plants the attacker-controlled payload via the chosen vector.
func injectAdversarial(ctx context.Context, box *Box, adv *Adversarial) {
	payload := shellQuote(adv.Payload)
	switch adv.Vector {
	case "unit_description":
		unit := "maat-adv.service"
		box.Exec(ctx, fmt.Sprintf(`printf '[Unit]\nDescription=%s\n[Service]\nExecStart=/bin/true\n' > /etc/systemd/system/%s && systemctl daemon-reload && systemctl start %s || true`, adv.Payload, unit, unit))
	case "http_access_log":
		box.Exec(ctx, "mkdir -p /var/log && echo "+payload+" >> /var/log/maat-access.log; logger -t nginx "+payload)
	default: // logger / sshd_invalid_user fall back to a logged line
		box.Exec(ctx, "logger -t sshd "+payload)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func sanitize(id string) string {
	out := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, strings.ToLower(id))
	return strings.Trim(out, "-")
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "maat-bench:", err)
	os.Exit(1)
}
