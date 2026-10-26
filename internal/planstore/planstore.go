// Package planstore persists proposed plans as immutable JSON files shared
// between the server (which writes them) and maatctl (which reads and executes
// them). Plans are never modified after creation; their outcome lives in the
// audit trail, which keeps the proposal and what happened to it cleanly
// separated.
package planstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bolesawny7/maat/internal/plan"
)

// Store is a directory of plan files.
type Store struct {
	Dir string
}

// Open returns a Store at dir, creating it if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) path(id string) (string, error) {
	// Defend against path traversal: a plan ID is a UUID, so it must not contain
	// separators or dots.
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return "", fmt.Errorf("invalid plan id %q", id)
	}
	return filepath.Join(s.Dir, id+".json"), nil
}

// Save writes a plan atomically (write to a temp file, then rename).
func (s *Store) Save(p *plan.Plan) error {
	path, err := s.path(p.ID)
	if err != nil {
		return err
	}
	data, err := p.Marshal()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads one plan by ID and validates it.
func (s *Store) Load(id string) (*plan.Plan, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := plan.Unmarshal(data)
	if err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("plan %s failed validation: %w", id, err)
	}
	return p, nil
}

// List returns all stored plans, newest last (UUIDv7 sorts by time).
func (s *Store) List() ([]*plan.Plan, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	sort.Strings(ids)
	var out []*plan.Plan
	for _, id := range ids {
		p, err := s.Load(id)
		if err != nil {
			continue // skip unreadable/invalid files rather than failing the list
		}
		out = append(out, p)
	}
	return out, nil
}
