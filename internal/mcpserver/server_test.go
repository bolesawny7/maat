package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bolesawny7/maat/internal/obs"
	"github.com/bolesawny7/maat/internal/plan"
	"github.com/bolesawny7/maat/internal/planstore"
	"github.com/bolesawny7/maat/internal/trail"
)

// connect wires an in-memory client to a maat server built from deps.
func connect(t *testing.T, deps *Deps) (*mcp.ClientSession, func()) {
	t.Helper()
	ctx := context.Background()
	srv := New(deps)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs, func() { cs.Close(); ss.Close() }
}

func testDeps(t *testing.T) *Deps {
	t.Helper()
	dir := t.TempDir()
	ps, err := planstore.Open(filepath.Join(dir, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := trail.Open(filepath.Join(dir, "trail.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Disable journal checkpoints in tests (no journal on the dev host).
	tr.SetCheckpointFuncs(func(map[string]string) error { return nil }, func() ([]map[string]string, error) { return nil, nil })
	return &Deps{Plans: ps, Trail: tr, Budget: obs.Budget{}, Proposer: plan.Proposer{Client: "test-client"}}
}

func TestToolsAreRegisteredAndReadOnly(t *testing.T) {
	cs, done := connect(t, testDeps(t))
	defer done()

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"system_overview": true, "list_units": true, "unit_status": true,
		"recent_failures": true, "journal_summary": true, "journal_entries": true,
		"kernel_messages": true, "apparmor_denials": true, "network_state": true,
		"listening_sockets": true, "disk_usage": true, "top_processes": true,
		"propose_action": true,
	}
	got := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
	// Every tool except propose_action must advertise readOnlyHint.
	for name, tool := range got {
		if name == "propose_action" {
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
				t.Errorf("propose_action must not be read-only")
			}
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q must be read-only", name)
		}
	}
}

func TestProposeActionWritesPlanAndTrail(t *testing.T) {
	deps := testDeps(t)
	cs, done := connect(t, deps)
	defer done()

	args := map[string]any{
		"kind":      "service.restart",
		"params":    map[string]string{"unit": "nginx.service"},
		"evidence":  []string{"ev_abc12345"},
		"rationale": "nginx failed after a transient dependency error",
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "propose_action", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("propose_action returned error: %v", res.Content)
	}
	var out proposeOut
	decodeStructured(t, res, &out)
	if out.PlanID == "" || out.Kind != "service.restart" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if !strings.Contains(out.Note, "maatctl") {
		t.Errorf("note should direct the user to maatctl: %q", out.Note)
	}

	// The plan must be persisted and valid.
	p, digest, err := deps.Plans.Load(out.PlanID)
	if err != nil {
		t.Fatalf("plan not stored: %v", err)
	}
	if p.Params["unit"] != "nginx.service" {
		t.Errorf("stored plan params wrong: %+v", p.Params)
	}
	// The proposal must be recorded in the trail, bound to the file's bytes.
	recs, _ := deps.Trail.ReadAll()
	if len(recs) != 1 || recs[0].Type != trail.TypeProposal || recs[0].Plan != out.PlanID {
		t.Fatalf("trail did not record the proposal: %+v", recs)
	}
	if recs[0].Digest != digest {
		t.Errorf("proposal digest %s does not match the stored file %s", recs[0].Digest, digest)
	}
}

func TestProposeActionAttachesIssuedEvidence(t *testing.T) {
	deps := testDeps(t)
	cs, done := connect(t, deps)
	defer done()
	// Stand in for an earlier tool call that returned this evidence.
	deps.evidence.add("ev_1a2b3c4d", "journal", "app.service", "app.service: Failed with result 'exit-code'.")

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "propose_action", Arguments: map[string]any{
		"kind":      "service.reset-failed",
		"params":    map[string]string{"unit": "app.service"},
		"evidence":  []string{"ev_1a2b3c4d", "ev_99999999"},
		"rationale": "stale failure",
	}})
	if err != nil || res.IsError {
		t.Fatalf("propose_action failed: %v %v", err, res)
	}
	var out proposeOut
	decodeStructured(t, res, &out)
	if len(out.UnresolvedEvidence) != 1 || out.UnresolvedEvidence[0] != "ev_99999999" {
		t.Errorf("the invented evidence ID should be reported back, got %v", out.UnresolvedEvidence)
	}
	p, _, err := deps.Plans.Load(out.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.EvidenceDetail) != 1 || p.EvidenceDetail[0].ID != "ev_1a2b3c4d" || !strings.Contains(string(p.EvidenceDetail[0].Excerpt), "exit-code") {
		t.Errorf("issued evidence not attached to the plan: %+v", p.EvidenceDetail)
	}
}

