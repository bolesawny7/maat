// Package trail is maat's tamper-evident audit log. Each record is one line of
// JSON, hash-chained to the previous record, so any edit, deletion, or
// reordering breaks the chain. Checkpoints of the chain head are written to the
// systemd journal, which unprivileged processes can append to but not rewrite,
// giving a second, independent record that detects wholesale rewrites of the
// file.
package trail

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Entry types.
const (
	TypeObservation  = "observation"
	TypeProposal     = "proposal"
	TypeApproval     = "approval"
	TypeExecution    = "execution"
	TypeVerification = "verification"
	TypeRollback     = "rollback"
	TypeRejection    = "rejection"
	TypeCheckpoint   = "checkpoint"
)

// Record is one audit entry. Only non-secret digests and short summaries are
// stored; raw log text never enters the trail.
type Record struct {
	Seq     int       `json:"seq"`
	Ts      time.Time `json:"ts"`
	Type    string    `json:"type"`
	Actor   string    `json:"actor"`
	Plan    string    `json:"plan,omitempty"`
	Digest  string    `json:"digest,omitempty"`
	Summary string    `json:"summary,omitempty"`
	Prev    string    `json:"prev"`
	Hash    string    `json:"hash"`
}

// Trail is an append-only hash chain stored at Path.
type Trail struct {
	Path             string
	JournalID        string // SYSLOG_IDENTIFIER used for checkpoints
	checkpointer     func(fields map[string]string) error
	checkpointReader func() ([]map[string]string, error)
}

// Open returns a Trail at path, creating the parent directory if needed.
func Open(path string) (*Trail, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	t := &Trail{Path: path, JournalID: "maat"}
	t.checkpointer = t.journalCheckpoint
	t.checkpointReader = t.journalCheckpoints
	return t, nil
}

// SetCheckpointFuncs overrides how checkpoints are written and read. maat uses
// this to disable journal checkpoints on hosts without a journal, and tests use
// it to supply in-memory fakes. Passing nil for either keeps the default.
func (t *Trail) SetCheckpointFuncs(writer func(map[string]string) error, reader func() ([]map[string]string, error)) {
	if writer != nil {
		t.checkpointer = writer
	}
	if reader != nil {
		t.checkpointReader = reader
	}
}

// hashInput is the canonical form hashed for a record: the record with an empty
// Hash field, marshaled compactly. Because Prev is part of the record, the hash
// binds each entry to its predecessor.
func hashInput(r Record) ([]byte, error) {
	r.Hash = ""
	return json.Marshal(r)
}

func computeHash(r Record) (string, error) {
	data, err := hashInput(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DigestOf returns a content digest suitable for recording in the trail, e.g.
// the digest of a plan file so the trail binds to exact content without copying
// it.
func DigestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Append adds a record under an exclusive lock, chaining it to the current head.
// It returns the stored record (with Seq, Prev, and Hash filled in).
func (t *Trail) Append(rec Record) (Record, error) {
	stored, _, err := t.append(rec)
	return stored, err
}

// append is Append that also returns the trail's ID: the hash of its first
// record. The ID ties journal checkpoints to this particular trail, so a new
// trail (after a reinstall, say) is told apart from a tampered one.
func (t *Trail) append(rec Record) (Record, string, error) {
	f, err := os.OpenFile(t.Path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return Record{}, "", err
	}
	defer f.Close()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return Record{}, "", fmt.Errorf("lock trail: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	first, last, _, err := readEnds(f)
	if err != nil {
		return Record{}, "", err
	}

	rec.Seq = last.Seq + 1
	rec.Prev = last.Hash
	if rec.Prev == "" {
		rec.Prev = "sha256:" + strings.Repeat("0", 64) // genesis
	}
	if rec.Ts.IsZero() {
		rec.Ts = time.Now().UTC()
	}
	h, err := computeHash(rec)
	if err != nil {
		return Record{}, "", err
	}
	rec.Hash = h

	line, err := json.Marshal(rec)
	if err != nil {
		return Record{}, "", err
	}
	if _, err := f.Seek(0, 2); err != nil {
		return Record{}, "", err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return Record{}, "", err
	}
	if err := f.Sync(); err != nil {
		return Record{}, "", err
	}
	trailID := first.Hash
	if rec.Seq == 1 {
		trailID = rec.Hash
	}
	return rec, trailID, nil
}

// AppendAndCheckpoint appends a record and then writes a journal checkpoint of
// the new head. The checkpoint is best-effort: a failure to reach the journal
// does not fail the append, because the hash chain already protects integrity.
// The returned error is non-nil only if the append itself failed.
func (t *Trail) AppendAndCheckpoint(rec Record) (Record, error) {
	stored, trailID, err := t.append(rec)
	if err != nil {
		return stored, err
	}
	_ = t.checkpoint(trailID, stored)
	return stored, nil
}

// checkpoint writes the chain head (trail ID, seq and hash) to the journal.
func (t *Trail) checkpoint(trailID string, head Record) error {
	if t.checkpointer == nil {
		return nil
	}
	return t.checkpointer(map[string]string{
		"MESSAGE":         fmt.Sprintf("maat trail checkpoint seq=%d", head.Seq),
		"MAAT_TRAIL_ID":   trailID,
		"MAAT_TRAIL_HEAD": strconv.Itoa(head.Seq),
		"MAAT_TRAIL_HASH": head.Hash,
		"MAAT_ACTOR":      head.Actor,
	})
}

// readEnds returns the first and last records in the open file and the count
// of records.
func readEnds(f *os.File) (first, last Record, count int, err error) {
	if _, err := f.Seek(0, 0); err != nil {
		return Record{}, Record{}, 0, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return Record{}, Record{}, count, fmt.Errorf("corrupt trail line %d: %w", count+1, err)
		}
		if count == 0 {
			first = r
		}
		last = r
		count++
	}
	if err := sc.Err(); err != nil {
		return Record{}, Record{}, count, err
	}
	return first, last, count, nil
}

// ReadAll returns every record in order.
func (t *Trail) ReadAll() ([]Record, error) {
	f, err := os.Open(t.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return recs, fmt.Errorf("corrupt trail line: %w", err)
		}
		recs = append(recs, r)
	}
	return recs, sc.Err()
}
