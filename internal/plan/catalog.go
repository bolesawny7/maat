package plan

import (
	"fmt"
	"regexp"
	"sort"
)

// Spec defines one action kind: its parameters, how to validate them, and how to
// build the safety steps (preconditions, verification, rollback) for a given set
// of parameters. The Build function is the authoritative source of those steps;
// a proposer never supplies them.
type Spec struct {
	Kind        string
	Summary     string
	Params      []ParamSpec
	Build       func(p Params) (pre, verify []Check, rb Rollback)
	Research    bool // if true, not executable by the production actuator
	DangerLevel string
}

// ParamSpec describes one parameter of an action.
type ParamSpec struct {
	Name        string
	Required    bool
	Description string
	Validate    func(string) error
}

func (s Spec) validateParams(p Params) error {
	known := map[string]bool{}
	for _, ps := range s.Params {
		known[ps.Name] = true
		v, present := p[ps.Name]
		if !present || v == "" {
			if ps.Required {
				return fmt.Errorf("action %q requires parameter %q", s.Kind, ps.Name)
			}
			continue
		}
		if ps.Validate != nil {
			if err := ps.Validate(v); err != nil {
				return fmt.Errorf("action %q parameter %q: %w", s.Kind, ps.Name, err)
			}
		}
	}
	for name := range p {
		if !known[name] {
			return fmt.Errorf("action %q has no parameter %q", s.Kind, name)
		}
	}
	return nil
}

var (
	// unitNameRe matches a systemd unit name with a recognized suffix. This is a
	// format check; whether the unit exists is confirmed against the live system
	// at apply time.
	unitNameRe = regexp.MustCompile(`^[a-zA-Z0-9:_.\\@-]+\.(service|socket|timer|target|mount|path|slice|scope|automount|swap)$`)
	// snapNameRe matches snap naming rules: lowercase letters, digits, and
	// hyphens, not starting or ending with a hyphen.
	snapNameRe = regexp.MustCompile(`^[a-z0-9](?:-?[a-z0-9])*$`)
	// revisionRe matches a snap revision: digits, or "xN" for sideloaded.
	revisionRe = regexp.MustCompile(`^x?\d+$`)
)

func validateUnitName(s string) error {
	if len(s) > 255 {
		return fmt.Errorf("unit name too long")
	}
	if !unitNameRe.MatchString(s) {
		return fmt.Errorf("not a valid systemd unit name")
	}
	return nil
}

func validateSnapName(s string) error {
	if len(s) > 40 || !snapNameRe.MatchString(s) {
		return fmt.Errorf("not a valid snap name")
	}
	return nil
}

func validateRevision(s string) error {
	if !revisionRe.MatchString(s) {
		return fmt.Errorf("not a valid snap revision")
	}
	return nil
}

// Catalog is the closed set of actions maat can propose and execute. Adding a
// kind here is a deliberate, reviewed change accompanied by a threat review and
// a benchmark scenario.
var Catalog = map[string]Spec{
	"service.restart": {
		Kind:        "service.restart",
		Summary:     "Restart a failed systemd service",
		DangerLevel: "medium",
		Params: []ParamSpec{
			{Name: "unit", Required: true, Description: "systemd unit to restart", Validate: validateUnitName},
		},
		Build: func(p Params) ([]Check, []Check, Rollback) {
			unit := p["unit"]
			pre := []Check{{Type: "unit.active_state", Unit: unit, NotEquals: "active"}}
			verify := []Check{{Type: "unit.active_state", Unit: unit, Equals: "active", WithinSeconds: 30}}
			rb := Rollback{Kind: "none", Reason: "A restart leaves no persistent on-disk change to undo; failure is surfaced by the verification step."}
			return pre, verify, rb
		},
	},
	"service.reset-failed": {
		Kind:        "service.reset-failed",
		Summary:     "Clear the failed state of a systemd unit whose cause is already resolved",
		DangerLevel: "low",
		Params: []ParamSpec{
			{Name: "unit", Required: true, Description: "systemd unit to reset", Validate: validateUnitName},
		},
		Build: func(p Params) ([]Check, []Check, Rollback) {
			unit := p["unit"]
			pre := []Check{{Type: "unit.active_state", Unit: unit, Equals: "failed"}}
			verify := []Check{{Type: "unit.active_state", Unit: unit, NotEquals: "failed", WithinSeconds: 10}}
			rb := Rollback{Kind: "none", Reason: "Resetting failed state only clears a flag; there is nothing to undo."}
			return pre, verify, rb
		},
	},
	"snap.revert": {
		Kind:        "snap.revert",
		Summary:     "Revert a snap to its previous revision after a bad update",
		DangerLevel: "high",
		Params: []ParamSpec{
			{Name: "snap", Required: true, Description: "snap to revert", Validate: validateSnapName},
			{Name: "to_revision", Required: false, Description: "revision to return to on rollback", Validate: validateRevision},
		},
		Build: func(p Params) ([]Check, []Check, Rollback) {
			snap := p["snap"]
			pre := []Check{{Type: "snap.installed", Snap: snap, Equals: "true"}}
			verify := []Check{{Type: "snap.services_active", Snap: snap, Equals: "true", WithinSeconds: 60}}
			rb := Rollback{Kind: "snap.refresh", Reason: "Refresh back to the revision recorded before the revert.", Params: Params{"snap": snap}}
			return pre, verify, rb
		},
	},
	"netplan.try": {
		Kind:        "netplan.try",
		Summary:     "Apply a netplan change with automatic rollback (research; not executed by the production actuator)",
		DangerLevel: "high",
		Research:    true,
		Params: []ParamSpec{
			{Name: "delta", Required: true, Description: "netplan set-style delta, e.g. ethernets.eth0.dhcp4=true"},
			{Name: "timeout_seconds", Required: false, Description: "seconds before automatic revert", Validate: func(s string) error {
				if !regexp.MustCompile(`^\d{1,3}$`).MatchString(s) {
					return fmt.Errorf("timeout must be 1-3 digits of seconds")
				}
				return nil
			}},
		},
		Build: func(p Params) ([]Check, []Check, Rollback) {
			pre := []Check{{Type: "network.has_default_route", Equals: "true"}}
			verify := []Check{{Type: "network.has_default_route", Equals: "true", WithinSeconds: 20}}
			rb := Rollback{Kind: "automatic", Reason: "netplan try reverts automatically if the change is not confirmed within the timeout."}
			return pre, verify, rb
		},
	},
}

// Kinds returns the catalog kinds in sorted order, for help text and tests.
func Kinds() []string {
	ks := make([]string, 0, len(Catalog))
	for k := range Catalog {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