func TestProposeActionRejectsTargets(t *testing.T) {
	cs, done := connect(t, testDeps(t))
	defer done()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "propose_action", Arguments: map[string]any{
		"kind": "service.restart", "params": map[string]string{"unit": "reboot.target"}, "rationale": "the logs said so",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("restarting reboot.target must be rejected")
	}
}

func TestProposeActionWithdrawsPlanIfTrailFails(t *testing.T) {
	deps := testDeps(t)
	// A trail that can't be written: its path is a directory.
	bad, _ := trail.Open(filepath.Join(t.TempDir(), "trail-is-a-dir"))
	os.MkdirAll(bad.Path, 0o700)
	bad.SetCheckpointFuncs(func(map[string]string) error { return nil }, func() ([]map[string]string, error) { return nil, nil })
	deps.Trail = bad
	cs, done := connect(t, deps)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "propose_action", Arguments: map[string]any{
		"kind": "service.restart", "params": map[string]string{"unit": "a.service"}, "rationale": "x",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error when the proposal can't be recorded")
	}
	entries, _ := deps.Plans.List()
	if len(entries) != 0 {
		t.Errorf("a plan without a proposal record was left behind: %+v", entries)
	}
}

func TestServerIconSentOnceAtInitialize(t *testing.T) {
	cs, done := connect(t, testDeps(t))
	defer done()

	info := cs.InitializeResult().ServerInfo
	if info == nil || len(info.Icons) != 1 || !strings.HasPrefix(info.Icons[0].Source, "data:image/png;base64,") {
		t.Fatalf("initialize should carry the icon as a PNG data URI: %+v", info)
	}

	// Tool results identify the server too (SEP-2575), but without the icon,
	// which would otherwise be repeated in every result.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "propose_action", Arguments: map[string]any{"kind": "nope", "params": map[string]string{}, "rationale": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := res.Meta[mcp.MetaKeyServerInfo].(map[string]any)
	if !ok {
		t.Fatalf("tool result should identify the server in _meta, got %v", res.Meta)
	}
	if got["name"] != "maat" {
		t.Errorf("server identity in _meta = %v", got)
	}
	if _, hasIcons := got["icons"]; hasIcons {
		t.Error("tool results must not repeat the icon")
	}
}

func TestFitKeepsRankedPrefix(t *testing.T) {
	items := []string{strings.Repeat("a", 50), strings.Repeat("b", 50), strings.Repeat("c", 50)}
	got, truncated := fit(items, obs.Budget{MaxItems: 10, MaxChars: 110})
	if len(got) != 2 || !truncated {
		t.Errorf("fit by chars = %d items (truncated=%v), want 2", len(got), truncated)
	}
	got, truncated = fit(items, obs.Budget{MaxItems: 1, MaxChars: 10000})
	if len(got) != 1 || !truncated {
		t.Errorf("fit by items = %d items, want 1", len(got))
	}
	got, _ = fit(items, obs.Budget{MaxItems: 10, MaxChars: 1})
	if len(got) != 1 {
		t.Errorf("the first item must always be kept, got %d", len(got))
	}
}

func TestProposeActionRejectsBadKind(t *testing.T) {
	cs, done := connect(t, testDeps(t))
	defer done()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "propose_action",
		Arguments: map[string]any{"kind": "rm.rf", "params": map[string]string{}, "rationale": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for an unknown kind")
	}
}

func decodeStructured(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
}
