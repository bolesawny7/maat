package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// NetInterface is one network link with its addresses.
type NetInterface struct {
	Name      string       `json:"name"`
	MAC       string       `json:"mac,omitempty"`
	MTU       int          `json:"mtu,omitempty"`
	OperState string       `json:"oper_state,omitempty"`
	Up        *bool        `json:"up,omitempty"` // absent when the state can't be read
	Addrs     []string     `json:"addrs,omitempty"`
	Counters  *NetCounters `json:"counters,omitempty"`
}

// NetCounters are an interface's cumulative traffic counters from /proc/net/dev.
type NetCounters struct {
	RxBytes   int64 `json:"rx_bytes"`
	RxErrors  int64 `json:"rx_errors"`
	RxDropped int64 `json:"rx_dropped"`
	TxBytes   int64 `json:"tx_bytes"`
	TxErrors  int64 `json:"tx_errors"`
	TxDropped int64 `json:"tx_dropped"`
}

// Route is one routing-table entry.
type Route struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway,omitempty"`
	Dev     string `json:"dev"`
	Default bool   `json:"default"`
}

// NetworkState is the host's links and routes. When Limited is set, only what
// /proc/net/dev provides is known, and Note says why.
type NetworkState struct {
	Interfaces []NetInterface `json:"interfaces"`
	Routes     []Route        `json:"routes"`
	Limited    bool           `json:"limited,omitempty"`
	Note       string         `json:"note,omitempty"`
}

// confinedNetworkNote explains the limited view under the strict snap.
const confinedNetworkNote = "Addresses and routes are unavailable here: under maat's strict snap confinement reading them needs the network-observe interface, which maat deliberately does not use because it also permits outbound connections. Interface names and traffic counters come from /proc/net/dev."

// The following mirror the JSON that `ip -j` emits.
type ipAddrJSON struct {
	IfName    string   `json:"ifname"`
	Address   string   `json:"address"`
	MTU       int      `json:"mtu"`
	OperState string   `json:"operstate"`
	Flags     []string `json:"flags"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

type ipRouteJSON struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
}

// ParseIPAddr parses the output of `ip -j addr`.
func ParseIPAddr(data []byte) ([]NetInterface, error) {
	var raw []ipAddrJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]NetInterface, 0, len(raw))
	for _, r := range raw {
		up := containsStr(r.Flags, "UP")
		iface := NetInterface{
			Name:      r.IfName,
			MAC:       r.Address,
			MTU:       r.MTU,
			OperState: r.OperState,
			Up:        &up,
		}
		for _, a := range r.AddrInfo {
			iface.Addrs = append(iface.Addrs, a.Local)
		}
		out = append(out, iface)
	}
	return out, nil
}

// ParseIPRoute parses the output of `ip -j route`.
func ParseIPRoute(data []byte) ([]Route, error) {
	var raw []ipRouteJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Route, 0, len(raw))
	for _, r := range raw {
		out = append(out, Route{
			Dst:     r.Dst,
			Gateway: r.Gateway,
			Dev:     r.Dev,
			Default: r.Dst == "default",
		})
	}
	return out, nil
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// ParseNetDev parses /proc/net/dev into per-interface counters, in file order.
func ParseNetDev(data string) ([]string, map[string]NetCounters) {
	var names []string
	out := map[string]NetCounters{}
	for _, line := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if name == "" || len(f) < 16 {
			continue
		}
		n := func(i int) int64 { v, _ := strconv.ParseInt(f[i], 10, 64); return v }
		names = append(names, name)
		out[name] = NetCounters{
			RxBytes: n(0), RxErrors: n(2), RxDropped: n(3),
			TxBytes: n(8), TxErrors: n(10), TxDropped: n(11),
		}
	}
	return names, out
}

// CollectNetwork gathers links and routes by running `ip -j` with a fixed
// argument list, and attaches traffic counters from /proc/net/dev. Inside the
// strict snap `ip` cannot read links or routes (that needs netlink), so the
// result falls back to the interface names and counters system-observe allows,
// marked Limited.
func CollectNetwork(ctx context.Context) (NetworkState, error) {
	var ns NetworkState
	devData, devErr := os.ReadFile("/proc/net/dev")
	names, counters := ParseNetDev(string(devData))

	addrOut, err := exec.CommandContext(ctx, "ip", "-j", "addr").Output()
	if err != nil {
		if devErr != nil {
			return ns, fmt.Errorf("network state is unavailable: ip failed (%v) and /proc/net/dev is unreadable (%v)", err, devErr)
		}
		for _, name := range names {
			c := counters[name]
			ns.Interfaces = append(ns.Interfaces, NetInterface{Name: name, Counters: &c})
		}
		ns.Limited, ns.Note = true, confinedNetworkNote
		return ns, nil
	}
	ns.Interfaces, err = ParseIPAddr(addrOut)
	if err != nil {
		return ns, err
	}
	for i := range ns.Interfaces {
		if c, ok := counters[ns.Interfaces[i].Name]; ok {
			ns.Interfaces[i].Counters = &c
		}
	}
	routeOut, err := exec.CommandContext(ctx, "ip", "-j", "route").Output()
	if err != nil {
		return ns, err
	}
	ns.Routes, err = ParseIPRoute(routeOut)
	if err != nil {
		return ns, err
	}
	return ns, nil
}
