// Package actuate executes approved plans against the live system and evaluates
// the checks that guard them. It is used only by maatctl, which runs outside
// snap confinement and never as root, so that polkit remains the gatekeeper for
// every privileged operation.
package actuate

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/bolesawny7/maat/internal/collect"
	"github.com/bolesawny7/maat/internal/plan"
)

const (
	systemdDest    = "org.freedesktop.systemd1"
	systemdPath    = "/org/freedesktop/systemd1"
	systemdManager = "org.freedesktop.systemd1.Manager"
)

// Actuator executes actions and evaluates checks.
type Actuator struct {
	snapd *snapdClient
}

// New returns an Actuator.
func New() *Actuator { return &Actuator{snapd: newSnapdClient()} }

// CheckResult is the outcome of evaluating one check.
type CheckResult struct {
	Check  plan.Check
	OK     bool
	Actual string
	Err    error
}

// EvaluateAll evaluates checks once (no polling). It is used for preconditions.
func (a *Actuator) EvaluateAll(ctx context.Context, checks []plan.Check) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		ok, actual, err := a.evaluate(ctx, c)
		out = append(out, CheckResult{Check: c, OK: ok, Actual: actual, Err: err})
	}
	return out
}

// VerifyAll evaluates verification checks, polling each until it passes or its
// WithinSeconds budget elapses.
func (a *Actuator) VerifyAll(ctx context.Context, checks []plan.Check) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		deadline := time.Now().Add(time.Duration(maxInt(c.WithinSeconds, 1)) * time.Second)
		var last CheckResult
		for {
			ok, actual, err := a.evaluate(ctx, c)
			last = CheckResult{Check: c, OK: ok, Actual: actual, Err: err}
			if ok || time.Now().After(deadline) || ctx.Err() != nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		out = append(out, last)
	}
	return out
}

// evaluate computes a single check against live state.
func (a *Actuator) evaluate(ctx context.Context, c plan.Check) (ok bool, actual string, err error) {
	switch c.Type {
	case "unit.active_state":
		st, err := collect.GetUnitStatus(ctx, c.Unit)
		if err != nil {
			return false, "", err
		}
		return compare(c, st.ActiveState), st.ActiveState, nil
	case "snap.installed":
		installed, err := a.snapd.IsInstalled(ctx, c.Snap)
		if err != nil {
			return false, "", err
		}
		return compare(c, boolStr(installed)), boolStr(installed), nil
	case "snap.services_active":
		active, err := a.snapd.ServicesActive(ctx, c.Snap)
		if err != nil {
			return false, "", err
		}
		return compare(c, boolStr(active)), boolStr(active), nil
	case "network.has_default_route":
		ns, err := collect.CollectNetwork(ctx)
		if err != nil {
			return false, "", err
		}
		has := false
		for _, r := range ns.Routes {
			if r.Default {
				has = true
				break
			}
		}
		return compare(c, boolStr(has)), boolStr(has), nil
	default:
		return false, "", fmt.Errorf("unknown check type %q", c.Type)
	}
}

// compare applies the Equals/NotEquals semantics of a check to an actual value.
func compare(c plan.Check, actual string) bool {
	if c.Equals != "" {
		return actual == c.Equals
	}
	if c.NotEquals != "" {
		return actual != c.NotEquals
	}
	return true
}

// Execute performs the action described by the plan. The plan must already have
// been validated and its preconditions checked by the caller.
func (a *Actuator) Execute(ctx context.Context, p *plan.Plan) error {
	switch p.Kind {
	case "service.restart":
		return a.systemdUnitCall(ctx, "RestartUnit", p.Params["unit"], "replace")
	case "service.reset-failed":
		return a.systemdResetFailed(ctx, p.Params["unit"])
	case "snap.revert":
		_, err := a.snapd.Revert(ctx, p.Params["snap"])
		return err
	default:
		return fmt.Errorf("action kind %q is not executable by the production actuator", p.Kind)
	}
}

// Rollback undoes an action when its verification fails and the kind has an
// inverse. Actions whose Rollback.Kind is "none" or "automatic" need nothing.
func (a *Actuator) Rollback(ctx context.Context, p *plan.Plan) error {
	switch p.Rollback.Kind {
	case "", "none", "automatic":
		return nil
	case "snap.refresh":
		// A full implementation would refresh to the recorded revision. v0 leaves
		// this to the operator and records the intent; see roadmap M4.
		return fmt.Errorf("snap.refresh rollback is not yet automated; revert manually to the recorded revision")
	default:
		return fmt.Errorf("unknown rollback kind %q", p.Rollback.Kind)
	}
}

// systemdUnitCall invokes a two-argument systemd Manager method (name, mode).
// It sets the interactive-authorization flag so polkit can prompt when no rule
// pre-authorizes the operation, and succeeds silently when one does.
func (a *Actuator) systemdUnitCall(ctx context.Context, method, unit, mode string) error {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("connect system bus: %w", err)
	}
	defer conn.Close()
	obj := conn.Object(systemdDest, dbus.ObjectPath(systemdPath))
	var jobPath dbus.ObjectPath
	call := obj.CallWithContext(ctx, systemdManager+"."+method, dbus.FlagAllowInteractiveAuthorization, unit, mode)
	if err := call.Store(&jobPath); err != nil {
		return fmt.Errorf("%s %s: %w", method, unit, err)
	}
	return nil
}

// systemdResetFailed invokes the one-argument ResetFailedUnit method.
func (a *Actuator) systemdResetFailed(ctx context.Context, unit string) error {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("connect system bus: %w", err)
	}
	defer conn.Close()
	obj := conn.Object(systemdDest, dbus.ObjectPath(systemdPath))
	call := obj.CallWithContext(ctx, systemdManager+".ResetFailedUnit", dbus.FlagAllowInteractiveAuthorization, unit)
	if err := call.Err; err != nil {
		return fmt.Errorf("ResetFailedUnit %s: %w", unit, err)
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
