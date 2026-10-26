package trail

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// VerifyResult summarizes an integrity check of the trail.
//
// Problems are proof that this trail was altered: an edited, deleted, reordered
// or truncated record. Warnings are things a person should look at but that
// have innocent explanations too, such as checkpoints from an earlier trail
// (reinstalling the snap deletes its data) or a journal that could not be read.
type VerifyResult struct {
	Records            int      `json:"records"`
	TrailID            string   `json:"trail_id,omitempty"`
	ChainOK            bool     `json:"chain_ok"`
	CheckpointsChecked int      `json:"checkpoints_checked"`
	CheckpointsOK      bool     `json:"checkpoints_ok"`
	Problems           []string `json:"problems,omitempty"`
	Warnings           []string `json:"warnings,omitempty"`
}

// OK reports whether no tampering was detected.
func (r VerifyResult) OK() bool { return r.ChainOK && r.CheckpointsOK }

// Verify recomputes the hash chain and, when the journal is readable, checks it
// against the checkpoints this user recorded there. Only checkpoints whose
// journald-assigned _UID matches the current user are considered, because any
// local user can write journal entries with maat's identifier and field names.
func (t *Trail) Verify(ctx context.Context) (VerifyResult, error) {
	recs, err := t.ReadAll()
	if err != nil {
		return VerifyResult{}, err
	}
	res := VerifyResult{Records: len(recs), ChainOK: true, CheckpointsOK: true}
	if len(recs) > 0 {
		res.TrailID = recs[0].Hash
	}

	// Recompute the chain.
	bySeq := map[int]Record{}
	prev := "sha256:" + strings.Repeat("0", 64)
	for i, r := range recs {
		if r.Seq != i+1 {
			res.ChainOK = false
			res.Problems = append(res.Problems, fmt.Sprintf("record %d has seq %d, expected %d", i+1, r.Seq, i+1))
		}
		if r.Prev != prev {
			res.ChainOK = false
			res.Problems = append(res.Problems, fmt.Sprintf("record %d prev %s does not link to previous hash %s", r.Seq, short(r.Prev), short(prev)))
		}
		want, err := computeHash(r)
		if err != nil {
			return res, err
		}
		if want != r.Hash {
			res.ChainOK = false
			res.Problems = append(res.Problems, fmt.Sprintf("record %d hash mismatch: content hashes to %s but stored hash is %s", r.Seq, short(want), short(r.Hash)))
		}
		bySeq[r.Seq] = r
		prev = r.Hash
	}

	if t.checkpointReader == nil {
		return res, nil
	}
	cps, err := t.checkpointReader()
	if err != nil {
		res.Warnings = append(res.Warnings, "journal checkpoints were not checked: "+err.Error())
		return res, nil
	}

	uid := strconv.Itoa(os.Getuid())
	type otherTrail struct {
		maxSeq int
		count  int
	}
	others := map[string]*otherTrail{}
	seenSeqHash := map[int]string{}
	for _, cp := range cps {
		if u := cp["_UID"]; u != "" && u != uid {
			continue
		}
		seq, err := strconv.Atoi(cp["MAAT_TRAIL_HEAD"])
		hash := cp["MAAT_TRAIL_HASH"]
		if err != nil || seq < 1 || hash == "" {
			continue
		}
		if id := cp["MAAT_TRAIL_ID"]; id != res.TrailID || id == "" {
			o := others[id]
			if o == nil {
				o = &otherTrail{}
				others[id] = o
			}
			o.count++
			if seq > o.maxSeq {
				o.maxSeq = seq
			}
			continue
		}
		res.CheckpointsChecked++
		// Two different hashes checkpointed for the same seq means the file was
		// rewritten after a checkpoint was taken.
		if prevHash, ok := seenSeqHash[seq]; ok && prevHash != hash {
			res.CheckpointsOK = false
			res.Problems = append(res.Problems, fmt.Sprintf("conflicting checkpoints for record %d: %s vs %s", seq, short(prevHash), short(hash)))
		}
		seenSeqHash[seq] = hash
		rec, ok := bySeq[seq]
		switch {
		case !ok && seq > len(recs):
			res.CheckpointsOK = false
			res.Problems = append(res.Problems, fmt.Sprintf("the journal has a checkpoint for record %d, but the trail ends at record %d: records were removed from the end", seq, len(recs)))
		case ok && rec.Hash != hash:
			res.CheckpointsOK = false
			res.Problems = append(res.Problems, fmt.Sprintf("checkpoint for record %d expects hash %s but the file has %s", seq, short(hash), short(rec.Hash)))
		}
	}

	ids := make([]string, 0, len(others))
	for id := range others {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := others[id]
		name := "trail " + short(id)
		if id == "" {
			name = "an unidentified trail"
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"the journal holds %d checkpoint(s) of %s that is not this file (up to record %d). This is expected if maat's data was reset, for example by removing and reinstalling the snap; otherwise this trail file may have been replaced.",
			o.count, name, o.maxSeq))
	}
	return res, nil
}

func short(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 10 {
		return h[:10]
	}
	return h
}

// journalCheckpoint writes a structured journal entry via `logger --journald`,
// which reads KEY=value field lines from stdin. This avoids cgo and the journal
// native protocol, and works from inside the strict snap. It is best-effort:
// callers ignore the error.
func (t *Trail) journalCheckpoint(fields map[string]string) error {
	var b strings.Builder
	// MESSAGE should come first by convention.
	if msg, ok := fields["MESSAGE"]; ok {
		fmt.Fprintf(&b, "MESSAGE=%s\n", msg)
	}
	fmt.Fprintf(&b, "SYSLOG_IDENTIFIER=%s\n", t.JournalID)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "MESSAGE" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, fields[k])
	}
	cmd := exec.Command("logger", "--journald")
	cmd.Stdin = strings.NewReader(b.String())
	return cmd.Run()
}

// journalCheckpoints reads back maat's checkpoints from the journal, with the
// trusted _UID field journald attached to each.
func (t *Trail) journalCheckpoints() ([]map[string]string, error) {
	cmd := exec.Command("journalctl", "-t", t.JournalID, "--output=json", "--no-pager")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var cps []map[string]string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		cp := map[string]string{}
		for _, key := range []string{"MAAT_TRAIL_ID", "MAAT_TRAIL_HEAD", "MAAT_TRAIL_HASH", "MAAT_ACTOR", "_UID"} {
			if raw, ok := entry[key]; ok {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					cp[key] = s
				}
			}
		}
		if cp["MAAT_TRAIL_HEAD"] != "" {
			cps = append(cps, cp)
		}
	}
	return cps, nil
}
