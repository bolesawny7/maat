package collect

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bolesawny7/maat/internal/obs"
)

// Process is one process, with a CPU percentage measured over a short sampling
// interval.
type Process struct {
	PID     int           `json:"pid"`
	Comm    string        `json:"comm"`
	State   string        `json:"state"`
	RSSMB   int64         `json:"rss_mb"`
	CPUPct  float64       `json:"cpu_pct"`
	Cmdline obs.Untrusted `json:"cmdline"`
}

// statSample is the subset of /proc/<pid>/stat we need to compute CPU usage.
type statSample struct {
	comm  string
	state string
	utime uint64
	stime uint64
}

// parseStat parses /proc/<pid>/stat. The comm field is parenthesized and may
// contain spaces and parentheses, so it is located by the last ')' rather than
// by splitting on whitespace.
func parseStat(data string) (statSample, bool) {
	open := strings.IndexByte(data, '(')
	closeIdx := strings.LastIndexByte(data, ')')
	if open < 0 || closeIdx < 0 || closeIdx < open {
		return statSample{}, false
	}
	comm := data[open+1 : closeIdx]
	rest := strings.Fields(data[closeIdx+1:])
	// rest[0] is state; utime and stime are fields 14 and 15 of the whole line,
	// which are rest[11] and rest[12] after the comm.
	if len(rest) < 13 {
		return statSample{}, false
	}
	utime, _ := strconv.ParseUint(rest[11], 10, 64)
	stime, _ := strconv.ParseUint(rest[12], 10, 64)
	return statSample{comm: comm, state: rest[0], utime: utime, stime: stime}, true
}

// parseStatusRSS extracts VmRSS (in kB) from /proc/<pid>/status.
func parseStatusRSS(data string) int64 {
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				if v, err := strconv.ParseInt(f[1], 10, 64); err == nil {
					return v
				}
			}
		}
	}
	return 0
}

// cleanCmdline turns the NUL-separated /proc/<pid>/cmdline into a space-joined
// string. It is returned as Untrusted because arguments can contain
// attacker-influenced data.
func cleanCmdline(data string) obs.Untrusted {
	parts := strings.Split(strings.TrimRight(data, "\x00"), "\x00")
	return obs.Untrusted(strings.TrimSpace(strings.Join(parts, " ")))
}

// stateName maps the single-letter process state to a word.
func stateName(s string) string {
	switch s {
	case "R":
		return "running"
	case "S":
		return "sleeping"
	case "D":
		return "disk-wait"
	case "Z":
		return "zombie"
	case "T":
		return "stopped"
	case "I":
		return "idle"
	default:
		return s
	}
}

// TopProcesses returns the processes using the most CPU and memory, sampled over
// the given interval. root is the proc parent ("/" for live). The CPU figure is
// the share of one core over the interval, summed as the kernel reports it.
func TopProcesses(root string, interval time.Duration, limit int) ([]Process, error) {
	procDir := filepath.Join(root, "proc")
	clkTck := clockTicks()

	first, err := sampleAll(procDir)
	if err != nil {
		return nil, err
	}
	time.Sleep(interval)
	second, err := sampleAll(procDir)
	if err != nil {
		return nil, err
	}

	secs := interval.Seconds()
	var procs []Process
	for pid, s2 := range second {
		s1, ok := first[pid]
		cpu := 0.0
		if ok && secs > 0 {
			dt := float64((s2.utime + s2.stime) - (s1.utime + s1.stime))
			cpu = round1(dt / float64(clkTck) / secs * 100)
		}
		rss := parseStatusRSS(readFile(filepath.Join(procDir, strconv.Itoa(pid), "status")))
		procs = append(procs, Process{
			PID:     pid,
			Comm:    s2.comm,
			State:   stateName(s2.state),
			RSSMB:   rss / 1024,
			CPUPct:  cpu,
			Cmdline: cleanCmdline(readFile(filepath.Join(procDir, strconv.Itoa(pid), "cmdline"))),
		})
	}
	sort.SliceStable(procs, func(i, j int) bool {
		if procs[i].CPUPct != procs[j].CPUPct {
			return procs[i].CPUPct > procs[j].CPUPct
		}
		if procs[i].RSSMB != procs[j].RSSMB {
			return procs[i].RSSMB > procs[j].RSSMB
		}
		return procs[i].PID < procs[j].PID
	})
	if limit > 0 && len(procs) > limit {
		procs = procs[:limit]
	}
	return procs, nil
}

func sampleAll(procDir string) (map[int]statSample, error) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, err
	}
	out := map[int]statSample{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		s, ok := parseStat(readFile(filepath.Join(procDir, e.Name(), "stat")))
		if !ok {
			continue
		}
		out[pid] = s
	}
	return out, nil
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// clockTicks returns the kernel's USER_HZ, the unit of the utime/stime fields in
// /proc/<pid>/stat. On Linux this is 100 on every mainstream configuration
// (CONFIG_HZ affects the scheduler tick, not USER_HZ, which the kernel fixes at
// 100 for the stat interface). Reading it exactly would require cgo's
// sysconf(_SC_CLK_TCK); the fixed value is correct for Ubuntu on amd64 and
// arm64, which is what maat targets.
func clockTicks() float64 { return 100 }
