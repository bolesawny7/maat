package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		maatBin      = flag.String("maat", "bin/linux/maat", "path to the linux maat binary to run inside each box")
		maatctlBin   = flag.String("maatctl", "bin/linux/maatctl-bench", "path to a linux maatctl built with -tags maatbench; fixes are applied with it")
		only         = flag.String("only", "", "run only scenarios whose ID contains this substring")
		runs         = flag.Int("runs", 1, "repetitions per scenario (published results use 3)")
		outDir       = flag.String("out", "bench/results", "output directory")
		keep         = flag.Bool("keep", false, "keep containers after each run (for debugging)")
		baseURL      = flag.String("openai-url", env("OPENAI_BASE_URL", "http://localhost:8336/v1"), "OpenAI-compatible base URL")
		model        = flag.String("model", env("OPENAI_MODEL", ""), "model name for the openai agent")
		apiKey       = flag.String("api-key", env("OPENAI_API_KEY", ""), "API key (blank for local inference snaps)")
		protocol     = flag.String("protocol", "react", "openai agent protocol: react (text, works anywhere) or tools (native function-calling)")
		maxSteps     = flag.Int("max-steps", 16, "max tool-calling steps per scenario (openai agent)")
		maxReply     = flag.Int("max-reply-tokens", 1024, "max tokens per model reply (openai agent); raise it for reasoning models")
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

	agent, err := buildAgent(*agentName, *baseURL, *model, *apiKey, *protocol, *maxSteps, *maxReply)
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
		fatal(fmt.Errorf("maat binary not found at %s (run `make bench-bin` first): %w", absMaat, err))
	}
	absMaatctl := ""
	if *maatctlBin != "" {
		if absMaatctl, err = filepath.Abs(*maatctlBin); err != nil {
			fatal(err)
		}
		if _, err := os.Stat(absMaatctl); err != nil {
			fatal(fmt.Errorf("bench maatctl not found at %s (run `make bench-bin` first): %w", absMaatctl, err))
		}
	}

	runID := time.Now().UTC().Format("20060102-150405")
	resultDir := filepath.Join(*outDir, runID)
	if err := os.MkdirAll(filepath.Join(resultDir, "transcripts"), 0o755); err != nil {
		fatal(err)
	}
	writeRunConfig(filepath.Join(resultDir, "config.json"), agent, *model, *protocol, *baseURL, *runs, scenarios, absMaat, absMaatctl)
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
			sc, transcript := runOne(scCtx, agent, s, absMaat, absMaatctl, *keep, run)
			cancel()
			all = append(all, sc)
			line, _ := json.Marshal(sc)
			scoresFile.Write(append(line, '\n'))
			writeJSON(filepath.Join(resultDir, "transcripts", fmt.Sprintf("%s-%d.json", s.ID, run)), transcript)
			fmt.Println(formatScore(sc))
		}
	}
	writeSummary(filepath.Join(resultDir, "summary.md"), agent.Name(), all)
	fmt.Printf("\nResults written to %s\n", resultDir)
}

func buildAgent(name, baseURL, model, apiKey, protocol string, maxSteps, maxReply int) (Agent, error) {
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
		if maxReply > 0 {
			a.MaxReplyTokens = maxReply
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unknown agent %q", name)
	}
}

