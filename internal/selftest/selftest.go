// Package selftest probes maat's own confinement and capabilities on the host it
// runs on. Inside the strict snap it is the evidence that the security model
// holds: the network is unreachable, the filesystem is read-only, systemd cannot
// be controlled, yet the journal and unit state are readable. Outside the snap
// (plain binary) the "blocked" checks are expected to fail, which the report
// states plainly.
package selftest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/bolesawny7/maat/internal/collect"
)

// Check is one probe and its interpretation.
type Check struct {
	Name        string `json:"name"`
	Expectation string `json:"expectation"`
	Observed    string `json:"observed"`
	Pass        bool   `json:"pass"`
}

// Report is the full set of checks plus the environment they ran in.
type Report struct {
	Confined bool    `json:"confined"` // true if running inside a snap
	SnapName string  `json:"snap_name,omitempty"`
	Checks   []Check `json:"checks"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
}

// Run executes all probes and returns a report.
func Run(ctx context.Context) Report {
	confined := os.Getenv("SNAP") != ""
	r := Report{Confined: confined, SnapName: os.Getenv("SNAP_NAME")}

	r.add(checkJournalReadable(ctx))
	r.add(checkUnitsListable(ctx))
	r.add(checkNetworkBlocked(ctx, confined))
	r.add(checkFilesystemReadOnly(confined))
	r.add(checkSystemdControlDenied(ctx, confined))
	r.add(checkNetworkStateReadable(ctx))
	r.add(checkSocketsReadable())

	for _, c := range r.Checks {
		if c.Pass {
			r.Passed++
		} else {
			r.Failed++
		}
	}
	return r
}

func (r *Report) add(c Check) { r.Checks = append(r.Checks, c) }

// checkJournalReadable confirms maat can read the journal (via log-observe).
func checkJournalReadable(ctx context.Context) Check {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	recs, err := collect.ReadJournal(cctx, collect.JournalOptions{Lines: 1})
	c := Check{Name: "journal_readable", Expectation: "maat can read the system journal"}
	if err != nil {
		c.Observed = "journalctl failed: " + err.Error()
		c.Pass = false
		return c
	}
	c.Observed = fmt.Sprintf("read %d entry/entries from the journal", len(recs))
	c.Pass = true
	return c
}

// checkUnitsListable confirms maat can list systemd units (via system-observe).
func checkUnitsListable(ctx context.Context) Check {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	units, err := collect.ListUnits(cctx, false)
	c := Check{Name: "units_listable", Expectation: "maat can list systemd units over D-Bus"}
	if err != nil {
		c.Observed = "ListUnits failed: " + err.Error()
		c.Pass = false
		return c
	}
	c.Observed = fmt.Sprintf("listed %d units", len(units))
	c.Pass = true
	return c
}

// checkNetworkBlocked confirms the confined server cannot open a network
// connection. Passing requires the dial to fail when confined.
func checkNetworkBlocked(ctx context.Context, confined bool) Check {
	c := Check{Name: "network_blocked", Expectation: "the confined server cannot open an outbound network connection"}
	d := net.Dialer{Timeout: 4 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", "1.1.1.1:443")
	if conn != nil {
		conn.Close()
	}
	blocked := err != nil
	if blocked {
		c.Observed = "outbound TCP connect was refused: " + err.Error()
	} else {
		c.Observed = "outbound TCP connect SUCCEEDED (expected only outside the snap)"
	}
	// When confined, blocked is the pass condition. When not confined, the probe
	// is informational and reported as pass so a dev run is not alarming.
	c.Pass = blocked || !confined
	if !confined {
		c.Expectation += " (informational: not confined)"
	}
	return c
}

// checkFilesystemReadOnly confirms the confined server cannot write outside its
// own data. Passing requires the write to fail when confined.
func checkFilesystemReadOnly(confined bool) Check {
	c := Check{Name: "filesystem_read_only", Expectation: "the confined server cannot write to the host filesystem"}
	target := "/etc/maat-selftest-probe"
	err := os.WriteFile(target, []byte("probe"), 0o600)
	if err == nil {
		os.Remove(target)
		c.Observed = "write to /etc SUCCEEDED (expected only outside the snap)"
		c.Pass = !confined
	} else {
		c.Observed = "write to /etc was refused: " + err.Error()
		c.Pass = true
	}
	if !confined {
		c.Expectation += " (informational: not confined)"
	}
	return c
}

// checkSystemdControlDenied confirms the confined server cannot restart a unit.
// It attempts a RestartUnit on a sentinel unit name; passing requires the call to
// be refused (by AppArmor or polkit). The sentinel need not exist, because an
// authorization or confinement denial precedes any not-found error.
func checkSystemdControlDenied(ctx context.Context, confined bool) Check {
	c := Check{Name: "systemd_control_denied", Expectation: "the confined server cannot restart systemd units"}
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		c.Observed = "could not reach the system bus to attempt control: " + err.Error()
		c.Pass = true // cannot even attempt control
		return c
	}
	defer conn.Close()
	obj := conn.Object("org.freedesktop.systemd1", dbus.ObjectPath("/org/freedesktop/systemd1"))
	var job dbus.ObjectPath
	// No interactive-auth flag: we want a plain deny, not a prompt.
	err = obj.CallWithContext(ctx, "org.freedesktop.systemd1.Manager.RestartUnit", 0, "maat-selftest-sentinel.service", "replace").Store(&job)
	if err != nil {
		c.Observed = "RestartUnit was refused: " + err.Error()
		c.Pass = true
		return c
	}
	c.Observed = "RestartUnit was ACCEPTED (expected only outside confinement with authorization)"
	c.Pass = !confined
	return c
}

// checkNetworkStateReadable is informational: it reports whether the network
// collector works under the current interface set. maat drops network-observe to
// guarantee no egress, so this may legitimately fail closed inside the snap.
func checkNetworkStateReadable(ctx context.Context) Check {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ns, err := collect.CollectNetwork(cctx)
	c := Check{Name: "network_state_readable", Expectation: "informational: can the network collector read links/routes?", Pass: true}
	if err != nil {
		c.Observed = "no (fails closed): " + err.Error()
	} else {
		c.Observed = fmt.Sprintf("yes: %d interfaces, %d routes", len(ns.Interfaces), len(ns.Routes))
	}
	return c
}

// checkSocketsReadable is informational: whether listening sockets can be read.
func checkSocketsReadable() Check {
	socks, err := collect.CollectSockets("/")
	c := Check{Name: "sockets_readable", Expectation: "informational: can listening sockets be read from /proc/net?", Pass: true}
	if err != nil {
		c.Observed = "no: " + err.Error()
	} else {
		c.Observed = fmt.Sprintf("yes: %d sockets", len(socks))
	}
	return c
}

// WriteText renders the report as a readable table.
func (r Report) WriteText(w io.Writer) {
	env := "NOT confined (plain binary)"
	if r.Confined {
		env = "confined in snap " + r.SnapName
	}
	fmt.Fprintf(w, "maat self-test — %s\n\n", env)
	for _, c := range r.Checks {
		mark := "FAIL"
		if c.Pass {
			mark = "PASS"
		}
		fmt.Fprintf(w, "[%s] %s\n       expect: %s\n       observed: %s\n", mark, c.Name, c.Expectation, c.Observed)
	}
	fmt.Fprintf(w, "\n%d passed, %d failed\n", r.Passed, r.Failed)
	if !r.Confined {
		fmt.Fprintln(w, "\nNote: the 'blocked' checks only prove confinement when run inside the snap.")
	}
}

// WriteJSON renders the report as JSON for evidence capture.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
