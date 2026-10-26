// Command maatctl is the unconfined half of the project: it shows plans proposed
// by the agent and executes the ones a human approves. It runs as the invoking
// user and refuses to run as root, so that polkit remains the gatekeeper for
// every privileged operation and the audit trail names a real person.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bolesawny7/maat/internal/config"
	"github.com/bolesawny7/maat/internal/planstore"
	"github.com/bolesawny7/maat/internal/trail"
)

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
	case "list":
		err = runList()
	case "show":
		err = runShow(os.Args[2:])
	case "apply":
		err = runApply(ctx, os.Args[2:])
	case "reject":
		err = runReject(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("maatctl", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "maatctl: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "maatctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `maatctl — review and apply maat plans

Usage:
  maatctl list              List proposed plans and their status
  maatctl show <plan-id>    Show a plan: action, evidence, preview, rollback
  maatctl apply <plan-id>   Apply a plan after on-terminal confirmation
  maatctl reject <plan-id>  Record that a plan was rejected

maatctl must be run as your own user, not root: privileged steps are authorized
through polkit, which prompts you (or follows a rule you configured).
`)
}

// refuseRoot exits if maatctl is run as root. polkit, not sudo, is the intended
// gate; running as root would bypass it and record the wrong actor.
func refuseRoot() error {
	if os.Geteuid() == 0 {
		return fmt.Errorf("refusing to run as root; run as your own user so polkit authorizes the change and the trail records you")
	}
	return nil
}

func openState() (*planstore.Store, *trail.Trail, error) {
	ps, err := planstore.Open(config.PlansDir())
	if err != nil {
		return nil, nil, err
	}
	tr, err := trail.Open(config.TrailPath())
	if err != nil {
		return nil, nil, err
	}
	return ps, tr, nil
}

// statusOf derives a plan's status from the trail: applied, rejected, or pending.
func statusOf(tr *trail.Trail, planID string) string {
	recs, err := tr.ReadAll()
	if err != nil {
		return "unknown"
	}
	status := "pending"
	for _, r := range recs {
		if r.Plan != planID {
			continue
		}
		switch r.Type {
		case trail.TypeExecution:
			status = "applied"
		case trail.TypeRollback:
			status = "rolled-back"
		case "rejection":
			status = "rejected"
		}
	}
	return status
}

func runList() error {
	ps, tr, err := openState()
	if err != nil {
		return err
	}
	plans, err := ps.List()
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		fmt.Println("No plans. The agent records plans here when it proposes a fix.")
		return nil
	}
	fmt.Printf("%-36s  %-20s  %-11s  %s\n", "PLAN ID", "KIND", "STATUS", "PARAMS")
	for _, p := range plans {
		fmt.Printf("%-36s  %-20s  %-11s  %v\n", p.ID, p.Kind, statusOf(tr, p.ID), p.Params)
	}
	return nil
}

func runReject(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: maatctl reject <plan-id>")
	}
	if err := refuseRoot(); err != nil {
		return err
	}
	ps, tr, err := openState()
	if err != nil {
		return err
	}
	p, err := ps.Load(args[0])
	if err != nil {
		return err
	}
	_, err = tr.AppendAndCheckpoint(trail.Record{
		Type:    "rejection",
		Actor:   actor(),
		Plan:    p.ID,
		Summary: "rejected " + p.Kind,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Recorded rejection of plan %s.\n", p.ID)
	return nil
}

// actor returns a label for who is acting, for the trail.
func actor() string {
	if u := os.Getenv("USER"); u != "" {
		return "maatctl:" + u
	}
	return "maatctl"
}
