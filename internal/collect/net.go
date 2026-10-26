package collect

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// NetInterface is one network link with its addresses.
type NetInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	MTU       int      `json:"mtu,omitempty"`
	OperState string   `json:"oper_state"`
	Up        bool     `json:"up"`
	Addrs     []string `json:"addrs,omitempty"`
}

// Route is one routing-table entry.
type Route struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway,omitempty"`
	Dev     string `json:"dev"`
	Default bool   `json:"default"`
}

// NetworkState is the host's links and routes.
type NetworkState struct {
	Interfaces []NetInterface `json:"interfaces"`
	Routes     []Route        `json:"routes"`
}

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
		iface := NetInterface{
			Name:      r.IfName,
			MAC:       r.Address,
			MTU:       r.MTU,
			OperState: r.OperState,
			Up:        containsStr(r.Flags, "UP"),
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

// CollectNetwork gathers links and routes by running `ip -j`. The snap ships
// iproute2 as a staged package and holds the network-observe interface, which
// grants read-only access to this information. ip is invoked with a fixed
// argument list and no shell.
func CollectNetwork(ctx context.Context) (NetworkState, error) {
	var ns NetworkState
	addrOut, err := exec.CommandContext(ctx, "ip", "-j", "addr").Output()
	if err != nil {
		return ns, err
	}
	ns.Interfaces, err = ParseIPAddr(addrOut)
	if err != nil {
		return ns, err
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
