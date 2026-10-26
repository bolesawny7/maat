package invariants

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// Invariant 7 (source side): maatctl always confirms on /dev/tty and has no
// environment-variable bypass of confirmation.
func TestMaatctlConfirmationHasNoBypass(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "cmd", "maatctl", "apply.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, `/dev/tty`) {
		t.Error("maatctl must read confirmation from /dev/tty")
	}
	for _, bypass := range []string{"MAAT_YES", "--yes", "MAAT_ASSUME_YES", "SKIP_CONFIRM"} {
		if strings.Contains(src, bypass) {
			t.Errorf("maatctl apply contains a confirmation bypass %q", bypass)
		}
	}
}

// Invariant 7 (root side): maatctl refuses to run as root.
func TestMaatctlRefusesRoot(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "cmd", "maatctl", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Geteuid() == 0") {
		t.Error("maatctl must refuse to run as root (expected a Geteuid() == 0 check)")
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
