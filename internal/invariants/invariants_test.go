package invariants

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bolesawny7/maat/internal/plan"
)

// repoRoot returns the module root relative to this test's package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

type snapcraft struct {
	Confinement string `yaml:"confinement"`
	Apps        map[string]struct {
		Plugs []string `yaml:"plugs"`
	} `yaml:"apps"`
}

func loadSnapcraft(t *testing.T) snapcraft {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "snap", "snapcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var sc snapcraft
	if err := yaml.Unmarshal(data, &sc); err != nil {
		t.Fatal(err)
	}
	return sc
}

// Invariant 1: the snap declares no network plug.
func TestNoNetworkPlug(t *testing.T) {
	sc := loadSnapcraft(t)
	for app, spec := range sc.Apps {
		for _, p := range spec.Plugs {
			if p == "network" || p == "network-bind" {
				t.Errorf("app %q must not plug %q: the confined server must have no network access", app, p)
			}
		}
	}
}

// Invariant 2: the snap plugs only the approved read-oriented interfaces.
// network-observe is deliberately excluded: testing showed it permits outbound
// TCP, which would break the no-egress guarantee (see docs/security-model.md).
func TestOnlyApprovedPlugs(t *testing.T) {
	allowed := map[string]bool{
		"log-observe": true, "system-observe": true, "mount-observe": true,
	}
	sc := loadSnapcraft(t)
	if sc.Confinement != "strict" {
		t.Errorf("confinement must be strict, got %q", sc.Confinement)
	}
	for app, spec := range sc.Apps {
		for _, p := range spec.Plugs {
			if !allowed[p] {
				t.Errorf("app %q plugs unapproved interface %q; update docs/security-model.md if this is intended", app, p)
			}
		}
	}
}

// goSources returns all non-test .go files under cmd/ and internal/, excluding
// this invariants package.
func goSources(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	for _, dir := range []string{"cmd", "internal"} {
		filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.Contains(path, filepath.Join("internal", "invariants")) {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			out[path] = string(b)
			return nil
		})
	}
	if len(out) == 0 {
		t.Fatal("found no Go sources to scan")
	}
	return out
}

// Invariant 3: no code path spawns a shell or builds a command line from strings.
func TestNoShellExecution(t *testing.T) {
	forbidden := []string{
		`"sh", "-c"`, `"bash", "-c"`, `"/bin/sh"`, `"/bin/bash"`,
		`exec.Command("sh"`, `exec.Command("bash"`,
		`exec.CommandContext(ctx, "sh"`, `exec.CommandContext(ctx, "bash"`,
	}
	for path, src := range goSources(t) {
		for _, f := range forbidden {
			if strings.Contains(src, f) {
				t.Errorf("%s contains forbidden shell pattern %q", rel(t, path), f)
			}
		}
	}
}

// Invariant 4: no code path writes to the sensitive settings that log-observe
// happens to permit (kernel printk ratelimit, AppArmor audit control).
func TestNoWritesToSensitiveLogObservePaths(t *testing.T) {
	sensitive := []string{
		"printk_ratelimit",
		"module/apparmor/parameters/audit",
	}
	for path, src := range goSources(t) {
		for _, s := range sensitive {
			if strings.Contains(src, s) {
				t.Errorf("%s references sensitive path %q; maat must never touch it", rel(t, path), s)
			}
		}
	}
}

