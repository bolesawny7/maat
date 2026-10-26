package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/collect"
)

type emptyIn struct{}

// ---- network_state ----

func (d *Deps) networkState(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, collect.NetworkState, error) {
	cctx, cancel := ctxWithTimeout(ctx, 5*time.Second)
	defer cancel()
	ns, err := collect.CollectNetwork(cctx)
	if err != nil {
		return errResult(err), collect.NetworkState{}, nil
	}
	return textResult(fmt.Sprintf("%d interfaces, %d routes", len(ns.Interfaces), len(ns.Routes))), ns, nil
}

// ---- listening_sockets ----

type socketsOut struct {
	Sockets []collect.Socket `json:"sockets"`
}

func (d *Deps) listeningSockets(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, socketsOut, error) {
	socks, err := collect.CollectSockets("/")
	if err != nil {
		return errResult(err), socketsOut{}, nil
	}
	return textResult(fmt.Sprintf("%d listening/bound sockets", len(socks))), socketsOut{Sockets: socks}, nil
}

// ---- disk_usage ----

type disksOut struct {
	Filesystems []collect.DiskUsage `json:"filesystems"`
}

func (d *Deps) diskUsage(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, disksOut, error) {
	disks, err := collect.CollectDisks("/")
	if err != nil {
		return errResult(err), disksOut{}, nil
	}
	return textResult(fmt.Sprintf("%d filesystems", len(disks))), disksOut{Filesystems: disks}, nil
}
