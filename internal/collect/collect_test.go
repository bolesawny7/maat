package collect

import (
	"strings"
	"testing"

	"github.com/bolesawny7/maat/internal/obs"
)

func TestParseJournalJSON(t *testing.T) {
	// Two real-shaped journal lines: one normal, one with a MESSAGE encoded as a
	// byte array (as journald does for non-UTF-8 payloads).
	input := strings.Join([]string{
		`{"__REALTIME_TIMESTAMP":"1761421200000000","__CURSOR":"s=abc;i=1","PRIORITY":"3","_SYSTEMD_UNIT":"nginx.service","MESSAGE":"connect() failed (111: Connection refused)","_HOSTNAME":"box","_TRANSPORT":"journal"}`,
		`{"__REALTIME_TIMESTAMP":"1761421201000000","PRIORITY":"6","SYSLOG_IDENTIFIER":"kernel","MESSAGE":[72,105,0,255],"_TRANSPORT":"kernel"}`,
		``,
		`{bad json`,
	}, "\n")

	recs, err := ParseJournalJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records (bad line skipped), got %d", len(recs))
	}
	if recs[0].Unit != "nginx.service" || recs[0].Priority != obs.SevError {
		t.Errorf("record 0 wrong: %+v", recs[0])
	}
	if !strings.Contains(recs[0].Message, "Connection refused") {
		t.Errorf("message not parsed: %q", recs[0].Message)
	}
	if recs[0].Time.IsZero() {
		t.Error("timestamp not parsed")
	}
	if recs[1].Source != "kernel" {
		t.Errorf("kernel transport not mapped: %q", recs[1].Source)
	}
	if !strings.HasPrefix(recs[1].Message, "Hi") { // bytes 72,105 = "Hi"
		t.Errorf("byte-array message not decoded: %q", recs[1].Message)
	}
}

func TestJournalctlArgsNoInjection(t *testing.T) {
	args := journalctlArgs(JournalOptions{Unit: "nginx.service", Priority: obs.SevWarning, Lines: 500, ThisBoot: true})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--output=json") {
		t.Error("missing json output flag")
	}
	// The unit must appear as a separate argument after --unit, never fused.
	foundUnit := false
	for i, a := range args {
		if a == "--unit" && i+1 < len(args) && args[i+1] == "nginx.service" {
			foundUnit = true
		}
	}
	if !foundUnit {
		t.Errorf("unit not passed as its own argument: %v", args)
	}
}

func TestJournalctlArgsPriority(t *testing.T) {
	// Zero priority (the unset default) must NOT add a --priority filter; it once
	// collided with SevEmergency (also 0) and hid everything but emergencies.
	args := strings.Join(journalctlArgs(JournalOptions{}), " ")
	if strings.Contains(args, "--priority") {
		t.Errorf("zero priority should not filter; got %q", args)
	}
	// A positive priority must be passed through.
	args = strings.Join(journalctlArgs(JournalOptions{Priority: obs.SevError}), " ")
	if !strings.Contains(args, "--priority 3") {
		t.Errorf("expected --priority 3 for SevError; got %q", args)
	}
}

func TestParseAppArmorDenial(t *testing.T) {
	msg := `apparmor="DENIED" operation="connect" profile="snap.maat.maat" name="/run/systemd/private" pid=4242 comm="maat" requested_mask="wr" denied_mask="wr" fsuid=1000 ouid=0`
	d, ok := ParseAppArmorDenial(msg)
	if !ok {
		t.Fatal("expected a denial")
	}
	if d.Operation != "connect" || d.Profile != "snap.maat.maat" || d.Comm != "maat" {
		t.Errorf("fields wrong: %+v", d)
	}
	if d.Name != "/run/systemd/private" {
		t.Errorf("name wrong: %q", d.Name)
	}
	if _, ok := ParseAppArmorDenial("just a normal log line"); ok {
		t.Error("false positive on normal line")
	}
}

func TestParseAppArmorDBusDenialUsesLabel(t *testing.T) {
	// A real D-Bus mediation denial captured from the confined self-test: it has
	// label= rather than profile=, and member= names the refused method.
	msg := `apparmor="DENIED" operation="dbus_method_call"  bus="system" path="/org/freedesktop/systemd1" interface="org.freedesktop.systemd1.Manager" member="RestartUnit" mask="send" name="org.freedesktop.systemd1" pid=343096 label="snap.maat.maat" peer_pid=1 peer_label="unconfined"`
	d, ok := ParseAppArmorDenial(msg)
	if !ok {
		t.Fatal("expected a denial")
	}
	if d.Profile != "snap.maat.maat" || d.Operation != "dbus_method_call" {
		t.Errorf("profile/operation wrong: %+v", d)
	}
}

