package trail

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newTestTrail returns a trail backed by a temp file, with in-memory checkpoint
// functions so tests don't need a journal.
func newTestTrail(t *testing.T) (*Trail, *[]map[string]string) {
	t.Helper()
	dir := t.TempDir()
	tr, err := Open(filepath.Join(dir, "trail.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints []map[string]string
	tr.checkpointer = func(fields map[string]string) error {
		cp := map[string]string{}
		for k, v := range fields {
			cp[k] = v
		}
		checkpoints = append(checkpoints, cp)
		return nil
	}
	tr.checkpointReader = func() ([]map[string]string, error) { return checkpoints, nil }
	return tr, &checkpoints
}

func TestAppendChainsAndVerifies(t *testing.T) {
	tr, _ := newTestTrail(t)
	for i := 0; i < 5; i++ {
		if _, err := tr.AppendAndCheckpoint(Record{Type: TypeObservation, Actor: "maat", Summary: "step"}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := tr.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.ChainOK || res.Records != 5 {
		t.Fatalf("expected a valid 5-record chain, got %+v", res)
	}
	if res.CheckpointsChecked == 0 || !res.CheckpointsOK {
		t.Errorf("checkpoints not verified: %+v", res)
	}
}

func TestSeqAndPrevLinkage(t *testing.T) {
	tr, _ := newTestTrail(t)
	a, _ := tr.Append(Record{Type: TypeProposal, Actor: "x"})
	b, _ := tr.Append(Record{Type: TypeApproval, Actor: "x"})
	if b.Seq != a.Seq+1 {
		t.Errorf("seq not incremented: %d then %d", a.Seq, b.Seq)
	}
	if b.Prev != a.Hash {
		t.Errorf("prev linkage broken: %s != %s", b.Prev, a.Hash)
	}
}

func TestTamperDetectedByChain(t *testing.T) {
	tr, _ := newTestTrail(t)
	for i := 0; i < 4; i++ {
		tr.Append(Record{Type: TypeObservation, Actor: "maat", Summary: "ok"})
	}
	// Tamper: edit the summary of record 2 directly in the file, leaving its
	// stored hash unchanged.
	recs, _ := tr.ReadAll()
	recs[1].Summary = "TAMPERED"
	rewriteFile(t, tr.Path, recs)

	res, _ := tr.Verify(context.Background())
	if res.ChainOK {
		t.Fatal("tampering with a record went undetected")
	}
	if len(res.Problems) == 0 {
		t.Error("expected a reported problem")
	}
}

func TestTamperDetectedByCheckpointAfterRewrite(t *testing.T) {
	tr, cps := newTestTrail(t)
	// Record and checkpoint three entries.
	for i := 0; i < 3; i++ {
		tr.AppendAndCheckpoint(Record{Type: TypeExecution, Actor: "maatctl", Summary: "did a thing"})
	}
	// Attacker rewrites the whole file into a clean, self-consistent chain
	// (different content, recomputed hashes) but cannot delete the journal
	// checkpoints that recorded the original head hashes.
	forged := forgeChain(3)
	rewriteRaw(t, tr.Path, forged)

	res, _ := tr.Verify(context.Background())
	if res.ChainOK && res.CheckpointsOK {
		t.Fatal("a full rewrite escaped both the chain and the checkpoints")
	}
	// The chain itself is internally consistent after forging, so detection must
	// come from the checkpoints.
	if res.CheckpointsOK {
		t.Errorf("checkpoint cross-check failed to catch the rewrite: %+v (checkpoints=%d)", res, len(*cps))
	}
}

func TestConcurrentAppendsSerialize(t *testing.T) {
	tr, _ := newTestTrail(t)
	const writers, each = 8, 5
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, err := tr.Append(Record{Type: TypeObservation, Actor: "w"}); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append failed: %v", err)
	}
	res, err := tr.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != writers*each {
		t.Fatalf("expected %d records, got %d", writers*each, res.Records)
	}
	if !res.ChainOK {
		t.Fatalf("concurrent appends broke the chain: %+v", res.Problems)
	}
}

func TestDeletionDetected(t *testing.T) {
	tr, _ := newTestTrail(t)
	for i := 0; i < 5; i++ {
		tr.Append(Record{Type: TypeObservation, Actor: "maat"})
	}
	recs, _ := tr.ReadAll()
	// Delete the middle record.
	recs = append(recs[:2], recs[3:]...)
	rewriteFile(t, tr.Path, recs)
	res, _ := tr.Verify(context.Background())
	if res.ChainOK {
		t.Fatal("deletion of a record went undetected")
	}
}

// helpers

func rewriteFile(t *testing.T, path string, recs []Record) {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rewriteRaw(t *testing.T, path string, recs []Record) {
	t.Helper()
	rewriteFile(t, path, recs)
}

// forgeChain builds an internally valid chain of n records whose content differs
// from any real trail (so the chain check passes, but recorded checkpoints of
// the real hashes will not match).
func forgeChain(n int) []Record {
	var recs []Record
	prev := "sha256:" + strings.Repeat("0", 64)
	for i := 0; i < n; i++ {
		r := Record{Seq: i + 1, Type: TypeObservation, Actor: "attacker", Summary: "nothing to see here", Prev: prev}
		h, _ := computeHash(r)
		r.Hash = h
		prev = h
		recs = append(recs, r)
	}
	return recs
}
