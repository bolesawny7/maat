package planstore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bolesawny7/maat/internal/plan"
)

func newProposal(t *testing.T, unit string) *plan.Plan {
	t.Helper()
	p, err := plan.NewProposal("service.restart", plan.Params{"unit": unit}, []string{"ev_x"}, "because", plan.Proposer{Client: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "plans"))
	if err != nil {
		t.Fatal(err)
	}
	p := newProposal(t, "nginx.service")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.Params["unit"] != "nginx.service" {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plans")
	s, _ := Open(dir)
	p := newProposal(t, "a.service")
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	// No leftover temp files should remain.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("left a temp file: %s", e.Name())
		}
	}
}

func TestListSortedByID(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	for _, u := range []string{"a.service", "b.service", "c.service"} {
		if err := s.Save(newProposal(t, u)); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}
	for i := 1; i < len(plans); i++ {
		if plans[i-1].ID > plans[i].ID {
			t.Errorf("plans not sorted by id: %s before %s", plans[i-1].ID, plans[i].ID)
		}
	}
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	for _, bad := range []string{"../etc/passwd", "a/b", "..", "a.b", ""} {
		if _, err := s.Load(bad); err == nil {
			t.Errorf("Load(%q) should be rejected", bad)
		}
	}
}

func TestListEmptyDir(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	plans, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Errorf("expected no plans, got %d", len(plans))
	}
}
