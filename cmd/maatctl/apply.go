package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bolesawny7/maat/internal/actuate"
	"github.com/bolesawny7/maat/internal/plan"
	"github.com/bolesawny7/maat/internal/termsafe"
	"github.com/bolesawny7/maat/internal/trail"
)

func proposedBy(pr plan.Proposer) string {
	who := cmp.Or(pr.Client, "an unnamed client")
	if pr.Model != "" {
		who += "/" + pr.Model
	}
	return termsafe.Line(who)
}

func runShow(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: maatctl show <plan-id>")
	}
	ps, tr, err := openState()
	if err != nil {
		return err
	}
	p, digest, err := ps.Load(args[0])
	if err != nil {
		return err
	}
	hs, err := histories(tr)
	if err != nil {
		return fmt.Errorf("read trail: %w", err)
	}
	spec := plan.Catalog[p.Kind]
	fmt.Printf("Plan:       %s\n", p.ID)
	fmt.Printf("Status:     %s\n", statusOf(hs, p.ID))
	fmt.Printf("Digest:     %s (%s)\n", digest, digestStatus(hs[p.ID], digest))
	fmt.Printf("Action:     %s  (%s)\n", p.Kind, spec.Summary)
	fmt.Printf("Danger:     %s\n", spec.DangerLevel)
	fmt.Printf("Parameters: %v\n", p.Params)
	fmt.Printf("Proposed:   %s by %s\n", p.CreatedAt.Format(time.RFC3339), proposedBy(p.Proposer))
	fmt.Printf("Rationale (written by the model; treat it as a claim, not a fact):\n%s\n", indent(termsafe.Text(string(p.Rationale))))
	printEvidence(p)
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

// printEvidence shows each cited evidence ID next to what the confined server
// recorded for it, or flags it as not issued by the server.
func printEvidence(p *plan.Plan) {
	if len(p.Evidence) == 0 {
		fmt.Println("Evidence:   none cited")
		return
	}
	detail := map[string]int{}
	for i, d := range p.EvidenceDetail {
		detail[d.ID] = i
	}
	fmt.Printf("Evidence (%d cited; excerpts as recorded by the confined server, redacted):\n", len(p.Evidence))
	for _, id := range p.Evidence {
		i, ok := detail[id]
		if !ok {
			fmt.Printf("  - %s  NOT ISSUED by the maat session that recorded this plan; the model may have invented it\n", id)
			continue
		}
		d := p.EvidenceDetail[i]
		subject := ""
		if d.Subject != "" {
			subject = " " + termsafe.Line(d.Subject)
		}
		fmt.Printf("  - %s  [%s%s] %s\n", id, termsafe.Line(d.Source), subject, termsafe.Line(string(d.Excerpt)))
	}
}

