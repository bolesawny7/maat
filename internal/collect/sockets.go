package collect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Socket is a listening (TCP) or bound (UDP) socket, with the owning process
// resolved when possible.
type Socket struct {
	Proto     string `json:"proto"`
	LocalAddr string `json:"local_addr"`
	LocalPort int    `json:"local_port"`
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
	Comm      string `json:"comm,omitempty"`
	inode     uint64
}

// TCP state codes from the kernel; 0x0A is LISTEN.
const tcpListen = "0A"

// parseProcNet parses /proc/net/{tcp,tcp6,udp,udp6}. For TCP it keeps only
// listening sockets; for UDP it keeps bound sockets (state 07). Addresses are
// hex and little-endian in these files; decodeHexAddr handles both v4 and v6.
func parseProcNet(proto, data string) []Socket {
	var out []Socket
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		if i == 0 { // header
			continue
		}
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		state := f[3]
		isTCP := strings.HasPrefix(proto, "tcp")
		if isTCP && state != tcpListen {
			continue
		}
		if !isTCP && state != "07" {
			continue
		}
		addr, port := decodeHexAddr(f[1])
		inode, _ := strconv.ParseUint(f[9], 10, 64)
		s := Socket{
			Proto:     proto,
			LocalAddr: addr,
			LocalPort: port,
			State:     stateForProto(isTCP, state),
			inode:     inode,
		}
		out = append(out, s)
	}
	return out
}

func stateForProto(isTCP bool, code string) string {
	if isTCP {
		return "LISTEN"
	}
	return "BOUND"
}

// decodeHexAddr decodes a "HEXADDR:HEXPORT" field from /proc/net/*. IPv4 is 8
// hex chars (little-endian bytes); IPv6 is 32. The port is big-endian hex.
func decodeHexAddr(s string) (string, int) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", 0
	}
	port64, _ := strconv.ParseUint(parts[1], 16, 32)
	port := int(port64)

	hexAddr := parts[0]
	switch len(hexAddr) {
	case 8: // IPv4, little-endian
		b := make([]byte, 4)
		for i := 0; i < 4; i++ {
			v, _ := strconv.ParseUint(hexAddr[i*2:i*2+2], 16, 8)
			b[3-i] = byte(v)
		}
		return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3]), port
	case 32: // IPv6, little-endian per 32-bit word
		var words []string
		for w := 0; w < 4; w++ {
			chunk := hexAddr[w*8 : w*8+8]
			b := make([]byte, 4)
			for i := 0; i < 4; i++ {
				v, _ := strconv.ParseUint(chunk[i*2:i*2+2], 16, 8)
				b[3-i] = byte(v)
			}
			words = append(words, fmt.Sprintf("%02x%02x", b[0], b[1]), fmt.Sprintf("%02x%02x", b[2], b[3]))
		}
		return strings.Join(words, ":"), port
	default:
		return hexAddr, port
	}
}

// CollectSockets reads the four /proc/net socket tables under root, then maps
// socket inodes to processes by scanning /proc/<pid>/fd. Use "/" for live.
func CollectSockets(root string) ([]Socket, error) {
	procDir := filepath.Join(root, "proc")
	var sockets []Socket
	for _, proto := range []string{"tcp", "tcp6", "udp", "udp6"} {
		data := readFile(filepath.Join(procDir, "net", proto))
		if data == "" {
			continue
		}
		sockets = append(sockets, parseProcNet(proto, data)...)
	}
	inodeToProc := mapInodesToProcs(procDir)
	for i := range sockets {
		if p, ok := inodeToProc[sockets[i].inode]; ok {
			sockets[i].PID = p.pid
			sockets[i].Comm = p.comm
		}
	}
	sort.SliceStable(sockets, func(i, j int) bool {
		if sockets[i].Proto != sockets[j].Proto {
			return sockets[i].Proto < sockets[j].Proto
		}
		return sockets[i].LocalPort < sockets[j].LocalPort
	})
	return sockets, nil
}

type procRef struct {
	pid  int
	comm string
}

// mapInodesToProcs builds a socket-inode to process map by reading each
// process's open file descriptors. Permission errors on individual processes
// are ignored so the scan still covers what it can.
func mapInodesToProcs(procDir string) map[uint64]procRef {
	out := map[uint64]procRef{}
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join(procDir, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		var comm string
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inodeStr := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			inode, err := strconv.ParseUint(inodeStr, 10, 64)
			if err != nil {
				continue
			}
			if comm == "" {
				comm = strings.TrimSpace(readFile(filepath.Join(procDir, e.Name(), "comm")))
			}
			if _, exists := out[inode]; !exists {
				out[inode] = procRef{pid: pid, comm: comm}
			}
		}
	}
	return out
}
