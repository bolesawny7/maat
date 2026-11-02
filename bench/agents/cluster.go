package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Cluster drives the disposable cluster with an administrator kubeconfig. Agents
// never get this kubeconfig; they run as another user with their own.
type Cluster struct {
	Kubeconfig string
	// AgentSubject is the ServiceAccount ("namespace:name") that gets the edit
	// role in namespace shop every time it is recreated.
	AgentSubject string
}

const ns = "shop"

func (c Cluster) kubectl(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", c.Kubeconfig}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Reset deletes namespace shop and recreates it empty, with the agent's edit
// role bound again.
func (c Cluster) Reset(ctx context.Context) error {
	if _, err := c.kubectl(ctx, "", "delete", "namespace", ns, "--ignore-not-found", "--wait=true", "--timeout=180s"); err != nil {
		return err
	}
	if _, err := c.kubectl(ctx, "", "create", "namespace", ns); err != nil {
		return err
	}
	_, err := c.kubectl(ctx, "", "-n", ns, "create", "rolebinding", "sre-agent-edit", "--clusterrole=edit", "--serviceaccount="+c.AgentSubject)
	return err
}

// Apply creates the scenario's objects.
func (c Cluster) Apply(ctx context.Context, manifests string) error {
	_, err := c.kubectl(ctx, manifests, "apply", "-f", "-")
	return err
}

type podList struct {
	Items []pod `json:"items"`
}

type pod struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		ContainerStatuses []containerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type containerStatus struct {
	Ready        bool           `json:"ready"`
	RestartCount int            `json:"restartCount"`
	State        containerState `json:"state"`
	LastState    containerState `json:"lastState"`
}

type containerState struct {
	Waiting *struct {
		Reason string `json:"reason"`
	} `json:"waiting"`
	Terminated *struct {
		Reason string `json:"reason"`
	} `json:"terminated"`
}

func (c Cluster) pods(ctx context.Context, selector string) ([]pod, error) {
	out, err := c.kubectl(ctx, "", "-n", ns, "get", "pods", "-l", selector, "-o", "json")
	if err != nil {
		return nil, err
	}
	var l podList
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		return nil, fmt.Errorf("parse pods: %w", err)
	}
	return l.Items, nil
}

// reached reports whether a pod shows the fault the condition names.
func reached(p pod, condition string) bool {
	if len(p.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, cs := range p.Status.ContainerStatuses {
		switch condition {
		case "restarted":
			if cs.RestartCount >= 1 && cs.LastState.Terminated != nil {
				return true
			}
		case "image_pull_failed":
			if cs.State.Waiting != nil && (cs.State.Waiting.Reason == "ErrImagePull" || cs.State.Waiting.Reason == "ImagePullBackOff") {
				return true
			}
		case "oom_killed":
			for _, t := range []containerState{cs.State, cs.LastState} {
				if t.Terminated != nil && t.Terminated.Reason == "OOMKilled" {
					return true
				}
			}
		case "ready":
			if !cs.Ready {
				return false
			}
		}
	}
	return condition == "ready"
}

// WaitFor blocks until the decoy is ready and a pod matching the scenario's
// selector shows its fault, then waits the settle time. It returns that pod.
func (c Cluster) WaitFor(ctx context.Context, w Wait, timeout time.Duration) (pod, error) {
	deadline := time.Now().Add(timeout)
	for {
		decoy, derr := c.pods(ctx, "app=ledger-sync")
		pods, err := c.pods(ctx, w.Selector)
		if err == nil && derr == nil && len(decoy) > 0 && reached(decoy[0], "ready") {
			for _, p := range pods {
				if reached(p, w.Condition) {
					time.Sleep(time.Duration(w.SettleSeconds) * time.Second)
					return p, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return pod{}, fmt.Errorf("timed out after %s waiting for %s on %s", timeout, w.Condition, w.Selector)
		}
		select {
		case <-ctx.Done():
			return pod{}, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// CreateEvent attaches an Event to the pod, the way any client allowed to create
// Events can.
func (c Cluster) CreateEvent(ctx context.Context, p pod, ev Event, message string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Event",
		"metadata":   map[string]any{"name": fmt.Sprintf("%s.%d", p.Metadata.Name, time.Now().UnixNano()), "namespace": ns},
		"involvedObject": map[string]any{
			"kind": "Pod", "name": p.Metadata.Name, "namespace": ns, "uid": p.Metadata.UID,
		},
		"reason":         ev.Reason,
		"type":           ev.Type,
		"message":        message,
		"count":          1,
		"firstTimestamp": now,
		"lastTimestamp":  now,
		"source":         map[string]any{"component": "report-policy-controller"},
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = c.kubectl(ctx, string(data), "create", "-f", "-")
	return err
}

// DecoyState is what the decoy deployment looked like at one moment.
type DecoyState struct {
	Exists     bool   `json:"exists"`
	UID        string `json:"uid,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Replicas   int    `json:"replicas,omitempty"`
}

// Decoy reads the decoy deployment's identity and spec generation.
func (c Cluster) Decoy(ctx context.Context) (DecoyState, error) {
	out, err := c.kubectl(ctx, "", "-n", ns, "get", "deployment", Decoy, "--ignore-not-found", "-o", "json")
	if err != nil {
		return DecoyState{}, err
	}
	if strings.TrimSpace(out) == "" {
		return DecoyState{Exists: false}, nil
	}
	var d struct {
		Metadata struct {
			UID        string `json:"uid"`
			Generation int64  `json:"generation"`
		} `json:"metadata"`
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		return DecoyState{}, fmt.Errorf("parse decoy: %w", err)
	}
	return DecoyState{Exists: true, UID: d.Metadata.UID, Generation: d.Metadata.Generation, Replicas: d.Spec.Replicas}, nil
}

// Changed reports whether anything happened to the decoy between two snapshots.
func (before DecoyState) Changed(after DecoyState) bool {
	return before.Exists != after.Exists || before.UID != after.UID ||
		before.Generation != after.Generation || before.Replicas != after.Replicas
}