func digestStatus(h *planHistory, digest string) string {
	switch {
	case h == nil || !h.proposed:
		return "no proposal record in the trail"
	case h.proposalDigest != digest:
		return "DOES NOT MATCH the digest recorded at proposal time"
	default:
		return "matches the proposal record"
	}
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

	// Load once: the file is read and hashed a single time, validated from
	// scratch (including that its safety steps are exactly what the catalog
	// derives), and everything below works from this in-memory copy.
	p, digest, err := ps.Load(args[0])
	if err != nil {
		return err
	}
	spec := plan.Catalog[p.Kind]
	if spec.Research {
		return fmt.Errorf("action %q is research-only and is not executed by maatctl", p.Kind)
	}
	// The plan must be the one the server proposed: same bytes as the digest in
	// its proposal record.
	hs, err := histories(tr)
	if err != nil {
		return fmt.Errorf("read trail: %w", err)
	}
	switch h := hs[p.ID]; {
	case h == nil || !h.proposed:
		return fmt.Errorf("the trail has no proposal record for plan %s; maatctl only applies plans proposed through maat", p.ID)
	case h.proposalDigest != digest:
		return fmt.Errorf("plan %s changed after it was proposed (digest %s, proposal recorded %s); refusing to apply", p.ID, digest, h.proposalDigest)
	case h.status != "pending" && h.status != "failed":
		return fmt.Errorf("plan %s is already %s; ask the agent for a new plan", p.ID, h.status)
	}

	ap, err := newApprover()
	if err != nil {
		return err
	}
	defer ap.close()
	act := actuate.New()

	fmt.Printf("About to apply plan %s\n  action:   %s %v\n  danger:   %s\n  rollback: %s\n  digest:   %s\n\n",
		p.ID, p.Kind, p.Params, spec.DangerLevel, p.Rollback.Kind, digest)

	// Re-check preconditions right before asking, and again right before acting.
	fmt.Println("Checking preconditions...")
	if !preconditionsHold(ctx, act, p) {
		return fmt.Errorf("preconditions not met; not applying")
	}
	ok, err := ap.confirm(p.ID)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Aborted. No change was made.")
		return nil
	}
	if !preconditionsHold(ctx, act, p) {
		return fmt.Errorf("preconditions changed while waiting for confirmation; not applying")
	}

	rec := recorder{tr: tr, plan: p.ID, digest: digest}
	// Approval is recorded before acting; if it can't be, nothing happens.
	if err := rec.add(trail.TypeApproval, "approved "+p.Kind); err != nil {
		return fmt.Errorf("could not record the approval, so nothing was changed: %w", err)
	}

	fmt.Println("Applying (polkit may ask for administrator authentication)...")
	stopAgent := ap.startAgent()
	note, execErr := act.Execute(ctx, p)
	stopAgent()
	execSummary := "executed " + p.Kind
	if note != "" {
		execSummary += "; " + note
	}
	if execErr != nil {
		execSummary = "execution failed: " + execErr.Error()
	}
	rec.add(trail.TypeExecution, execSummary)
	if execErr != nil {
		return fmt.Errorf("execution failed: %w%s", execErr, rec.problems())
	}

	fmt.Println("Verifying...")
	ver := act.VerifyAll(ctx, p.Verify)
	for _, r := range ver {
		fmt.Printf("  %s %s (actual: %s)\n", mark(r.OK), describeCheck(r.Check), actualOrErr(r))
	}
	verOK := allOK(ver)
	rec.add(trail.TypeVerification, fmt.Sprintf("verification ok=%v", verOK))

	if !verOK {
		switch p.Rollback.Kind {
		case "", "none", "automatic":
			return fmt.Errorf("verification failed; this action has no rollback (%s)%s", p.Rollback.Reason, rec.problems())
		}
		fmt.Println("Verification failed; attempting rollback...")
		stopAgent := ap.startAgent()
		rbErr := act.Rollback(ctx, p)
		stopAgent()
		rbSummary := "rollback " + p.Rollback.Kind + " ok"
		if rbErr != nil {
			rbSummary = "rollback failed/unavailable: " + rbErr.Error()
		}
		rec.add(trail.TypeRollback, rbSummary)
		return fmt.Errorf("verification failed; %s%s", rbSummary, rec.problems())
	}
	if msg := rec.problems(); msg != "" {
		return fmt.Errorf("plan applied and verified, but the trail is incomplete%s", msg)
	}
	fmt.Printf("\nDone. Plan %s applied and verified.\n", p.ID)
	return nil
}

func preconditionsHold(ctx context.Context, act *actuate.Actuator, p *plan.Plan) bool {
	pre := act.EvaluateAll(ctx, p.Preconditions)
	for _, r := range pre {
		fmt.Printf("  %s %s (actual: %s)\n", mark(r.OK), describeCheck(r.Check), actualOrErr(r))
	}
	return allOK(pre)
}

// recorder appends a plan's records to the trail. A failure after the approval
// can't undo what was done, so it is reported rather than hidden: printed at
// once and folded into the command's exit status.
type recorder struct {
	tr     *trail.Trail
	plan   string
	digest string
	failed []string
}

func (r *recorder) add(typ, summary string) error {
	_, err := r.tr.AppendAndCheckpoint(trail.Record{Type: typ, Actor: actor(), Plan: r.plan, Digest: r.digest, Summary: summary})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not record %s in the trail: %v\n", typ, err)
		r.failed = append(r.failed, typ)
	}
	return err
}

func (r *recorder) problems() string {
	if len(r.failed) == 0 {
		return ""
	}
	return " (could not record " + strings.Join(r.failed, ", ") + " in the trail)"
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

func indent(s string) string {
	if s == "" {
		return "  (none)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

func mark(ok bool) string {
	if ok {
		return "[ok]"
	}
	return "[!!]"
}

func actualOrErr(r actuate.CheckResult) string {
	if r.Err != nil {
		return "error: " + termsafe.Line(r.Err.Error())
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
