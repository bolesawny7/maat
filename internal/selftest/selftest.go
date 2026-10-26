// Package selftest probes maat's own confinement and capabilities on the host it
// runs on. Inside the strict snap it is the evidence that the security model
// holds: the network is unreachable, the user's files can't be written, systemd
// can't be controlled, yet the journal and unit state are readable.
//
// Each "blocked" probe passes only when the refusal comes from confinement, not
// from something else that happens to refuse too. A non-root user can't write
// /etc and isn't allowed to restart units by polkit anyway, so probing those
// would prove nothing; the probes target what the user could do without the
// snap. Outside the snap (plain binary) those probes are informational.
package selftest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/bolesawny7/maat/internal/collect"
)

// Status is the outcome of one check.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"
	Info Status = "info" // reported, not counted
)

// Check is one probe and its interpretation.
type Check struct {
	Name        string `json:"name"`
	Expectation string `json:"expectation"`
	Observed    string `json:"observed"`
	Status      Status `json:"status"`
}

// Report is the full set of checks plus the environment they ran in.
type Report struct {
	Confined bool    `json:"confined"` // true if running inside a snap
	SnapName string  `json:"snap_name,omitempty"`
	Checks   []Check `json:"checks"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Info     int     `json:"info"`
}

// Run executes all probes and returns a report.
func Run(ctx context.Context) Report {
	confined := os.Getenv("SNAP") != ""
	r := Report{Confined: confined, SnapName: os.Getenv("SNAP_NAME")}

	r.add(checkNotRoot())
	r.add(checkJournalReadable(ctx))
	r.add(checkUnitsListable(ctx))
	r.add(checkNetworkBlocked(confined))
	r.add(checkHomeWriteBlocked(confined))
	r.add(checkSystemdControlDenied(ctx, confined))
	r.add(checkDiskView())
	r.add(checkNetworkState(ctx))
	r.add(checkSockets())

	for _, c := range r.Checks {
		switch c.Status {
		case Pass:
			r.Passed++
		case Fail:
			r.Failed++
		default:
			r.Info++
		}
	}
	return r
}

func (r *Report) add(c Check) { r.Checks = append(r.Checks, c) }

func status(ok bool) Status {
	if ok {
		return Pass
	}
	return Fail
}

// checkNotRoot: as root, the confinement still applies, but ordinary file
// permissions no longer keep log-observe's two writable settings out of reach.
func checkNotRoot() Check {
	c := Check{Name: "not_root", Expectation: "maat runs as a normal user, so file permissions back up the confinement"}
	if os.Geteuid() == 0 {
		c.Observed = "running as root"
		c.Status = Fail
		return c
	}
	c.Observed = fmt.Sprintf("running as uid %d", os.Geteuid())
	c.Status = Pass
	return c
}

// checkJournalReadable confirms maat can read the journal (via log-observe).
func checkJournalReadable(ctx context.Context) Check {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	recs, err := collect.ReadJournal(cctx, collect.JournalOptions{Lines: 1})
	c := Check{Name: "journal_readable", Expectation: "maat can read the system journal"}
	if err != nil {
		c.Observed = "journalctl failed: " + err.Error()
		c.Status = Fail
		return c
	}
	c.Observed = fmt.Sprintf("read %d entry/entries from the journal", len(recs))
	c.Status = Pass
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
		c.Status = Fail
		return c
	}
	c.Observed = fmt.Sprintf("listed %d units", len(units))
	c.Status = Pass
	return c
}

// isPolicyDenial reports whether err is the EACCES or EPERM that AppArmor and
// seccomp return, as opposed to a network-level failure.
func isPolicyDenial(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

// checkNetworkBlocked tries to create internet sockets. It dials closed loopback
// ports, so nothing leaves the machine even when the probe is not blocked. Only
// a permission error from creating the socket proves confinement: "connection
// refused" or a timeout means the socket was created and the network is open.
func checkNetworkBlocked(confined bool) Check {
	c := Check{Name: "network_blocked", Expectation: "the server cannot create internet sockets (TCP or UDP, IPv4 or IPv6)"}
	var blocked, open []string
	for _, p := range []struct{ network, addr string }{
		{"tcp4", "127.0.0.1:9"}, {"tcp6", "[::1]:9"}, {"udp4", "127.0.0.1:9"},
	} {
		conn, err := net.DialTimeout(p.network, p.addr, time.Second)
		if conn != nil {
			conn.Close()
		}
		if err != nil && isPolicyDenial(err) {
			blocked = append(blocked, p.network+": "+err.Error())
		} else {
			res := "socket created"
			if err != nil {
				res += " (" + err.Error() + ")"
			}
			open = append(open, p.network+": "+res)
		}
	}
	switch {
	case len(open) == 0:
		c.Observed = "refused by policy: " + strings.Join(blocked, "; ")
		c.Status = Pass
	case confined:
		c.Observed = "NOT blocked: " + strings.Join(open, "; ")
		c.Status = Fail
	default:
		c.Observed = "not confined: " + strings.Join(open, "; ")
		c.Status = Info
	}
	return c
}

// checkHomeWriteBlocked tries to write in the user's real home directory, which
// the user may write but a strict snap without the home interface may not.
func checkHomeWriteBlocked(confined bool) Check {
	c := Check{Name: "home_write_blocked", Expectation: "the server cannot write the user's files"}
	home := os.Getenv("SNAP_REAL_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			c.Observed = "no home directory to probe: " + err.Error()
			c.Status = Info
			return c
		}
		home = h
	}
	target := filepath.Join(home, fmt.Sprintf(".maat-selftest-probe-%d", os.Getpid()))
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
		os.Remove(target)
		if confined {
			c.Observed = "write to " + home + " SUCCEEDED"
			c.Status = Fail
		} else {
			c.Observed = "not confined: write to " + home + " succeeded"
			c.Status = Info
		}
		return c
	}
	if isPolicyDenial(err) {
		c.Observed = "write to " + home + " was refused: " + err.Error()
		c.Status = Pass
		return c
	}
	c.Observed = "write to " + home + " failed for a reason that proves nothing about confinement: " + err.Error()
	c.Status = Info
	return c
}

// checkSystemdControlDenied attempts RestartUnit on a sentinel unit name without
// the interactive-authorization flag. Confinement passes only when the D-Bus
// daemon refuses the message on AppArmor's behalf; a polkit refusal would mean
// AppArmor let the call through.
func checkSystemdControlDenied(ctx context.Context, confined bool) Check {
	c := Check{Name: "systemd_control_denied", Expectation: "AppArmor stops the server from restarting systemd units"}
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		c.Observed = "could not reach the system bus: " + err.Error()
		c.Status = Info
		return c
	}
	defer conn.Close()
	obj := conn.Object("org.freedesktop.systemd1", dbus.ObjectPath("/org/freedesktop/systemd1"))
	var job dbus.ObjectPath
	err = obj.CallWithContext(ctx, "org.freedesktop.systemd1.Manager.RestartUnit", 0, "maat-selftest-sentinel.service", "replace").Store(&job)
	name := collect.DBusErrorName(err)
	byAppArmor := name == "org.freedesktop.DBus.Error.AccessDenied" && err != nil && strings.Contains(err.Error(), "AppArmor")
	switch {
	case byAppArmor:
		c.Observed = "refused by AppArmor: " + err.Error()
		c.Status = Pass
	case !confined:
		if err != nil {
			c.Observed = "not confined; refused by " + name + ": " + err.Error()
		} else {
			c.Observed = "not confined; RestartUnit was accepted"
		}
		c.Status = Info
	case err != nil:
		c.Observed = "AppArmor let the call through; it was refused by something else (" + name + "): " + err.Error()
		c.Status = Fail
	default:
		c.Observed = "RestartUnit was ACCEPTED"
		c.Status = Fail
	}
	return c
}

// checkDiskView reports whether the host's root filesystem is visible.
func checkDiskView() Check {
	c := Check{Name: "disk_view", Expectation: "informational: disk_usage sees the host's filesystems", Status: Info}
	disks, err := collect.CollectDisks("/")
	if err != nil {
		c.Observed = "no: " + err.Error()
		return c
	}
	for _, d := range disks {
		if d.MountPoint == "/" {
			c.Observed = fmt.Sprintf("yes: %d filesystems; / is %s on %s, %.1f%% used", len(disks), d.FSType, d.Device, d.UsedPct)
			return c
		}
	}
	c.Observed = fmt.Sprintf("%d filesystems, but the host root (/) is not among them", len(disks))
	return c
}

// checkNetworkState reports how much of the network the collector can see.
func checkNetworkState(ctx context.Context) Check {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ns, err := collect.CollectNetwork(cctx)
	c := Check{Name: "network_state", Expectation: "informational: what network_state can read", Status: Info}
	switch {
	case err != nil:
		c.Observed = "nothing: " + err.Error()
	case ns.Limited:
		c.Observed = fmt.Sprintf("interface names and counters only (%d interfaces); addresses and routes need network-observe, which maat does not use", len(ns.Interfaces))
	default:
		c.Observed = fmt.Sprintf("full: %d interfaces, %d routes", len(ns.Interfaces), len(ns.Routes))
	}
	return c
}

// checkSockets reports whether listening sockets can be read.
func checkSockets() Check {
	c := Check{Name: "listening_sockets", Expectation: "informational: whether listening sockets can be read from /proc/net", Status: Info}
	socks, err := collect.CollectSockets("/")
	if err != nil {
		c.Observed = "no (the tool fails closed with an explanation)"
		return c
	}
	c.Observed = fmt.Sprintf("yes: %d sockets", len(socks))
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
		fmt.Fprintf(w, "[%s] %s\n       expect: %s\n       observed: %s\n", strings.ToUpper(string(c.Status)), c.Name, c.Expectation, c.Observed)
	}
	fmt.Fprintf(w, "\n%d passed, %d failed, %d informational\n", r.Passed, r.Failed, r.Info)
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
