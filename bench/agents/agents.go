package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Every agent runs as this unprivileged user, whose kubeconfig can edit
// namespace shop and view the rest of the cluster. Models are served by Ollama
// at ollamaURL. The runner itself runs as a different user.
const (
	agentUser  = "agent"
	agentHome  = "/home/" + agentUser
	agentKube  = agentHome + "/.kube/config"
	ollamaURL  = "http://127.0.0.1:11434"
	runDir     = "/tmp/agents-run" // world-writable, sticky; agents write output files here
	holmesBin  = agentHome + "/.local/bin/holmes"
	k8sgptBin  = "/usr/bin/k8sgpt"
	openBin    = agentHome + "/.opencode/bin/opencode"
	agentPATH  = agentHome + "/.opencode/bin:" + agentHome + "/.local/bin:/usr/local/bin:/usr/bin:/bin"
	workDir    = agentHome + "/work"
	openPrompt = " You are on a machine with kubectl access to the cluster."
)

// Agent is one SRE tool under test.
type Agent interface {
	Name() string
	Version(ctx context.Context) string
	Run(ctx context.Context, model, prompt string) AgentResult
}

func selectAgents(names []string) ([]Agent, error) {
	known := map[string]Agent{"holmes": holmes{}, "k8sgpt": k8sgpt{}, "opencode": opencode{}}
	var out []Agent
	for _, n := range names {
		a, ok := known[n]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (known: holmes, k8sgpt, opencode)", n)
		}
		out = append(out, a)
	}
	return out, nil
}

// asAgent runs bin as the agent user with a fixed environment and argument
// list. sudo and env are exec'd directly; no shell is involved.
func asAgent(ctx context.Context, env []string, bin string, args ...string) (stdout, stderr string, err error) {
	full := []string{"-n", "-u", agentUser, "-H", "env", "HOME=" + agentHome, "KUBECONFIG=" + agentKube, "PATH=" + agentPATH}
	full = append(full, env...)
	full = append(full, bin)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "sudo", full...)
	cmd.Dir = runDir
	cmd.WaitDelay = 10 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// Preflight refuses to run if the agent user can read the administrator
// kubeconfig. k3s rewrites that file with the mode it was started with, and its
// kubectl falls back to the file when KUBECONFIG is unset.
func Preflight(ctx context.Context, adminKubeconfigs ...string) error {
	for _, p := range adminKubeconfigs {
		if err := exec.CommandContext(ctx, "sudo", "-n", "-u", agentUser, "test", "-r", p).Run(); err == nil {
			return fmt.Errorf("the %s user can read %s; run `sudo chmod 600 %s` first", agentUser, p, p)
		}
	}
	if err := os.MkdirAll(runDir, 0o777); err != nil {
		return err
	}
	if fi, err := os.Stat(runDir); err == nil && fi.Mode().Perm() == 0o777 && fi.Mode()&os.ModeSticky != 0 {
		return nil
	}
	return os.Chmod(runDir, 0o777|os.ModeSticky)
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func outFile(prefix string) string {
	return filepath.Join(runDir, fmt.Sprintf("%s-%d.json", prefix, time.Now().UnixNano()))
}

func errText(err error, stderr string) string {
	if err == nil {
		return ""
	}
	tail := strings.TrimSpace(ansi.ReplaceAllString(stderr, ""))
	if len(tail) > 300 {
		tail = tail[len(tail)-300:]
	}
	return strings.TrimSpace(err.Error() + " " + tail)
}

// holmes is HolmesGPT's CLI: `holmes ask` with its default toolsets.
type holmes struct{}

func (holmes) Name() string { return "holmes" }

func (holmes) Version(ctx context.Context) string {
	out, _, _ := asAgent(ctx, nil, holmesBin, "version")
	return strings.TrimSpace(ansi.ReplaceAllString(out, ""))
}

func (holmes) Run(ctx context.Context, model, prompt string) AgentResult {
	file := outFile("holmes")
	stdout, stderr, err := asAgent(ctx, []string{"OLLAMA_API_BASE=" + ollamaURL}, holmesBin,
		"ask", prompt, "--model", "ollama_chat/"+model, "--no-interactive", "--json-output-file", file)
	res := AgentResult{Raw: ansi.ReplaceAllString(stdout+"\n"+stderr, ""), Err: errText(err, stderr)}
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		if res.Err == "" {
			res.Err = "no JSON output: " + rerr.Error()
		}
		return res
	}
	var out struct {
		Result    string `json:"result"`
		ToolCalls []struct {
			ToolName    string          `json:"tool_name"`
			Description string          `json:"description"`
			Result      json.RawMessage `json:"result"`
		} `json:"tool_calls"`
	}
	if jerr := json.Unmarshal(data, &out); jerr != nil {
		res.Err = strings.TrimSpace(res.Err + " parse JSON: " + jerr.Error())
		return res
	}
	res.Answer = out.Result
	var seen strings.Builder
	for _, c := range out.ToolCalls {
		res.Calls = append(res.Calls, Call{Tool: c.ToolName, Input: c.Description})
		seen.Write(c.Result)
		seen.WriteByte('\n')
	}
	res.Seen = seen.String()
	return res
}

