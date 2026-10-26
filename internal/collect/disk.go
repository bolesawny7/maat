package collect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Mount is one entry from /proc/self/mountinfo, reduced to what disk reporting
// needs.
type Mount struct {
	Device     string
	MountPoint string
	FSType     string
}

// DiskUsage reports space and inode usage for one mounted filesystem.
type DiskUsage struct {
	MountPoint    string  `json:"mount_point"`
	Device        string  `json:"device"`
	FSType        string  `json:"fs_type"`
	TotalMB       int64   `json:"total_mb"`
	UsedMB        int64   `json:"used_mb"`
	AvailMB       int64   `json:"avail_mb"`
	UsedPct       float64 `json:"used_pct"`
	InodesTotal   int64   `json:"inodes_total"`
	InodesUsed    int64   `json:"inodes_used"`
	InodesUsedPct float64 `json:"inodes_used_pct"`
}

// pseudoFS are filesystem types that never run out of space in a way worth
// reporting, or that represent read-only snap mounts (squashfs). Skipping them
// keeps the disk view focused on real storage.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"devtmpfs": true, "devpts": true, "mqueue": true, "debugfs": true,
	"tracefs": true, "bpf": true, "pstore": true, "securityfs": true,
	"configfs": true, "fusectl": true, "autofs": true, "binfmt_misc": true,
	"hugetlbfs": true, "nsfs": true, "efivarfs": true, "squashfs": true,
	"overlay": true, "ramfs": true, "fuse.snapfuse": true,
}

// ParseMountinfo parses the contents of /proc/self/mountinfo into mounts,
// dropping pseudo and snap filesystems. The format is documented in
// proc(5): fields up to " - " are the mount, then fstype, source, superopts.
func ParseMountinfo(data string) []Mount {
	var mounts []Mount
	seen := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		// Find the separator " - ".
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		mountPoint := unescapeOctal(fields[4])
		fsType := fields[sep+1]
		device := fields[sep+2]
		if pseudoFS[fsType] {
			continue
		}
		if seen[mountPoint] {
			continue
		}
		seen[mountPoint] = true
		mounts = append(mounts, Mount{Device: device, MountPoint: mountPoint, FSType: fsType})
	}
	return mounts
}

// unescapeOctal decodes the \040-style octal escapes that mountinfo uses for
// spaces and other special characters in paths.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, ok := parseOctal3(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseOctal3(s string) (byte, bool) {
	if len(s) != 3 {
		return 0, false
	}
	v := 0
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	return byte(v), true
}

// CollectDisks reads mounts from root's mountinfo and runs statfs on each. Use
// "/" for the live system. Mounts that cannot be statfs'd are skipped.
func CollectDisks(root string) ([]DiskUsage, error) {
	data, err := os.ReadFile(filepath.Join(root, "proc/self/mountinfo"))
	if err != nil {
		return nil, err
	}
	mounts := ParseMountinfo(string(data))
	var out []DiskUsage
	for _, m := range mounts {
		var st unix.Statfs_t
		if err := unix.Statfs(m.MountPoint, &st); err != nil {
			continue
		}
		out = append(out, diskUsageFromStatfs(m, &st))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UsedPct > out[j].UsedPct })
	return out, nil
}

// diskUsageFromStatfs converts a statfs result into a DiskUsage. It is separated
// so the arithmetic can be unit-tested with synthetic values.
func diskUsageFromStatfs(m Mount, st *unix.Statfs_t) DiskUsage {
	bsize := int64(st.Bsize)
	total := int64(st.Blocks) * bsize
	avail := int64(st.Bavail) * bsize
	free := int64(st.Bfree) * bsize
	used := total - free
	du := DiskUsage{
		MountPoint: m.MountPoint,
		Device:     m.Device,
		FSType:     m.FSType,
		TotalMB:    total / (1024 * 1024),
		UsedMB:     used / (1024 * 1024),
		AvailMB:    avail / (1024 * 1024),
	}
	if total > 0 {
		du.UsedPct = round1(float64(used) / float64(total) * 100)
	}
	du.InodesTotal = int64(st.Files)
	if st.Files > 0 {
		iused := int64(st.Files) - int64(st.Ffree)
		du.InodesUsed = iused
		du.InodesUsedPct = round1(float64(iused) / float64(st.Files) * 100)
	}
	return du
}
