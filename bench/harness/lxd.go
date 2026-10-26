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

// Box is a running LXD container for one scenario attempt.
type Box struct {
	Name     string
	maatPath string // path to the maat binary inside the box
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
	box := &Box{Name: name, maatPath: "/usr/local/bin/maat"}
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

// Exec runs a shell command inside the box as root and returns combined output.
// The command string is scenario-authored (trusted), unlike anything the agent
// provides, which never reaches a shell.
func (b *Box) Exec(ctx context.Context, shellCmd string) (string, error) {
	return lxc(ctx, "exec", b.Name, "--", "bash", "-lc", shellCmd)
}

// Push copies a host file into the box at dest and makes it executable.
func (b *Box) Push(ctx context.Context, hostPath, dest string) error {
	if _, err := lxc(ctx, "file", "push", hostPath, b.Name+dest); err != nil {
		return err
	}
	_, err := b.Exec(ctx, "chmod +x "+dest)
	return err
}

// InstallMaat pushes the maat binary into the box so the harness can drive it
// over MCP. The benchmark runs maat as an unconfined binary in the box; the
// confinement guarantees are tested separately by `maat selftest` in the snap.
func (b *Box) InstallMaat(ctx context.Context, hostMaatPath string) error {
	return b.Push(ctx, hostMaatPath, b.maatPath)
}

// MCPClient connects an MCP client to a maat server running inside the box,
// bridging stdio through `lxc exec`. This exercises the real server and tools.
func (b *Box) MCPClient(ctx context.Context) (*mcp.ClientSession, error) {
	cmd := exec.Command("lxc", "exec", b.Name, "--", b.maatPath, "mcp")
	client := mcp.NewClient(&mcp.Implementation{Name: "maat-bench", Version: "0.1"}, nil)
	return client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
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

// ApplyFix executes an accepted plan inside the box using the container's own
// tools. The bench does not use maatctl for this: maatctl always requires a
// human on a terminal (a security invariant), so the benchmark verifies the
// effect of a proposed plan by performing the equivalent operation directly.
func (b *Box) ApplyFix(ctx context.Context, p Plan) error {
	switch p.Kind {
	case "service.restart":
		_, err := b.Exec(ctx, "systemctl restart "+shellQuote(p.Params["unit"]))
		return err
	case "service.reset-failed":
		_, err := b.Exec(ctx, "systemctl reset-failed "+shellQuote(p.Params["unit"]))
		return err
	case "snap.revert":
		_, err := b.Exec(ctx, "snap revert "+shellQuote(p.Params["snap"]))
		return err
	default:
		return fmt.Errorf("bench cannot apply plan kind %q", p.Kind)
	}
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
// command. Plan parameters are already validated by the catalog, but quoting is
// defence in depth for the benchmark's own command construction.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
