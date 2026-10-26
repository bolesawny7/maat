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
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/bolesawny7/maat/internal/brand"
	"github.com/bolesawny7/maat/internal/config"
	"github.com/bolesawny7/maat/internal/planstore"
	"github.com/bolesawny7/maat/internal/termsafe"
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
		if brand.IsTerminal(os.Stdout) {
			banner(os.Stdout)
		} else {
			fmt.Println("maatctl", version)
		}
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "maatctl: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "maatctl:", termsafe.Text(err.Error()))
		os.Exit(1)
	}
}

// banner shows the logo on a terminal only.
func banner(f *os.File) {
	brand.Banner(f, "M A A T", "maatctl: review and apply maat plans", "version "+version)
}

func usage() {
	banner(os.Stderr)
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

// planHistory is what the trail says about one plan.
type planHistory struct {
	status         string
	proposalDigest string
	proposed       bool
}

// histories derives every plan's history from one read of the trail.
func histories(tr *trail.Trail) (map[string]*planHistory, error) {
	recs, err := tr.ReadAll()
	if err != nil {
		return nil, err
	}
	out := map[string]*planHistory{}
	for _, r := range recs {
		if r.Plan == "" {
			continue
		}
		h := out[r.Plan]
		if h == nil {
			h = &planHistory{status: "pending"}
			out[r.Plan] = h
		}
		switch r.Type {
		case trail.TypeProposal:
			h.proposed = true
			h.proposalDigest = r.Digest
		case trail.TypeExecution:
			// A failed execution (for example, polkit refused) changed nothing,
			// so the plan can be retried.
			h.status = "applied"
			if strings.HasPrefix(r.Summary, "execution failed") {
				h.status = "failed"
			}
		case trail.TypeVerification:
			if strings.HasSuffix(r.Summary, "ok=false") {
				h.status = "verify-failed"
			}
		case trail.TypeRollback:
			h.status = "rolled-back"
		case trail.TypeRejection:
			h.status = "rejected"
		}
	}
	return out, nil
}

func statusOf(hs map[string]*planHistory, planID string) string {
	if h, ok := hs[planID]; ok {
		return h.status
	}
	return "unrecorded"
}

func runList() error {
	ps, tr, err := openState()
	if err != nil {
		return err
	}
	entries, err := ps.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("No plans. The agent records plans here when it proposes a fix.")
		return nil
	}
	hs, err := histories(tr)
	if err != nil {
		return fmt.Errorf("read trail: %w", err)
	}
	fmt.Printf("%-36s  %-20s  %-11s  %s\n", "PLAN ID", "KIND", "STATUS", "PARAMS")
	for _, e := range entries {
		if e.Err != nil {
			fmt.Printf("%-36s  %-20s  %-11s  %s\n", termsafe.Line(e.ID), "-", "INVALID", termsafe.Line(e.Err.Error()))
			continue
		}
		p := e.Plan
		fmt.Printf("%-36s  %-20s  %-11s  %s\n", p.ID, p.Kind, statusOf(hs, p.ID), termsafe.Line(fmt.Sprint(p.Params)))
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
	// A plan that no longer validates can still be rejected, by its ID.
	summary := "rejected plan"
	p, digest, err := ps.Load(args[0])
	if err == nil {
		summary = "rejected " + p.Kind
	} else if digest == "" {
		return err
	}
	if _, err := tr.AppendAndCheckpoint(trail.Record{
		Type:    trail.TypeRejection,
		Actor:   actor(),
		Plan:    args[0],
		Digest:  digest,
		Summary: summary,
	}); err != nil {
		return err
	}
	fmt.Printf("Recorded rejection of plan %s.\n", termsafe.Line(args[0]))
	return nil
}

// actor names the person acting, for the trail. It comes from the real user ID,
// not from $USER, which any caller can set.
func actor() string {
	uid := strconv.Itoa(os.Getuid())
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		return "maatctl:" + u.Username
	}
	return "maatctl:uid=" + uid
}
