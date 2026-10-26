// Command maat is the confined half of the project: an MCP server exposing
// read-only diagnostics and propose_action, plus utility subcommands. It is the
// program shipped inside the strict snap.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/brand"
	"github.com/bolesawny7/maat/internal/condense"
	"github.com/bolesawny7/maat/internal/config"
	"github.com/bolesawny7/maat/internal/mcpserver"
	"github.com/bolesawny7/maat/internal/obs"
	"github.com/bolesawny7/maat/internal/plan"
	"github.com/bolesawny7/maat/internal/planstore"
	"github.com/bolesawny7/maat/internal/report"
	"github.com/bolesawny7/maat/internal/selftest"
	"github.com/bolesawny7/maat/internal/termsafe"
	"github.com/bolesawny7/maat/internal/trail"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "mcp":
		err = runMCP(ctx)
	case "diagnose":
		err = runDiagnose(ctx)
	case "plans":
		err = runPlans()
	case "trail":
		err = runTrail(ctx, os.Args[2:])
	case "selftest":
		err = runSelftest(ctx, os.Args[2:])
	case "version", "--version", "-v":
		if brand.IsTerminal(os.Stdout) {
			banner(os.Stdout)
		} else {
			fmt.Println("maat", version)
		}
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "maat: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "maat:", termsafe.Text(err.Error()))
		os.Exit(1)
	}
}

// banner shows the logo on a terminal; maat mcp never calls it, because its
// standard output is the protocol channel.
func banner(f *os.File) {
	brand.Banner(f, "M A A T", "confined, auditable diagnostics for AI agents", "version "+version)
}

func usage() {
	banner(os.Stderr)
	fmt.Fprint(os.Stderr, `maat — confined system diagnostics for AI agents

Usage:
  maat mcp            Run the MCP server over stdio (for an AI client)
  maat diagnose       Print a deterministic diagnosis (no AI)
  maat plans          List proposed plans awaiting review
  maat trail verify   Verify the audit trail's integrity
  maat selftest       Probe maat's own confinement and capabilities
  maat version        Print the version

maat only observes. It never changes the system; fixes are applied by maatctl
after a human approves them.
`)
}

// buildDeps assembles the server dependencies from configuration and data paths.
func buildDeps() (*mcpserver.Deps, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := condense.CompileExtra(cfg.RedactExtra); err != nil {
		return nil, fmt.Errorf("invalid redact_extra pattern: %w", err)
	}
	ps, err := planstore.Open(config.PlansDir())
	if err != nil {
		return nil, err
	}
	tr, err := trail.Open(config.TrailPath())
	if err != nil {
		return nil, err
	}
	return &mcpserver.Deps{
		Redactor: condense.NewRedactor(cfg.RedactExtra...),
		Budget:   obs.Budget{MaxItems: cfg.MaxItems, MaxChars: cfg.MaxChars},
		Plans:    ps,
		Trail:    tr,
		Proposer: plan.Proposer{Client: os.Getenv("MAAT_CLIENT")},
		Version:  version,
	}, nil
}

// runMCP serves the tools to a model. It refuses to run as root: as a normal
// user, ordinary file permissions keep even a compromised server from using the
// two settings log-observe makes writable (the kernel's printk rate limit and
// AppArmor's audit mode, both writable by root only), and from reading other
// users' data.
func runMCP(ctx context.Context) error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("refusing to serve a model as root; run maat as your own user (add it to the systemd-journal group for journal access)")
	}
	deps, err := buildDeps()
	if err != nil {
		return err
	}
	srv := mcpserver.New(deps)
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func runDiagnose(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	banner(os.Stdout)
	report.Run(ctx, os.Stdout, condense.NewRedactor(cfg.RedactExtra...))
	return nil
}

func runPlans() error {
	ps, err := planstore.Open(config.PlansDir())
	if err != nil {
		return err
	}
	entries, err := ps.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("No plans recorded.")
		return nil
	}
	for _, e := range entries {
		if e.Err != nil {
			fmt.Printf("%s  INVALID: %s\n", termsafe.Line(e.ID), termsafe.Line(e.Err.Error()))
			continue
		}
		fmt.Printf("%s  %-22s  %v\n", e.Plan.ID, e.Plan.Kind, e.Plan.Params)
	}
	return nil
}

func runTrail(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return fmt.Errorf("usage: maat trail verify")
	}
	tr, err := trail.Open(config.TrailPath())
	if err != nil {
		return err
	}
	res, err := tr.Verify(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Trail: %s\nRecords: %d\nChain OK: %v\nJournal checkpoints checked: %d (OK: %v)\n",
		config.TrailPath(), res.Records, res.ChainOK, res.CheckpointsChecked, res.CheckpointsOK)
	for _, p := range res.Problems {
		fmt.Println("  PROBLEM:", termsafe.Line(p))
	}
	for _, w := range res.Warnings {
		fmt.Println("  warning:", termsafe.Line(w))
	}
	if !res.OK() {
		return fmt.Errorf("trail verification found tampering")
	}
	return nil
}

func runSelftest(ctx context.Context, args []string) error {
	rep := selftest.Run(ctx)
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}
	if asJSON {
		return rep.WriteJSON(os.Stdout)
	}
	banner(os.Stdout)
	rep.WriteText(os.Stdout)
	if rep.Failed > 0 {
		os.Exit(1)
	}
	return nil
}
