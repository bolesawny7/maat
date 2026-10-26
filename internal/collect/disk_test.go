package collect

import (
	"os"
	"testing"
)

// The fixtures were captured on an Ubuntu 24.04 VM: mountinfo-host.txt from a
// plain shell, mountinfo-snap-confined.txt from inside the strict maat snap
// (snap run --shell maat). Both describe the same machine.

func displayed(t *testing.T, fixture string) map[string]filesystem {
	t.Helper()
	data, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]filesystem{}
	for _, fs := range selectFilesystems(ParseMountinfo(string(data))) {
		if _, dup := out[fs.Display]; dup {
			t.Fatalf("%s: path %s reported twice", fixture, fs.Display)
		}
		out[fs.Display] = fs
	}
	return out
}

func TestSelectFilesystemsInsideSnapShowsHostView(t *testing.T) {
	got := displayed(t, "mountinfo-snap-confined.txt")

	root, ok := got["/"]
	if !ok || root.FSType != "ext4" || root.Device != "/dev/vda1" {
		t.Fatalf("the host root filesystem must appear as / (ext4 on /dev/vda1), got %+v", root)
	}
	if root.MountPoint != "/var/lib/snapd/hostfs" {
		t.Errorf("statfs must target the hostfs path, got %q", root.MountPoint)
	}
	for _, want := range []string{"/boot", "/boot/efi", "/run", "/Users/dev/maat"} {
		if _, ok := got[want]; !ok {
			t.Errorf("host filesystem %s missing", want)
		}
	}
	// Bind mounts of the root filesystem, snap internals and optical media must
	// not show up as separate filesystems.
	for _, unwanted := range []string{"/etc", "/home", "/var/log", "/tmp", "/usr/lib/modules", "/var/lib/snapd/hostfs", "/mnt/lima-cidata", "/run/netns"} {
		if fs, ok := got[unwanted]; ok {
			t.Errorf("%s should not be reported separately: %+v", unwanted, fs)
		}
	}
}

func TestSelectFilesystemsHostMatchesConfinedView(t *testing.T) {
	host := displayed(t, "mountinfo-host.txt")
	snap := displayed(t, "mountinfo-snap-confined.txt")
	for path, h := range host {
		s, ok := snap[path]
		if !ok {
			// /dev/shm and /run/user are visible to both; anything else the
			// confined view lacks is worth knowing about.
			t.Logf("only on the host: %s (%s)", path, h.FSType)
			continue
		}
		if s.DevID != h.DevID {
			t.Errorf("%s is device %s on the host but %s inside the snap", path, h.DevID, s.DevID)
		}
	}
	for _, want := range []string{"/", "/boot", "/boot/efi"} {
		if _, ok := host[want]; !ok {
			t.Errorf("host view lacks %s", want)
		}
	}
}

func TestParseMountinfoReadOnlyAndSkips(t *testing.T) {
	data := `28 1 253:1 / / rw,relatime shared:1 - ext4 /dev/vda1 rw
40 28 253:16 / /mnt/cdrom ro,relatime shared:9 - iso9660 /dev/vdb ro
41 28 253:2 / /srv/archive ro,relatime shared:10 - ext4 /dev/vdc ro
42 28 7:3 / /snap/lxd/1 ro,nodev,relatime shared:11 - squashfs /dev/loop3 ro`
	ms := ParseMountinfo(data)
	if len(ms) != 2 {
		t.Fatalf("expected / and /srv/archive, got %+v", ms)
	}
	if ms[0].ReadOnly || !ms[1].ReadOnly {
		t.Errorf("read-only flags wrong: %+v", ms)
	}
}

func TestSortDisksWritableAndFullestFirst(t *testing.T) {
	ds := []DiskUsage{
		{MountPoint: "/ro", ReadOnly: true, UsedPct: 100},
		{MountPoint: "/a", UsedPct: 40},
		{MountPoint: "/inodes", UsedPct: 10, InodesUsedPct: 100},
		{MountPoint: "/full", UsedPct: 99},
	}
	sortDisks(ds)
	got := []string{ds[0].MountPoint, ds[1].MountPoint, ds[2].MountPoint, ds[3].MountPoint}
	want := []string{"/inodes", "/full", "/a", "/ro"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
