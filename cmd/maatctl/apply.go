package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bolesawny7/maat/internal/actuate"
	"github.com/bolesawny7/maat/internal/plan"
	"github.com/bolesawny7/maat/internal/trail"
)

func runShow(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: maatctl show <plan-id>")
	}
	ps, tr, err := openState()
	if err != nil {
		return err
	}
	p, err := ps.Load(args[0])
	if err != nil {
		return err
	}
	spec := plan.Catalog[p.Kind]
	fmt.Printf("Plan:       %s\n", p.ID)
	fmt.Printf("Status:     %s\n", statusOf(tr, p.ID))
	fmt.Printf("Action:     %s  (%s)\n", p.Kind, spec.Summary)
	fmt.Printf("Danger:     %s\n", spec.DangerLevel)
	fmt.Printf("Parameters: %v\n", p.Params)
	fmt.Printf("Proposed:   %s by %s/%s\n", p.CreatedAt.Format(time.RFC3339), p.Proposer.Client, p.Proposer.Model)
	fmt.Printf("Rationale:  %s\n", p.Rationale)
	if len(p.Evidence) > 0 {
		fmt.Printf("Evidence:   %s\n", strings.Join(p.Evidence, ", "))
	}
	fmt.Printf("\nPreconditions (%d):\n", len(p.Preconditions))
	for _, c := range p.Preconditions {
		fmt.Printf("  - %s\n", describeCheck(c))
	}
	fmt.Printf("Verification (%d):\n", len(p.Verify))
	for _, c := range p.Verify {
		fmt.Printf("  - %s\n", describeCheck(c))
	}
	fmt.Printf("Rollback:   %s", p.Rollback.Kind)
	if p.Rollback.Reason != "" {
		fmt.Printf(" — %s", p.Rollback.Reason)
	}
	fmt.Println()
	fmt.Printf("\nApply with:  maatctl apply %s\n", p.ID)
	return nil
}

func runApply(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: maatctl apply <plan-id>")
	}
	if err := refuseRoot(); err != nil {
		return err
	}
	ps, tr, err := openState()
	if err != nil {
		return err
	}

	// Load once, validate from scratch, and hash so the applied content is bound
	// into the trail and cannot be swapped after review.
	p, err := ps.Load(args[0])
	if err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("plan failed validation: %w", err)
	}
	spec := plan.Catalog[p.Kind]
	if spec.Research {
		return fmt.Errorf("action %q is research-only and is not executed by maatctl", p.Kind)
	}
	data, _ := p.Marshal()
	digest := trail.DigestOf(data)

	act := actuate.New()

	// Show the preview and re-check preconditions right before asking.
	fmt.Printf("About to apply plan %s\n  action: %s %v\n  danger: %s\n  rollback: %s\n\n",
		p.ID, p.Kind, p.Params, spec.DangerLevel, p.Rollback.Kind)

	fmt.Println("Checking preconditions...")
	pre := act.EvaluateAll(ctx, p.Preconditions)
	for _, r := range pre {
		fmt.Printf("  %s %s (actual: %s)\n", mark(r.OK), describeCheck(r.Check), actualOrErr(r))
	}
	if !allOK(pre) {
		return fmt.Errorf("preconditions not met; not applying")
	}

	ok, err := confirm(p.ID)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Aborted. No change was made.")
		return nil
	}

	// Record approval before acting.
	if _, err := tr.AppendAndCheckpoint(trail.Record{
		Type: trail.TypeApproval, Actor: actor(), Plan: p.ID, Digest: digest,
		Summary: "approved " + p.Kind,
	}); err != nil {
		return err
	}

	// Execute.
	fmt.Println("Applying...")
	execErr := act.Execute(ctx, p)
	execSummary := "executed " + p.Kind
	if execErr != nil {
		execSummary = "execution failed: " + execErr.Error()
	}
	tr.AppendAndCheckpoint(trail.Record{Type: trail.TypeExecution, Actor: actor(), Plan: p.ID, Summary: execSummary})
	if execErr != nil {
		return fmt.Errorf("execution failed: %w", execErr)
	}

	// Verify.
	fmt.Println("Verifying...")
	ver := act.VerifyAll(ctx, p.Verify)
	for _, r := range ver {
		fmt.Printf("  %s %s (actual: %s)\n", mark(r.OK), describeCheck(r.Check), actualOrErr(r))
	}
	verOK := allOK(ver)
	tr.AppendAndCheckpoint(trail.Record{
		Type: trail.TypeVerification, Actor: actor(), Plan: p.ID,
		Summary: fmt.Sprintf("verification ok=%v", verOK),
	})

	if !verOK {
		fmt.Println("Verification failed; attempting rollback...")
		rbErr := act.Rollback(ctx, p)
		rbSummary := "rollback ok"
		if rbErr != nil {
			rbSummary = "rollback failed/unavailable: " + rbErr.Error()
		}
		tr.AppendAndCheckpoint(trail.Record{Type: trail.TypeRollback, Actor: actor(), Plan: p.ID, Summary: rbSummary})
		return fmt.Errorf("verification failed; %s", rbSummary)
	}

	fmt.Printf("\nDone. Plan %s applied and verified.\n", p.ID)
	return nil
}

// confirm requires the user to type the plan ID on the controlling terminal.
// Reading from /dev/tty (not stdin) prevents a non-interactive caller, such as an
// agent piping input, from answering on the user's behalf.
func confirm(planID string) (bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, fmt.Errorf("a terminal is required to confirm (could not open /dev/tty): %w", err)
	}
	defer tty.Close()
	fmt.Fprintf(tty, "Type the plan ID to confirm, or anything else to abort:\n  %s\n> ", planID)
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(line) == planID, nil
}

func describeCheck(c plan.Check) string {
	target := c.Unit
	if target == "" {
		target = c.Snap
	}
	switch {
	case c.Equals != "":
		return fmt.Sprintf("%s(%s) == %s", c.Type, target, c.Equals)
	case c.NotEquals != "":
		return fmt.Sprintf("%s(%s) != %s", c.Type, target, c.NotEquals)
	default:
		return fmt.Sprintf("%s(%s)", c.Type, target)
	}
}

func mark(ok bool) string {
	if ok {
		return "[ok]"
	}
	return "[!!]"
}

func actualOrErr(r actuate.CheckResult) string {
	if r.Err != nil {
		return "error: " + r.Err.Error()
	}
	return r.Actual
}

func allOK(rs []actuate.CheckResult) bool {
	for _, r := range rs {
		if !r.OK {
			return false
		}
	}
	return true
}
