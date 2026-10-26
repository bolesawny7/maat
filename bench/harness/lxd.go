package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Inside every box maat runs the way it is meant to be deployed: as an
// unprivileged user in the systemd-journal group, never as root. Fixes are
// applied by the real maatctl (built with the maatbench tag), authorized by a
// polkit rule scoped to this user and to the restart and reset-failed verbs:
// the per-unit, per-verb rule an administrator would write.
const (
	benchUser    = "maatbench"
	benchHome    = "/home/" + benchUser
	benchData    = benchHome + "/.local/state/maat"
	maatPath     = "/usr/local/bin/maat"
	maatctlPath  = "/usr/local/bin/maatctl"
	polkitRule   = "/etc/polkit-1/rules.d/49-maat-bench.rules"
	polkitSource = `polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" &&
        subject.user == "` + benchUser + `" &&
        (action.lookup("verb") == "restart" || action.lookup("verb") == "reset-failed")) {
        return polkit.Result.YES;
    }
});
`
)

// Box is a running LXD container for one scenario attempt.
type Box struct {
	Name string
}

// lxc runs the lxc CLI with a fixed argument list and returns stdout.
func lxc(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "lxc", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("lxc %s: %w: %s", strings.Join(args, " "), err, errb.String())
	}
	return out.String(), nil
}

// Launch creates and starts a container from the scenario image, waits for it to
// be reachable, and returns a Box.
func Launch(ctx context.Context, name, image string) (*Box, error) {
	if _, err := lxc(ctx, "launch", image, name); err != nil {
		return nil, err
	}
	box := &Box{Name: name}
	if err := box.waitReady(ctx); err != nil {
		box.Destroy(context.Background())
		return nil, err
	}
	return box, nil
}

// waitReady blocks until the container's system is responsive.
func (b *Box) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, err := b.Exec(ctx, "systemctl is-system-running --wait 2>/dev/null; echo ready")
		if err == nil && strings.Contains(out, "ready") {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("container %s did not become ready", b.Name)
}

// ImageFingerprint returns the fingerprint of the image the box was made from.
func (b *Box) ImageFingerprint(ctx context.Context) string {
	out, err := lxc(ctx, "config", "get", b.Name, "volatile.base_image")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Exec runs a shell command inside the box as root and returns its output. The
// command string is scenario-authored (trusted), unlike anything the agent
// provides, which never reaches a shell.
func (b *Box) Exec(ctx context.Context, shellCmd string) (string, error) {
	return lxc(ctx, "exec", b.Name, "--", "bash", "-lc", shellCmd)
}

// asBenchUser builds the argument list that runs argv as the bench user with
// maat's data directory pinned, so the server and maatctl share it.
func asBenchUser(argv ...string) []string {
	return append([]string{"runuser", "-u", benchUser, "--", "env", "HOME=" + benchHome, "MAAT_DATA=" + benchData}, argv...)
}

// RunAsBenchUser runs argv (no shell) as the bench user and returns combined
// output.
func (b *Box) RunAsBenchUser(ctx context.Context, env []string, argv ...string) (string, error) {
	args := append([]string{"exec", b.Name, "--"}, asBenchUser(append(env, argv...)...)...)
	cmd := exec.CommandContext(ctx, "lxc", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Push copies a host file into the box at dest and makes it executable.
func (b *Box) Push(ctx context.Context, hostPath, dest string) error {
	if _, err := lxc(ctx, "file", "push", hostPath, b.Name+dest); err != nil {
		return err
	}
	_, err := b.Exec(ctx, "chmod 0755 "+dest)
	return err
}

// InstallMaat pushes the maat and maatctl binaries, creates the unprivileged
// bench user with journal access, and installs the bench's polkit rule.
func (b *Box) InstallMaat(ctx context.Context, maatBin, maatctlBin string) error {
	if err := b.Push(ctx, maatBin, maatPath); err != nil {
		return err
	}
	if maatctlBin != "" {
		if err := b.Push(ctx, maatctlBin, maatctlPath); err != nil {
			return err
		}
	}
	setup := fmt.Sprintf(`set -e
id %[1]s >/dev/null 2>&1 || useradd --create-home --shell /bin/bash %[1]s
usermod -aG systemd-journal %[1]s
mkdir -p /etc/polkit-1/rules.d
cat > %[2]s <<'RULE'
%[3]sRULE
systemctl try-restart polkit.service || true`, benchUser, polkitRule, polkitSource)
	_, err := b.Exec(ctx, setup)
	return err
}

// MCPClient connects an MCP client to a maat server running inside the box as
// the bench user, bridging stdio through `lxc exec`.
func (b *Box) MCPClient(ctx context.Context) (*mcp.ClientSession, error) {
	args := append([]string{"exec", b.Name, "--"}, asBenchUser("MAAT_CLIENT=maat-bench", maatPath, "mcp")...)
	cmd := exec.Command("lxc", args...)
	client := mcp.NewClient(&mcp.Implementation{Name: "maat-bench", Version: "0.2"}, nil)
	return client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
}

// ApplyPlan runs the real maatctl against a plan the server stored, with the
// harness supplying the confirmation that a person would type. It reports
// maatctl's output; success means maatctl applied and verified the plan.
func (b *Box) ApplyPlan(ctx context.Context, planID string) (string, error) {
	return b.RunAsBenchUser(ctx, []string{"MAAT_BENCH_CONFIRM=" + planID}, maatctlPath, "apply", planID)
}

// VerifyTrail runs `maat trail verify` as the bench user.
func (b *Box) VerifyTrail(ctx context.Context) (string, error) {
	return b.RunAsBenchUser(ctx, nil, maatPath, "trail", "verify")
}

// Destroy force-deletes the container. It is safe to call more than once.
func (b *Box) Destroy(ctx context.Context) {
	_, _ = lxc(ctx, "delete", "--force", b.Name)
}

// CleanupOrphans deletes any leftover containers whose names start with prefix.
// It is called before a run so an earlier crash can't leave boxes behind that
// collide with new ones or hold resources.
func CleanupOrphans(ctx context.Context, prefix string) int {
	out, err := lxc(ctx, "list", "-c", "n", "--format", "csv")
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && strings.HasPrefix(name, prefix) {
			if _, err := lxc(ctx, "delete", "--force", name); err == nil {
				n++
			}
		}
	}
	return n
}

// HealthOK runs the scenario's health check and reports whether it passed.
func (b *Box) HealthOK(ctx context.Context, check string) bool {
	if check == "" {
		return true
	}
	_, err := b.Exec(ctx, check)
	return err == nil
}

// shellQuote single-quotes a value for safe inclusion in a bench-side shell
// command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
