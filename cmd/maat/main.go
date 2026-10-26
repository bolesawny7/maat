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

	"github.com/bolesawny7/maat/internal/condense"
	"github.com/bolesawny7/maat/internal/config"
	"github.com/bolesawny7/maat/internal/mcpserver"
	"github.com/bolesawny7/maat/internal/obs"
	"github.com/bolesawny7/maat/internal/plan"
	"github.com/bolesawny7/maat/internal/planstore"
	"github.com/bolesawny7/maat/internal/report"
	"github.com/bolesawny7/maat/internal/selftest"
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
		fmt.Println("maat", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "maat: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "maat:", err)
		os.Exit(1)
	}
}

func usage() {
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
	}, nil
}

func runMCP(ctx context.Context) error {
	deps, err := buildDeps()
	if err != nil {
		return err
	}
	srv := mcpserver.New(deps)
	return srv.Run(ctx, &mcp.StdioTransport{})
}

func runDiagnose(ctx context.Context) error {
	cfg, _ := config.Load()
	report.Run(ctx, os.Stdout, condense.NewRedactor(cfg.RedactExtra...))
	return nil
}

func runPlans() error {
	ps, err := planstore.Open(config.PlansDir())
	if err != nil {
		return err
	}
	plans, err := ps.List()
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		fmt.Println("No plans recorded.")
		return nil
	}
	for _, p := range plans {
		fmt.Printf("%s  %-22s  %v\n", p.ID, p.Kind, p.Params)
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
	fmt.Printf("Records: %d\nChain OK: %v\nCheckpoints checked: %d (OK: %v)\n",
		res.Records, res.ChainOK, res.CheckpointsChecked, res.CheckpointsOK)
	for _, p := range res.Problems {
		fmt.Println("  problem:", p)
	}
	if !res.ChainOK || !res.CheckpointsOK {
		return fmt.Errorf("trail verification found problems")
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
	rep.WriteText(os.Stdout)
	if rep.Failed > 0 {
		os.Exit(1)
	}
	return nil
}