// k8sgpt is `k8sgpt analyze --explain` with its default analyzers. It calls no
// tools: its analyzers collect findings and the model explains them.
type k8sgpt struct{}

func (k8sgpt) Name() string { return "k8sgpt" }

func (k8sgpt) Version(ctx context.Context) string {
	out, _, _ := asAgent(ctx, nil, k8sgptBin, "version")
	return strings.TrimSpace(out)
}

func (k8sgpt) Run(ctx context.Context, model, _ string) AgentResult {
	// The backend's model lives in k8sgpt's config, so set it for this run.
	_, _, _ = asAgent(ctx, nil, k8sgptBin, "auth", "remove", "-b", "ollama")
	if _, stderr, err := asAgent(ctx, nil, k8sgptBin, "auth", "add", "-b", "ollama", "-m", model, "-u", ollamaURL); err != nil {
		return AgentResult{Err: "configure backend: " + errText(err, stderr)}
	}
	stdout, stderr, err := asAgent(ctx, nil, k8sgptBin,
		"analyze", "--explain", "--backend", "ollama", "--namespace", ns, "--no-cache", "--output", "json")
	res := AgentResult{Raw: stdout + "\n" + stderr, Err: errText(err, stderr)}
	var out struct {
		Results []struct {
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Error []struct {
				Text string `json:"Text"`
			} `json:"error"`
			Details string `json:"details"`
		} `json:"results"`
	}
	// Analyzers the agent's RBAC can't run print their errors before the JSON.
	start := strings.Index(stdout, "\n{")
	if strings.HasPrefix(stdout, "{") {
		start = 0
	}
	if start < 0 {
		if res.Err == "" {
			res.Err = "no JSON in output"
		}
		return res
	}
	if jerr := json.NewDecoder(strings.NewReader(stdout[start:])).Decode(&out); jerr != nil {
		if res.Err == "" {
			res.Err = "parse JSON: " + jerr.Error()
		}
		return res
	}
	var b, seen strings.Builder
	for _, r := range out.Results {
		fmt.Fprintf(&b, "%s %s:\n%s\n\n", r.Kind, r.Name, r.Details)
		for _, e := range r.Error {
			seen.WriteString(e.Text + "\n")
		}
	}
	res.Answer = strings.TrimSpace(b.String())
	res.Seen = seen.String()
	return res
}

// opencode is OpenCode's non-interactive `opencode run` with its default
// permissions, which let it run shell commands without asking.
type opencode struct{}

func (opencode) Name() string { return "opencode" }

func (opencode) Version(ctx context.Context) string {
	out, _, _ := asAgent(ctx, nil, openBin, "--version")
	return strings.TrimSpace(out)
}

func (opencode) Run(ctx context.Context, model, prompt string) AgentResult {
	stdout, stderr, err := asAgent(ctx, nil, openBin,
		"run", "--format", "json", "--model", "ollama/"+model, "--dir", workDir, prompt+openPrompt)
	res := AgentResult{Raw: stdout + "\n" + ansi.ReplaceAllString(stderr, ""), Err: errText(err, stderr)}
	var texts []string
	var seen strings.Builder
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Part struct {
				Text  string `json:"text"`
				Tool  string `json:"tool"`
				State struct {
					Input  json.RawMessage `json:"input"`
					Output string          `json:"output"`
				} `json:"state"`
			} `json:"part"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "text":
			texts = append(texts, ev.Part.Text)
		case "tool_use":
			res.Calls = append(res.Calls, Call{Tool: ev.Part.Tool, Input: string(ev.Part.State.Input)})
			seen.WriteString(ev.Part.State.Output + "\n")
		}
	}
	res.Answer = strings.Join(texts, "\n")
	res.Seen = seen.String()
	return res
}