func TestNetDevParse(t *testing.T) {
	data := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  123456     100    0    0    0     0          0         0   123456     100    0    0    0     0       0          0
  eth0: 9876543    5000    3    7    0     0          0        12   4567890    4000    1    2    0     0       0          0
`
	names, c := ParseNetDev(data)
	if len(names) != 2 || names[1] != "eth0" {
		t.Fatalf("names = %v", names)
	}
	e := c["eth0"]
	if e.RxBytes != 9876543 || e.RxErrors != 3 || e.RxDropped != 7 || e.TxBytes != 4567890 || e.TxErrors != 1 || e.TxDropped != 2 {
		t.Errorf("eth0 counters wrong: %+v", e)
	}
}

func TestCollectSocketsFailsClosed(t *testing.T) {
	// A proc root with no readable socket tables must be an error, not "nothing
	// is listening".
	if _, err := CollectSockets(t.TempDir()); err == nil {
		t.Fatal("expected ErrSocketsUnavailable")
	}
}

func TestAppArmorDenialsDeduplicate(t *testing.T) {
	line := `apparmor="DENIED" operation="open" profile="p" name="/x" denied_mask="r"`
	recs := []obs.LogRecord{
		{Message: line}, {Message: line}, {Message: line},
		{Message: `apparmor="DENIED" operation="exec" profile="p" name="/y" denied_mask="x"`},
	}
	ds := AppArmorDenials(recs)
	if len(ds) != 2 {
		t.Fatalf("expected 2 unique denials, got %d", len(ds))
	}
	if ds[0].Count != 3 {
		t.Errorf("expected the repeated denial to have count 3, got %d", ds[0].Count)
	}
}

func TestParseMountinfo(t *testing.T) {
	data := `23 28 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:14 - proc proc rw
24 28 0:22 / /sys rw,nosuid,nodev,noexec,relatime shared:15 - sysfs sysfs rw
28 1 259:2 / / rw,relatime shared:1 - ext4 /dev/vda2 rw,errors=remount-ro
100 28 7:0 / /snap/core24/1 ro,nodev,relatime shared:50 - squashfs /dev/loop0 ro
120 28 0:50 / /mnt/my\040data rw,relatime shared:60 - ext4 /dev/vdb rw`

	mounts := ParseMountinfo(data)
	// proc, sysfs, squashfs dropped; root and the space-named mount kept.
	if len(mounts) != 2 {
		t.Fatalf("expected 2 real mounts, got %d: %+v", len(mounts), mounts)
	}
	var root, spaced *Mount
	for i := range mounts {
		if mounts[i].MountPoint == "/" {
			root = &mounts[i]
		}
		if strings.Contains(mounts[i].MountPoint, "my data") {
			spaced = &mounts[i]
		}
	}
	if root == nil || root.FSType != "ext4" || root.Device != "/dev/vda2" {
		t.Errorf("root mount wrong: %+v", root)
	}
	if spaced == nil {
		t.Error("octal-escaped mount point not decoded")
	}
}

func TestParseProcNetTCP(t *testing.T) {
	// sl local rem st ... inode (fields trimmed but column positions preserved).
	data := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1538 00000000:0000 0A 00000000:00000000 00:00000000 00000000   101        0 12345 1 0000 100 0 0 10 0
   1: 0100007F:0035 00000000:0000 01 00000000:00000000 00:00000000 00000000   101        0 99999 1 0000 100 0 0 10 0`

	socks := parseProcNet("tcp", data)
	if len(socks) != 1 {
		t.Fatalf("expected 1 listening socket, got %d: %+v", len(socks), socks)
	}
	if socks[0].LocalAddr != "127.0.0.1" {
		t.Errorf("addr decode wrong: %q", socks[0].LocalAddr)
	}
	if socks[0].LocalPort != 5432 { // 0x1538
		t.Errorf("port decode wrong: %d", socks[0].LocalPort)
	}
	if socks[0].State != "LISTEN" {
		t.Errorf("state wrong: %q", socks[0].State)
	}
}

