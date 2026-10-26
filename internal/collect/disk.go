package collect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/bolesawny7/maat/internal/obs"
)

// Mount is one entry from /proc/self/mountinfo, reduced to what disk reporting
// needs.
type Mount struct {
	DevID      string // major:minor; bind mounts of one filesystem share it
	Root       string // which directory of the filesystem is mounted here
	MountPoint string
	FSType     string
	Device     string
	ReadOnly   bool
}

// DiskUsage reports space and inode usage for one mounted filesystem.
type DiskUsage struct {
	EvidenceID    string  `json:"evidence_id"`
	MountPoint    string  `json:"mount_point"`
	Device        string  `json:"device"`
	FSType        string  `json:"fs_type"`
	ReadOnly      bool    `json:"read_only,omitempty"`
	TotalMB       int64   `json:"total_mb"`
	UsedMB        int64   `json:"used_mb"`
	AvailMB       int64   `json:"avail_mb"`
	UsedPct       float64 `json:"used_pct"`
	InodesTotal   int64   `json:"inodes_total"`
	InodesUsed    int64   `json:"inodes_used"`
	InodesUsedPct float64 `json:"inodes_used_pct"`
}

// skipFS are filesystem types that never run out of space in a way worth
// reporting: kernel pseudo filesystems, read-only snap and container images
// (squashfs, overlay), and optical media, which are always 100% "used" by design
// and would otherwise top the list as if a disk were full.
var skipFS = map[string]bool{
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"devtmpfs": true, "devpts": true, "mqueue": true, "debugfs": true,
	"tracefs": true, "bpf": true, "pstore": true, "securityfs": true,
	"configfs": true, "fusectl": true, "autofs": true, "binfmt_misc": true,
	"hugetlbfs": true, "nsfs": true, "efivarfs": true, "squashfs": true,
	"overlay": true, "ramfs": true, "fuse.snapfuse": true,
	"iso9660": true, "udf": true,
}

// ParseMountinfo parses the contents of /proc/self/mountinfo into mounts,
// dropping pseudo, image and optical filesystems. The format is documented in
// proc(5): mount ID, parent ID, major:minor, root, mount point, options,
// optional fields, " - ", fstype, source, superblock options.
func ParseMountinfo(data string) []Mount {
	var mounts []Mount
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		fsType := fields[sep+1]
		if skipFS[fsType] {
			continue
		}
		mounts = append(mounts, Mount{
			DevID:      fields[2],
			Root:       unescapeOctal(fields[3]),
			MountPoint: unescapeOctal(fields[4]),
			FSType:     fsType,
			Device:     unescapeOctal(fields[sep+2]),
			ReadOnly:   hasOption(fields[5], "ro"),
		})
	}
	return mounts
}

func hasOption(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

// filesystem is one filesystem to report: where to statfs it in this process's
// mount namespace, and the host path to show.
type filesystem struct {
	Mount
	Display string
}

// selectFilesystems reduces a mount table to one entry per filesystem, named by
// its host path.
//
// Inside a strict snap, /proc/self/mountinfo describes the snap's own mount
// namespace: "/" is a tmpfs, the base snap is mounted over /usr, and host
// directories such as /etc and /home are bind mounts of the host's root
// filesystem. The host's real view is under /var/lib/snapd/hostfs, so mounts
// there are preferred and their paths translated back ("/var/lib/snapd/hostfs"
// is the host's "/"). Bind mounts of the same filesystem collapse into one row.
func selectFilesystems(mounts []Mount) []filesystem {
	inSnap := false
	for _, m := range mounts {
		if m.MountPoint == hostfsPrefix {
			inSnap = true
			break
		}
	}
	type candidate struct {
		filesystem
		fromHost bool
	}
	rank := func(c candidate) int {
		switch {
		case c.fromHost && c.Root == "/":
			return 0
		case c.fromHost:
			return 1
		case c.Root == "/":
			return 2
		default:
			return 3
		}
	}
	better := func(a, b candidate) bool {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		if len(a.Display) != len(b.Display) {
			return len(a.Display) < len(b.Display)
		}
		return a.Display < b.Display
	}

	byDev := map[string]candidate{}
	for _, m := range mounts {
		c := candidate{filesystem: filesystem{Mount: m, Display: m.MountPoint}}
		if inSnap {
			switch {
			case m.MountPoint == hostfsPrefix:
				c.Display, c.fromHost = "/", true
			case strings.HasPrefix(m.MountPoint, hostfsPrefix+"/"):
				c.Display, c.fromHost = strings.TrimPrefix(m.MountPoint, hostfsPrefix), true
			}
		}
		if cur, ok := byDev[m.DevID]; !ok || better(c, cur) {
			byDev[m.DevID] = c
		}
	}
	// Two filesystems can now claim the same path: the snap's private "/" and
	// the host's. The host's wins.
	byPath := map[string]candidate{}
	for _, c := range byDev {
		if cur, ok := byPath[c.Display]; !ok || better(c, cur) {
			byPath[c.Display] = c
		}
	}
	out := make([]filesystem, 0, len(byPath))
	for _, c := range byPath {
		out = append(out, c.filesystem)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Display < out[j].Display })
	return out
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

// CollectDisks reports every real filesystem, as the host sees it, with
// writable filesystems first and the fullest first. Use "/" for the live system.
// Filesystems that cannot be statfs'd are skipped.
func CollectDisks(root string) ([]DiskUsage, error) {
	data, err := os.ReadFile(filepath.Join(root, "proc/self/mountinfo"))
	if err != nil {
		return nil, err
	}
	var out []DiskUsage
	for _, fs := range selectFilesystems(ParseMountinfo(string(data))) {
		var st unix.Statfs_t
		if err := unix.Statfs(fs.MountPoint, &st); err != nil {
			continue
		}
		out = append(out, diskUsageFromStatfs(fs, &st))
	}
	sortDisks(out)
	return out, nil
}

func sortDisks(ds []DiskUsage) {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].ReadOnly != ds[j].ReadOnly {
			return !ds[i].ReadOnly
		}
		pi := max(ds[i].UsedPct, ds[i].InodesUsedPct)
		pj := max(ds[j].UsedPct, ds[j].InodesUsedPct)
		if pi != pj {
			return pi > pj
		}
		return ds[i].MountPoint < ds[j].MountPoint
	})
}

// diskUsageFromStatfs converts a statfs result into a DiskUsage. It is separated
// so the arithmetic can be unit-tested with synthetic values.
func diskUsageFromStatfs(fs filesystem, st *unix.Statfs_t) DiskUsage {
	bsize := int64(st.Bsize)
	total := int64(st.Blocks) * bsize
	avail := int64(st.Bavail) * bsize
	free := int64(st.Bfree) * bsize
	used := total - free
	du := DiskUsage{
		EvidenceID: obs.EvidenceID("disk", fs.Display),
		MountPoint: fs.Display,
		Device:     fs.Device,
		FSType:     fs.FSType,
		ReadOnly:   fs.ReadOnly,
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
