package collect

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/godbus/dbus/v5"

	"github.com/bolesawny7/maat/internal/obs"
)

// Unit is a systemd unit as the agent sees it. Description is untrusted because
// package maintainers (not the user) write it, and it reaches the model.
type Unit struct {
	EvidenceID  string        `json:"evidence_id"`
	Name        string        `json:"name"`
	Description obs.Untrusted `json:"description"`
	LoadState   string        `json:"load_state"`
	ActiveState string        `json:"active_state"`
	SubState    string        `json:"sub_state"`
}

// UnitStatus adds the service-level detail useful for diagnosing a failure.
type UnitStatus struct {
	Unit
	Result         string `json:"result,omitempty"`          // e.g. exit-code, signal, oom-kill
	ExecMainStatus int    `json:"exec_main_status"`          // last exit code of the main process
	NRestarts      uint32 `json:"n_restarts"`                //
	UnitFileState  string `json:"unit_file_state,omitempty"` // enabled, disabled, ...
}

// rawUnit matches the a(ssssssouso) tuple returned by Manager.ListUnits.
type rawUnit struct {
	Name        string
	Description string
	LoadState   string
	ActiveState string
	SubState    string
	Following   string
	Path        dbus.ObjectPath
	JobID       uint32
	JobType     string
	JobPath     dbus.ObjectPath
}

const (
	systemdDest      = "org.freedesktop.systemd1"
	systemdPath      = "/org/freedesktop/systemd1"
	systemdManager   = "org.freedesktop.systemd1.Manager"
	systemdUnitIface = "org.freedesktop.systemd1.Unit"
	systemdSvcIface  = "org.freedesktop.systemd1.Service"
	propsGetAll      = "org.freedesktop.DBus.Properties.GetAll"
)

// UnitEvidenceID is the evidence ID for a unit's state, shared by every tool
// that reports it.
func UnitEvidenceID(name string) string { return obs.EvidenceID("unit", name) }

// mapRawUnits converts the ListUnits tuple into Unit values. If onlyFailed is
// set, it keeps only units whose active state is "failed". The conversion is
// pure so it can be unit-tested without a live bus.
func mapRawUnits(raw []rawUnit, onlyFailed bool) []Unit {
	var out []Unit
	for _, r := range raw {
		if onlyFailed && r.ActiveState != "failed" {
			continue
		}
		out = append(out, Unit{
			EvidenceID:  UnitEvidenceID(r.Name),
			Name:        r.Name,
			Description: obs.Untrusted(r.Description),
			LoadState:   r.LoadState,
			ActiveState: r.ActiveState,
			SubState:    r.SubState,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// systemBus opens a private connection to the system bus. The caller must close
// it. A private connection avoids sharing global state across collectors.
func systemBus(ctx context.Context) (*dbus.Conn, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("connect system bus: %w", err)
	}
	return conn, nil
}

// ListUnits returns all loaded units. With onlyFailed, it returns only failed
// ones. It uses the systemd Manager's ListUnits method, which the system-observe
// interface permits.
func ListUnits(ctx context.Context, onlyFailed bool) ([]Unit, error) {
	conn, err := systemBus(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var raw []rawUnit
	obj := conn.Object(systemdDest, dbus.ObjectPath(systemdPath))
	if err := obj.CallWithContext(ctx, systemdManager+".ListUnits", 0).Store(&raw); err != nil {
		return nil, fmt.Errorf("ListUnits: %w", err)
	}
	return mapRawUnits(raw, onlyFailed), nil
}

// ErrUnitNotLoaded is returned (wrapped) by GetUnitStatus when systemd has no
// such unit loaded. systemctl reports a unit in that state as "inactive"; it is
// also what a failed unit becomes after reset-failed lets systemd unload it.
var ErrUnitNotLoaded = errors.New("unit not loaded")

// DBusErrorName returns the D-Bus error name carried by err, or "".
func DBusErrorName(err error) string {
	var de dbus.Error
	if errors.As(err, &de) {
		return de.Name
	}
	return ""
}

// GetUnitStatus returns the detailed status of one unit. It resolves the unit's
// object path with GetUnit, then reads the Unit and (for services) Service
// property sets, both permitted by system-observe.
func GetUnitStatus(ctx context.Context, name string) (UnitStatus, error) {
	conn, err := systemBus(ctx)
	if err != nil {
		return UnitStatus{}, err
	}
	defer conn.Close()

	mgr := conn.Object(systemdDest, dbus.ObjectPath(systemdPath))
	var path dbus.ObjectPath
	if err := mgr.CallWithContext(ctx, systemdManager+".GetUnit", 0, name).Store(&path); err != nil {
		if DBusErrorName(err) == "org.freedesktop.systemd1.NoSuchUnit" {
			return UnitStatus{}, fmt.Errorf("%w: %s", ErrUnitNotLoaded, name)
		}
		return UnitStatus{}, fmt.Errorf("GetUnit %q: %w", name, err)
	}
	unitObj := conn.Object(systemdDest, path)

	unitProps, err := getAllProps(ctx, unitObj, systemdUnitIface)
	if err != nil {
		return UnitStatus{}, err
	}
	st := UnitStatus{Unit: Unit{EvidenceID: UnitEvidenceID(name), Name: name}}
	st.Description = obs.Untrusted(variantString(unitProps["Description"]))
	st.LoadState = variantString(unitProps["LoadState"])
	st.ActiveState = variantString(unitProps["ActiveState"])
	st.SubState = variantString(unitProps["SubState"])
	st.UnitFileState = variantString(unitProps["UnitFileState"])

	// Service-specific properties; ignored if the unit is not a service.
	if svcProps, err := getAllProps(ctx, unitObj, systemdSvcIface); err == nil {
		st.Result = variantString(svcProps["Result"])
		st.ExecMainStatus = int(variantInt(svcProps["ExecMainStatus"]))
		st.NRestarts = uint32(variantInt(svcProps["NRestarts"]))
	}
	return st, nil
}

func getAllProps(ctx context.Context, obj dbus.BusObject, iface string) (map[string]dbus.Variant, error) {
	var props map[string]dbus.Variant
	if err := obj.CallWithContext(ctx, propsGetAll, 0, iface).Store(&props); err != nil {
		return nil, fmt.Errorf("GetAll %s: %w", iface, err)
	}
	return props, nil
}

func variantString(v dbus.Variant) string {
	if v.Signature().String() == "" {
		return ""
	}
	if s, ok := v.Value().(string); ok {
		return s
	}
	return ""
}

func variantInt(v dbus.Variant) int64 {
	switch n := v.Value().(type) {
	case int32:
		return int64(n)
	case uint32:
		return int64(n)
	case int64:
		return n
	case uint64:
		return int64(n)
	default:
		return 0
	}
}