func TestParseMeminfoAndLoadavg(t *testing.T) {
	mem := parseMeminfo("MemTotal:       16384000 kB\nMemAvailable:    8192000 kB\nSwapTotal: 1000 kB\nSwapFree: 400 kB\n")
	if mem["MemTotal"] != 16384000 || mem["MemAvailable"] != 8192000 {
		t.Errorf("meminfo parse wrong: %+v", mem)
	}
	l1, l5, l15 := parseLoadavg("0.52 0.58 0.59 1/789 12345")
	if l1 != 0.52 || l5 != 0.58 || l15 != 0.59 {
		t.Errorf("loadavg parse wrong: %v %v %v", l1, l5, l15)
	}
}

func TestPrettyName(t *testing.T) {
	got := prettyName("NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.3 LTS\"\nVERSION_ID=\"24.04\"\n")
	if got != "Ubuntu 24.04.3 LTS" {
		t.Errorf("prettyName = %q", got)
	}
}

func TestParseStat(t *testing.T) {
	// comm contains spaces and a closing paren to test robust field location.
	data := `1234 (my (weird) proc) S 1 1234 1234 0 -1 4194560 100 0 0 0 42 17 0 0 20 0 1 0 999 12288 55 18446744073709551615`
	s, ok := parseStat(data)
	if !ok {
		t.Fatal("parseStat failed")
	}
	if s.comm != "my (weird) proc" {
		t.Errorf("comm wrong: %q", s.comm)
	}
	if s.state != "S" {
		t.Errorf("state wrong: %q", s.state)
	}
	if s.utime != 42 || s.stime != 17 {
		t.Errorf("cpu times wrong: u=%d s=%d", s.utime, s.stime)
	}
}

func TestCleanCmdline(t *testing.T) {
	got := cleanCmdline("nginx\x00-g\x00daemon off;\x00")
	if string(got) != "nginx -g daemon off;" {
		t.Errorf("cmdline = %q", got)
	}
}

func TestParseIPAddrAndRoute(t *testing.T) {
	addrJSON := `[{"ifname":"lo","mtu":65536,"operstate":"UNKNOWN","flags":["LOOPBACK","UP"],"address":"00:00:00:00:00:00","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8}]},
	{"ifname":"eth0","mtu":1500,"operstate":"UP","flags":["BROADCAST","UP","LOWER_UP"],"address":"52:54:00:12:34:56","addr_info":[{"family":"inet","local":"10.0.2.15","prefixlen":24}]}]`
	ifaces, err := ParseIPAddr([]byte(addrJSON))
	if err != nil {
		t.Fatalf("ParseIPAddr: %v", err)
	}
	if len(ifaces) != 2 || ifaces[1].Name != "eth0" || ifaces[1].MAC != "52:54:00:12:34:56" {
		t.Fatalf("iface parse wrong: %+v", ifaces)
	}
	if ifaces[1].Up == nil || !*ifaces[1].Up || len(ifaces[1].Addrs) != 1 || ifaces[1].Addrs[0] != "10.0.2.15" {
		t.Errorf("eth0 details wrong: %+v", ifaces[1])
	}

	routeJSON := `[{"dst":"default","gateway":"10.0.2.2","dev":"eth0"},{"dst":"10.0.2.0/24","dev":"eth0"}]`
	routes, err := ParseIPRoute([]byte(routeJSON))
	if err != nil {
		t.Fatalf("ParseIPRoute: %v", err)
	}
	if len(routes) != 2 || !routes[0].Default || routes[0].Gateway != "10.0.2.2" {
		t.Errorf("route parse wrong: %+v", routes)
	}
}

func TestMapRawUnits(t *testing.T) {
	raw := []rawUnit{
		{Name: "b.service", Description: "B", ActiveState: "active", SubState: "running"},
		{Name: "a.service", Description: "A", ActiveState: "failed", SubState: "failed"},
	}
	all := mapRawUnits(raw, false)
	if len(all) != 2 || all[0].Name != "a.service" { // sorted
		t.Fatalf("mapRawUnits all wrong: %+v", all)
	}
	failed := mapRawUnits(raw, true)
	if len(failed) != 1 || failed[0].Name != "a.service" {
		t.Fatalf("mapRawUnits failed filter wrong: %+v", failed)
	}
	if string(failed[0].Description) != "A" {
		t.Errorf("description not carried: %+v", failed[0])
	}
}