// Invariant 7 (source side): release builds of maatctl confirm on /dev/tty, and
// the only other confirmation path is confined to the maatbench build tag.
func TestMaatctlConfirmationHasNoBypass(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "cmd", "maatctl")
	tty, err := os.ReadFile(filepath.Join(dir, "confirm_tty.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(tty), "//go:build !maatbench\n") {
		t.Error("confirm_tty.go must be built exactly when maatbench is not set")
	}
	if !strings.Contains(string(tty), `/dev/tty`) {
		t.Error("maatctl must read confirmation from /dev/tty")
	}
	bench, err := os.ReadFile(filepath.Join(dir, "confirm_bench.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(bench), "//go:build maatbench\n") {
		t.Error("confirm_bench.go must only be built with the maatbench tag")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "confirm_bench.go" || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		for _, bypass := range []string{"MAAT_BENCH_CONFIRM", "MAAT_YES", "--yes", "MAAT_ASSUME_YES", "SKIP_CONFIRM"} {
			if strings.Contains(string(b), bypass) {
				t.Errorf("cmd/maatctl/%s contains a confirmation bypass %q", e.Name(), bypass)
			}
		}
	}
}

// Invariant 7 (root side): maatctl refuses to run as root, and so does the MCP
// server (as a normal user, file permissions keep log-observe's two writable
// settings out of reach).
func TestRefusesRoot(t *testing.T) {
	root := repoRoot(t)
	for _, f := range []string{"cmd/maatctl/main.go", "cmd/maat/main.go"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "Geteuid() == 0") {
			t.Errorf("%s must refuse to run as root (expected a Geteuid() == 0 check)", f)
		}
	}
}

// Invariant 8: release binaries contain no auto-confirm code. This builds
// maatctl both ways and looks for the benchmark-only strings in the binary.
func TestReleaseBinaryHasNoAutoConfirm(t *testing.T) {
	if testing.Short() {
		t.Skip("builds maatctl twice")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	root := repoRoot(t)
	markers := []string{"MAAT_BENCH_CONFIRM", "maatbench build: confirmation supplied"}
	build := func(tags ...string) string {
		out := filepath.Join(t.TempDir(), "maatctl")
		args := append([]string{"build", "-o", out}, tags...)
		cmd := exec.Command(goBin, append(args, "./cmd/maatctl")...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %v: %v\n%s", tags, err, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	release := build()
	for _, m := range markers {
		if strings.Contains(release, m) {
			t.Errorf("release maatctl contains benchmark auto-confirm code (%q)", m)
		}
	}
	bench := build("-tags", "maatbench")
	for _, m := range markers {
		if !strings.Contains(bench, m) {
			t.Errorf("sanity check failed: the maatbench build lacks %q, so this test can't see it", m)
		}
	}
}

// Invariant 6: every executable action kind is complete. It has preconditions
// and verification derived by the catalog, a rollback or a stated reason for
// none, an Execute case in the actuator, and only check types the actuator can
// evaluate.
func TestActionKindsComplete(t *testing.T) {
	root := repoRoot(t)
	actSrc, err := os.ReadFile(filepath.Join(root, "internal", "actuate", "actuate.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(actSrc)
	for _, kind := range plan.Kinds() {
		spec := plan.Catalog[kind]
		if spec.Research {
			continue
		}
		params := plan.Params{}
		for _, p := range spec.Params {
			switch p.Name {
			case "unit":
				params[p.Name] = "example.service"
			case "snap":
				params[p.Name] = "example"
			}
		}
		pre, verify, rb := spec.Build(params)
		if len(pre) == 0 || len(verify) == 0 {
			t.Errorf("%s: needs at least one precondition and one verification step", kind)
		}
		if (rb.Kind == "none" || rb.Kind == "") && rb.Reason == "" {
			t.Errorf("%s: has no rollback and no reason why none is needed", kind)
		}
		if !strings.Contains(src, `case "`+kind+`":`) {
			t.Errorf("%s: the actuator's Execute has no case for it", kind)
		}
		for _, c := range append(pre, verify...) {
			if !strings.Contains(src, `case "`+c.Type+`":`) {
				t.Errorf("%s: the actuator cannot evaluate check type %q", kind, c.Type)
			}
		}
		if rb.Kind != "none" && !strings.Contains(src, `case "`+rb.Kind+`":`) {
			t.Errorf("%s: the actuator's Rollback has no case for %q", kind, rb.Kind)
		}
	}
}

func rel(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.Rel(repoRoot(t), path)
	if err != nil {
		return path
	}
	return r
}
