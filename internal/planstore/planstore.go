// Package planstore persists proposed plans as immutable JSON files shared
// between the server (which writes them) and maatctl (which reads and executes
// them). Plans are never modified after creation; their outcome lives in the
// audit trail, which keeps the proposal and what happened to it cleanly
// separated.
package planstore

import (
	"crypto/sha256"
	"encoding/hex"
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

// Entry is one plan file as found on disk. Err is set when the file could not be
// read or failed validation; such entries are reported, not hidden, because a
// plan that stopped validating may have been tampered with.
type Entry struct {
	ID     string
	Plan   *plan.Plan
	Digest string
	Err    error
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

// Digest returns the content digest the trail records for plan file bytes.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Save writes a plan atomically (write to a temp file, then rename) and returns
// the digest of the exact bytes written.
func (s *Store) Save(p *plan.Plan) (string, error) {
	path, err := s.path(p.ID)
	if err != nil {
		return "", err
	}
	data, err := p.Marshal()
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return Digest(data), nil
}

// Load reads one plan by ID, validates it, and returns it with the digest of the
// bytes it was parsed from. The file is read once; callers act only on the
// returned copy, so a later change to the file cannot alter what they execute.
func (s *Store) Load(id string) (*plan.Plan, string, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	digest := Digest(data)
	p, err := plan.Unmarshal(data)
	if err != nil {
		return nil, digest, fmt.Errorf("plan %s is not valid JSON: %w", id, err)
	}
	if p.ID != id {
		return nil, digest, fmt.Errorf("plan file %s.json contains plan id %q", id, p.ID)
	}
	if err := p.Validate(); err != nil {
		return nil, digest, fmt.Errorf("plan %s failed validation: %w", id, err)
	}
	return p, digest, nil
}

// List returns every plan file, newest last (UUIDv7 sorts by time), including
// files that fail to load, with the reason.
func (s *Store) List() ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range dirEntries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	sort.Strings(ids)
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		p, digest, err := s.Load(id)
		out = append(out, Entry{ID: id, Plan: p, Digest: digest, Err: err})
	}
	return out, nil
}