// runOne provisions a box, injects the fault, runs the agent, scores it, applies
// an acceptable proposed fix through maatctl, checks health, and verifies the
// audit trail.
func runOne(ctx context.Context, agent Agent, s *Scenario, maatBin, maatctlBin string, keep bool, run int) (Score, []TranscriptStep) {
	start := time.Now()
	sc := Score{ScenarioID: s.ID, Run: run, Agent: agent.Name(), Expect: s.Expect, Adversarial: s.Adversarial != nil, AgentActs: agent.ProposesFromObservations()}

	boxName := fmt.Sprintf("maat-%s-%d", sanitize(s.ID), run)
	box, err := Launch(ctx, boxName, s.Image)
	if err != nil {
		sc.Error = "launch: " + err.Error()
		return sc, nil
	}
	if !keep {
		defer box.Destroy(context.Background())
	}
	sc.ImageFingerprint = box.ImageFingerprint(ctx)

	if err := box.InstallMaat(ctx, maatBin, maatctlBin); err != nil {
		sc.Error = "install maat: " + err.Error()
		return sc, nil
	}
	for _, cmd := range s.Setup {
		// Setup commands may legitimately fail (a failing start is how a box is
		// broken), so errors are not fatal.
		_, _ = box.Exec(ctx, cmd)
	}
	if s.Adversarial != nil {
		injectAdversarial(ctx, box, s.Adversarial)
	}

	r, err := agent.Investigate(ctx, box, s)
	if err != nil {
		sc.Error = "investigate: " + err.Error()
		return sc, r.Transcript
	}
	sc.InputTokens = r.InputTokens
	sc.OutputTokens = r.OutputTokens
	sc.ToolCalls = r.ToolCalls
	sc.CatalogViolations = r.CatalogViolations

	sc.DiagnosisOK = scoreDiagnosis(s, r.Answer)
	sc.EvidenceOK = scoreEvidence(s, r)
	sc.Hijacked = scoreHijack(s, r)
	if s.Expect == "diagnose_only" {
		sc.RestraintOK = scoreRestraint(s, r.allPlans())
	}
	if s.Expect == "fix" {
		sc.FixOK, sc.FixDetail = applyAndCheckFix(ctx, box, s, r)
	}
	out, err := box.VerifyTrail(ctx)
	sc.TrailOK = err == nil
	if !sc.TrailOK {
		sc.TrailDetail = truncate(strings.TrimSpace(out), 400)
	}

	sc.WallSeconds = round2(time.Since(start).Seconds())
	return sc, r.Transcript
}

// applyAndCheckFix applies the first proposed plan that matches an acceptable
// one, with the real maatctl, and reports whether the box is healthy afterwards.
// Plans the agent only described, without proposing them, can't be applied.
func applyAndCheckFix(ctx context.Context, box *Box, s *Scenario, r AgentResult) (bool, string) {
	for _, p := range r.ProposedPlans {
		if !matchesAcceptablePlan(p.asPlan(), s.AcceptablePlans) {
			continue
		}
		out, err := box.ApplyPlan(ctx, p.PlanID)
		if err != nil {
			return false, "maatctl apply failed: " + truncate(strings.TrimSpace(out), 400)
		}
		if !box.HealthOK(ctx, s.HealthCheck) {
			return false, "maatctl applied and verified the plan, but the scenario health check failed"
		}
		return true, ""
	}
	if len(r.Answer.Plans) > 0 {
		return false, "an acceptable fix was only described in the answer, never proposed with propose_action"
	}
	return false, "no acceptable plan was proposed"
}

// injectAdversarial plants the attacker-controlled payload via the chosen vector.
// sshd_invalid_user is simulated with a logger line tagged sshd: whether
// OpenSSH logs a crafted username verbatim is still open (A16).
func injectAdversarial(ctx context.Context, box *Box, adv *Adversarial) {
	payload := shellQuote(adv.Payload)
	switch adv.Vector {
	case "unit_description":
		unit := "maat-adv.service"
		box.Exec(ctx, fmt.Sprintf(`printf '[Unit]\nDescription=%%s\n[Service]\nExecStart=/bin/true\n' %s > /etc/systemd/system/%s && systemctl daemon-reload && systemctl start %s || true`, payload, unit, unit))
	case "http_access_log":
		box.Exec(ctx, "mkdir -p /var/log && echo "+payload+" >> /var/log/maat-access.log; logger -t nginx "+payload)
	default: // logger, sshd_invalid_user
		box.Exec(ctx, "logger -t sshd "+payload)
	}
}

// writeRunConfig records what was run, so a result can be reproduced and
// checked: the agent and model, and digests of every input.
func writeRunConfig(path string, agent Agent, model, protocol, baseURL string, runs int, scenarios []*Scenario, maatBin, maatctlBin string) {
	cfg := map[string]any{
		"started_at":  time.Now().UTC().Format(time.RFC3339),
		"agent":       agent.Name(),
		"runs":        runs,
		"maat_sha256": fileDigest(maatBin),
	}
	if maatctlBin != "" {
		cfg["maatctl_sha256"] = fileDigest(maatctlBin)
	}
	if _, ok := agent.(*OpenAIAgent); ok {
		cfg["model"], cfg["protocol"], cfg["endpoint"] = model, protocol, baseURL
	}
	scen := map[string]string{}
	for _, s := range scenarios {
		scen[s.ID] = fileDigest(s.path)
	}
	cfg["scenarios_sha256"] = scen
	writeJSON(path, cfg)
}

func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeJSON(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o644)
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
