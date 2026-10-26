package planstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bolesawny7/maat/internal/plan"
)

func newProposal(t *testing.T, unit string) *plan.Plan {
	t.Helper()
	p, err := plan.NewProposal("service.restart", plan.Params{"unit": unit}, []string{"ev_1a2b3c4d"}, "because", plan.Proposer{Client: "test"})
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
	saved, err := s.Save(p)
	if err != nil {
		t.Fatal(err)
	}
	got, loaded, err := s.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.Params["unit"] != "nginx.service" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if saved == "" || saved != loaded {
		t.Errorf("digest of the saved bytes %q differs from the digest of the loaded bytes %q", saved, loaded)
	}
}

func TestLoadDetectsEditedFile(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	p := newProposal(t, "nginx.service")
	saved, err := s.Save(p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, p.ID+".json")
	data, _ := os.ReadFile(path)

	// A cosmetic edit that still validates changes the digest, so the trail's
	// proposal record no longer matches.
	edited := strings.Replace(string(data), `"because"`, `"because, trust me"`, 1)
	os.WriteFile(path, []byte(edited), 0o600)
	_, digest, err := s.Load(p.ID)
	if err != nil {
		t.Fatalf("a rationale-only edit should still load: %v", err)
	}
	if digest == saved {
		t.Error("digest did not change after the file was edited")
	}

	// Removing the safety steps must fail validation outright.
	gutted := strings.Replace(string(data), `"equals": "failed"`, `"equals": "inactive"`, 1)
	os.WriteFile(path, []byte(gutted), 0o600)
	if _, _, err := s.Load(p.ID); err == nil {
		t.Error("a plan with edited preconditions loaded")
	}
}

func TestLoadRejectsMismatchedID(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	a := newProposal(t, "a.service")
	b := newProposal(t, "b.service")
	s.Save(b)
	// Put b's content under a's file name.
	data, _ := os.ReadFile(filepath.Join(s.Dir, b.ID+".json"))
	os.WriteFile(filepath.Join(s.Dir, a.ID+".json"), data, 0o600)
	if _, _, err := s.Load(a.ID); err == nil {
		t.Error("a plan file whose content names a different plan id loaded")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plans")
	s, _ := Open(dir)
	p := newProposal(t, "a.service")
	if _, err := s.Save(p); err != nil {
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

func TestListSortedAndReportsInvalid(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	for _, u := range []string{"a.service", "b.service", "c.service"} {
		if _, err := s.Save(newProposal(t, u)); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(s.Dir, "ffffffff-ffff-7fff-bfff-ffffffffffff.json"), []byte("{not json"), 0o600)

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries (3 valid, 1 broken), got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].ID > entries[i].ID {
			t.Errorf("plans not sorted by id: %s before %s", entries[i-1].ID, entries[i].ID)
		}
	}
	last := entries[len(entries)-1]
	if last.Err == nil || last.Plan != nil {
		t.Errorf("a broken plan file must be listed with its error, got %+v", last)
	}
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	for _, bad := range []string{"../etc/passwd", "a/b", "..", "a.b", ""} {
		if _, _, err := s.Load(bad); err == nil {
			t.Errorf("Load(%q) should be rejected", bad)
		}
	}
}

func TestListEmptyDir(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "plans"))
	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no plans, got %d", len(entries))
	}
}
