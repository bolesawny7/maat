package actuate

import (
	"context"
	"testing"

	"github.com/bolesawny7/maat/internal/plan"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		check  plan.Check
		actual string
		want   bool
	}{
		{plan.Check{Equals: "active"}, "active", true},
		{plan.Check{Equals: "active"}, "failed", false},
		{plan.Check{NotEquals: "active"}, "failed", true},
		{plan.Check{NotEquals: "active"}, "active", false},
		{plan.Check{}, "anything", true}, // no constraint
	}
	for _, c := range cases {
		if got := compare(c.check, c.actual); got != c.want {
			t.Errorf("compare(%+v, %q) = %v, want %v", c.check, c.actual, got, c.want)
		}
	}
}

func TestBoolStr(t *testing.T) {
	if boolStr(true) != "true" || boolStr(false) != "false" {
		t.Error("boolStr wrong")
	}
}

func TestExecuteRejectsResearchKind(t *testing.T) {
	a := New()
	// netplan.try is research-only; it must not be executable here. The plan is
	// constructed directly to bypass proposal-time checks and confirm the
	// actuator itself refuses it.
	p := &plan.Plan{Kind: "netplan.try", Params: plan.Params{"delta": "x=y"}}
	if _, err := a.Execute(context.Background(), p); err == nil {
		t.Error("actuator should refuse to execute a non-executable kind")
	}
}

func TestRollbackKinds(t *testing.T) {
	a := New()
	// "none" and "automatic" need no action.
	for _, kind := range []string{"", "none", "automatic"} {
		p := &plan.Plan{Rollback: plan.Rollback{Kind: kind}}
		if err := a.Rollback(context.Background(), p); err != nil {
			t.Errorf("rollback kind %q should be a no-op, got %v", kind, err)
		}
	}
	// snap.refresh is not yet automated and should report so rather than silently
	// claiming success.
	p := &plan.Plan{Rollback: plan.Rollback{Kind: "snap.refresh"}}
	if err := a.Rollback(context.Background(), p); err == nil {
		t.Error("snap.refresh rollback should report it is not automated")
	}
}
