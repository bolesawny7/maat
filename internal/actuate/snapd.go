package actuate

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// snapdSocket is the Unix socket snapd listens on for its REST API.
const snapdSocket = "/run/snapd.socket"

// snapdClient talks to snapd's REST API over its Unix socket. maatctl uses it to
// check snap state and to revert a snap; the request is authorized by snapd
// through polkit (io.snapcraft.snapd.manage).
type snapdClient struct {
	http *http.Client
}

func newSnapdClient() *snapdClient {
	return &snapdClient{
		http: &http.Client{
			Timeout: 90 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", snapdSocket)
				},
			},
		},
	}
}

// snapInfo is the subset of GET /v2/snaps/{name} we use.
type snapInfo struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
	Status   string `json:"status"`
}

type snapdResponse struct {
	Type       string          `json:"type"`
	StatusCode int             `json:"status-code"`
	Result     json.RawMessage `json:"result"`
	Change     string          `json:"change"`
}

func (c *snapdClient) get(ctx context.Context, path string) (*snapdResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://snapd"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var sr snapdResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}
	return &sr, nil
}

// SnapInfo returns a snap's installed revision and status.
func (c *snapdClient) SnapInfo(ctx context.Context, name string) (snapInfo, error) {
	sr, err := c.get(ctx, "/v2/snaps/"+name)
	if err != nil {
		return snapInfo{}, err
	}
	if sr.StatusCode == 404 {
		return snapInfo{}, fmt.Errorf("snap %q is not installed", name)
	}
	if sr.StatusCode/100 != 2 {
		return snapInfo{}, fmt.Errorf("snapd returned status %d for snap %q", sr.StatusCode, name)
	}
	var info snapInfo
	if err := json.Unmarshal(sr.Result, &info); err != nil {
		return snapInfo{}, err
	}
	return info, nil
}

// IsInstalled reports whether a snap is installed.
func (c *snapdClient) IsInstalled(ctx context.Context, name string) (bool, error) {
	_, err := c.SnapInfo(ctx, name)
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// snapApp is the subset of GET /v2/apps we use.
type snapApp struct {
	Snap   string `json:"snap"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Daemon string `json:"daemon"`
}

// ServicesActive reports whether all of a snap's services are active. A snap with
// no services is considered active.
func (c *snapdClient) ServicesActive(ctx context.Context, name string) (bool, error) {
	sr, err := c.get(ctx, "/v2/apps?names="+name+"&select=service")
	if err != nil {
		return false, err
	}
	if sr.StatusCode/100 != 2 {
		return false, fmt.Errorf("snapd returned status %d for apps of %q", sr.StatusCode, name)
	}
	var apps []snapApp
	if err := json.Unmarshal(sr.Result, &apps); err != nil {
		return false, err
	}
	for _, a := range apps {
		if a.Daemon != "" && !a.Active {
			return false, nil
		}
	}
	return true, nil
}

// Revert reverts a snap to its previous revision and waits for the change to
// complete. It returns the revision the snap was at before the revert, so a
// rollback can refresh back to it.
func (c *snapdClient) Revert(ctx context.Context, name string) (prevRevision string, err error) {
	before, err := c.SnapInfo(ctx, name)
	if err != nil {
		return "", err
	}
	body := strings.NewReader(`{"action":"revert"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://snapd/v2/snaps/"+name, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// Allow snapd to drive an interactive polkit prompt if no rule pre-authorizes.
	req.Header.Set("X-Allow-Interaction", "true")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var sr snapdResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", err
	}
	if sr.StatusCode/100 != 2 {
		return "", fmt.Errorf("snapd refused revert of %q (status %d)", name, sr.StatusCode)
	}
	if sr.Change != "" {
		if err := c.waitChange(ctx, sr.Change); err != nil {
			return before.Revision, err
		}
	}
	return before.Revision, nil
}

// waitChange polls a snapd change until it is Done or Error.
func (c *snapdClient) waitChange(ctx context.Context, id string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		sr, err := c.get(ctx, "/v2/changes/"+id)
		if err != nil {
			return err
		}
		var ch struct {
			Status string `json:"status"`
			Err    string `json:"err"`
		}
		if err := json.Unmarshal(sr.Result, &ch); err != nil {
			return err
		}
		switch ch.Status {
		case "Done":
			return nil
		case "Error", "Undone", "Hold", "Abort":
			return fmt.Errorf("snapd change %s ended as %s: %s", id, ch.Status, ch.Err)
		}
	}
}
