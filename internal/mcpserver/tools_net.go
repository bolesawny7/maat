package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/collect"
)

type emptyIn struct{}

type budgetIn struct {
	MaxItems int `json:"max_items,omitempty"`
	MaxChars int `json:"max_chars,omitempty"`
}

// ---- network_state ----

func (d *Deps) networkState(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, collect.NetworkState, error) {
	cctx, cancel := ctxWithTimeout(ctx, 5*time.Second)
	defer cancel()
	ns, err := collect.CollectNetwork(cctx)
	if err != nil {
		return errResult(err), collect.NetworkState{}, nil
	}
	summary := fmt.Sprintf("%d interfaces, %d routes", len(ns.Interfaces), len(ns.Routes))
	if ns.Limited {
		summary += " (limited view: " + ns.Note + ")"
	}
	return textResult(summary), ns, nil
}

// ---- listening_sockets ----

type socketsOut struct {
	Sockets   []collect.Socket `json:"sockets"`
	Truncated bool             `json:"truncated,omitempty"`
}

func (d *Deps) listeningSockets(ctx context.Context, _ *mcp.CallToolRequest, in budgetIn) (*mcp.CallToolResult, socketsOut, error) {
	socks, err := collect.CollectSockets("/")
	if err != nil {
		return errResult(err), socketsOut{}, nil
	}
	var out socketsOut
	out.Sockets, out.Truncated = fit(socks, d.effectiveBudget(in.MaxItems, in.MaxChars))
	for _, s := range out.Sockets {
		owner := "owner unknown"
		if s.PID > 0 {
			owner = fmt.Sprintf("pid %d %s", s.PID, s.Comm)
		}
		d.evidence.add(s.EvidenceID, "socket", fmt.Sprintf("%s:%d", s.LocalAddr, s.LocalPort), fmt.Sprintf("%s %s %s:%d (%s)", s.Proto, s.State, s.LocalAddr, s.LocalPort, owner))
	}
	return textResult(fmt.Sprintf("%d listening/bound sockets", len(socks))), out, nil
}

// ---- disk_usage ----

type disksOut struct {
	Filesystems []collect.DiskUsage `json:"filesystems"`
	Truncated   bool                `json:"truncated,omitempty"`
}

func (d *Deps) diskUsage(ctx context.Context, _ *mcp.CallToolRequest, in budgetIn) (*mcp.CallToolResult, disksOut, error) {
	disks, err := collect.CollectDisks("/")
	if err != nil {
		return errResult(err), disksOut{}, nil
	}
	var out disksOut
	out.Filesystems, out.Truncated = fit(disks, d.effectiveBudget(in.MaxItems, in.MaxChars))
	for _, fs := range out.Filesystems {
		ro := ""
		if fs.ReadOnly {
			ro = ", read-only"
		}
		d.evidence.add(fs.EvidenceID, "disk", fs.MountPoint, fmt.Sprintf("%s (%s on %s%s): %.1f%% space used, %d MB free, %.1f%% inodes used",
			fs.MountPoint, fs.FSType, fs.Device, ro, fs.UsedPct, fs.AvailMB, fs.InodesUsedPct))
	}
	return textResult(fmt.Sprintf("%d filesystems", len(disks))), out, nil
}
