package collect

import (
	"regexp"
	"sort"
	"time"

	"github.com/bolesawny7/maat/internal/obs"
)

// AppArmorDenial is one parsed AppArmor DENIED event. These are the clearest
// signal that confinement blocked something, and they are central to diagnosing
// a service that fails for no obvious reason, and to proving maat's own
// confinement works.
type AppArmorDenial struct {
	EvidenceID    string        `json:"evidence_id"`
	Operation     string        `json:"operation"`
	Profile       string        `json:"profile"`
	Name          obs.Untrusted `json:"name,omitempty"` // a path or bus name; any user can create paths
	Comm          obs.Untrusted `json:"comm,omitempty"` // a process can set its own name
	RequestedMask string        `json:"requested_mask,omitempty"`
	DeniedMask    string        `json:"denied_mask,omitempty"`
	Count         int           `json:"count"`
	Last          time.Time     `json:"last"`
}

var (
	apparmorDeniedRe = regexp.MustCompile(`apparmor="DENIED"`)
	kvRe             = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)
)

// ParseAppArmorDenial extracts a denial from a single log message, if present.
// It returns false for messages that are not AppArmor denials. File and network
// denials name the profile in profile=; D-Bus mediation denials use label=.
func ParseAppArmorDenial(msg string) (AppArmorDenial, bool) {
	if !apparmorDeniedRe.MatchString(msg) {
		return AppArmorDenial{}, false
	}
	d := AppArmorDenial{Count: 1}
	label := ""
	for _, m := range kvRe.FindAllStringSubmatch(msg, -1) {
		key := m[1]
		val := m[2]
		if val == "" {
			val = m[3]
		}
		switch key {
		case "operation":
			d.Operation = val
		case "profile":
			d.Profile = val
		case "label":
			label = val
		case "name":
			d.Name = obs.Untrusted(val)
		case "comm":
			d.Comm = obs.Untrusted(val)
		case "requested_mask", "requested":
			d.RequestedMask = val
		case "denied_mask", "denied":
			d.DeniedMask = val
		case "member":
			if d.Name == "" {
				d.Name = obs.Untrusted(val)
			}
		}
	}
	if d.Profile == "" {
		d.Profile = label
	}
	return d, true
}

// AppArmorDenials extracts and de-duplicates denials from a set of log records
// (typically kernel messages). Identical denials are collapsed with a count and
// the time of the latest, so a flood of the same block becomes one actionable row.
func AppArmorDenials(recs []obs.LogRecord) []AppArmorDenial {
	type key struct{ op, profile, name, mask string }
	index := map[key]*AppArmorDenial{}
	var order []key
	for _, r := range recs {
		d, ok := ParseAppArmorDenial(r.Message)
		if !ok {
			continue
		}
		k := key{d.Operation, d.Profile, string(d.Name), d.DeniedMask}
		if existing, ok := index[k]; ok {
			existing.Count++
			if r.Time.After(existing.Last) {
				existing.Last = r.Time
			}
			continue
		}
		d.Last = r.Time
		d.EvidenceID = obs.EvidenceID("apparmor", k.op, k.profile, k.name, k.mask)
		cp := d
		index[k] = &cp
		order = append(order, k)
	}
	out := make([]AppArmorDenial, 0, len(order))
	for _, k := range order {
		out = append(out, *index[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Profile < out[j].Profile
	})
	return out
}
