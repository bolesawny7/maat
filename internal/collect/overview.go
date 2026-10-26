package collect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Overview is a one-glance picture of a host's health.
type Overview struct {
	OSRelease     string    `json:"os_release"`
	KernelRelease string    `json:"kernel_release"`
	Hostname      string    `json:"hostname"`
	Uptime        string    `json:"uptime"`
	Load1         float64   `json:"load1"`
	Load5         float64   `json:"load5"`
	Load15        float64   `json:"load15"`
	MemTotalMB    int64     `json:"mem_total_mb"`
	MemAvailMB    int64     `json:"mem_available_mb"`
	MemUsedPct    float64   `json:"mem_used_pct"`
	SwapTotalMB   int64     `json:"swap_total_mb"`
	SwapUsedMB    int64     `json:"swap_used_mb"`
	FailedUnits   int       `json:"failed_units"`
	CollectedAt   time.Time `json:"collected_at"`
}

// hostPrefixes lists where to look for host files. When maat runs inside a
// strict snap, the system-observe interface bind-mounts the host's os-release
// under /var/lib/snapd/hostfs; /proc is already the host's. We prefer the hostfs
// copy so a confined run reports the host OS, not the snap base.
var hostfsPrefix = "/var/lib/snapd/hostfs"

// CollectOverview gathers the overview from a filesystem root (use "/" for the
// live system). failedUnits is supplied by the caller, who already has the unit
// list, to avoid collecting it twice.
func CollectOverview(root string, failedUnits int) Overview {
	ov := Overview{FailedUnits: failedUnits, CollectedAt: time.Now().UTC()}

	ov.OSRelease = prettyName(readFirst(root,
		filepath.Join(hostfsPrefix, "etc/os-release"),
		"etc/os-release",
	))
	ov.KernelRelease = strings.TrimSpace(readFirst(root, "proc/sys/kernel/osrelease"))
	ov.Hostname = strings.TrimSpace(readFirst(root, "proc/sys/kernel/hostname"))

	if up := readFirst(root, "proc/uptime"); up != "" {
		ov.Uptime = formatUptime(up)
	}
	ov.Load1, ov.Load5, ov.Load15 = parseLoadavg(readFirst(root, "proc/loadavg"))

	mem := parseMeminfo(readFirst(root, "proc/meminfo"))
	ov.MemTotalMB = mem["MemTotal"] / 1024
	ov.MemAvailMB = mem["MemAvailable"] / 1024
	if mem["MemTotal"] > 0 {
		used := mem["MemTotal"] - mem["MemAvailable"]
		ov.MemUsedPct = round1(float64(used) / float64(mem["MemTotal"]) * 100)
	}
	ov.SwapTotalMB = mem["SwapTotal"] / 1024
	ov.SwapUsedMB = (mem["SwapTotal"] - mem["SwapFree"]) / 1024
	return ov
}

// readFirst returns the trimmed contents of the first readable file among the
// candidates, joined to root. It returns "" if none can be read.
func readFirst(root string, candidates ...string) string {
	for _, c := range candidates {
		if b, err := os.ReadFile(filepath.Join(root, c)); err == nil {
			return string(b)
		}
	}
	return ""
}

// prettyName extracts PRETTY_NAME from os-release contents, falling back to the
// whole first line.
func prettyName(osRelease string) string {
	for _, line := range strings.Split(osRelease, "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			v := strings.TrimPrefix(line, "PRETTY_NAME=")
			return strings.Trim(v, `"`)
		}
	}
	return strings.TrimSpace(firstLine(osRelease))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// parseLoadavg parses the first three fields of /proc/loadavg.
func parseLoadavg(s string) (l1, l5, l15 float64) {
	f := strings.Fields(s)
	if len(f) >= 3 {
		l1, _ = strconv.ParseFloat(f[0], 64)
		l5, _ = strconv.ParseFloat(f[1], 64)
		l15, _ = strconv.ParseFloat(f[2], 64)
	}
	return
}

// parseMeminfo parses /proc/meminfo into a map of field name to kibibytes.
func parseMeminfo(s string) map[string]int64 {
	out := map[string]int64{}
	for _, line := range strings.Split(s, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSuffix(parts[0], ":")
		if v, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			out[name] = v
		}
	}
	return out
}

// formatUptime turns the first field of /proc/uptime (seconds) into a short
// human string like "3d 4h 12m".
func formatUptime(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	secs, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return ""
	}
	d := time.Duration(secs) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return strconv.Itoa(days) + "d " + strconv.Itoa(hours) + "h " + strconv.Itoa(mins) + "m"
	case hours > 0:
		return strconv.Itoa(hours) + "h " + strconv.Itoa(mins) + "m"
	default:
		return strconv.Itoa(mins) + "m"
	}
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}
