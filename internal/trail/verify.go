package trail

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// VerifyResult summarizes an integrity check of the trail.
type VerifyResult struct {
	Records            int      `json:"records"`
	ChainOK            bool     `json:"chain_ok"`
	CheckpointsChecked int      `json:"checkpoints_checked"`
	CheckpointsOK      bool     `json:"checkpoints_ok"`
	Problems           []string `json:"problems,omitempty"`
}

// Verify recomputes the hash chain and, when the journal is readable, checks
// that the chain agrees with the checkpoints recorded there. A problem in either
// place means the trail was tampered with.
func (t *Trail) Verify(ctx context.Context) (VerifyResult, error) {
	recs, err := t.ReadAll()
	if err != nil {
		return VerifyResult{}, err
	}
	res := VerifyResult{Records: len(recs), ChainOK: true, CheckpointsOK: true}

	// Recompute the chain.
	byHash := map[string]Record{}
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
		byHash[fmt.Sprintf("%d", r.Seq)] = r
		prev = r.Hash
	}

	// Cross-check journal checkpoints, if we can read them.
	if t.checkpointReader != nil {
		cps, err := t.checkpointReader()
		if err != nil {
			res.Problems = append(res.Problems, "checkpoints not checked: "+err.Error())
		} else {
			seenSeqHash := map[string]string{}
			for _, cp := range cps {
				seq := cp["MAAT_TRAIL_HEAD"]
				hash := cp["MAAT_TRAIL_HASH"]
				if seq == "" || hash == "" {
					continue
				}
				res.CheckpointsChecked++
				// Two different hashes checkpointed for the same seq means the
				// file was rewritten after a checkpoint was taken.
				if prevHash, ok := seenSeqHash[seq]; ok && prevHash != hash {
					res.CheckpointsOK = false
					res.Problems = append(res.Problems, fmt.Sprintf("conflicting checkpoints for seq %s: %s vs %s", seq, short(prevHash), short(hash)))
				}
				seenSeqHash[seq] = hash
				if rec, ok := byHash[seq]; ok && rec.Hash != hash {
					res.CheckpointsOK = false
					res.Problems = append(res.Problems, fmt.Sprintf("checkpoint for seq %s expects hash %s but file has %s", seq, short(hash), short(rec.Hash)))
				}
			}
		}
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
// native protocol. It is best-effort: callers ignore the error.
func (t *Trail) journalCheckpoint(fields map[string]string) error {
	var b strings.Builder
	// MESSAGE should come first by convention.
	if msg, ok := fields["MESSAGE"]; ok {
		fmt.Fprintf(&b, "MESSAGE=%s\n", msg)
	}
	fmt.Fprintf(&b, "SYSLOG_IDENTIFIER=%s\n", t.JournalID)
	for k, v := range fields {
		if k == "MESSAGE" {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	cmd := exec.Command("logger", "--journald")
	cmd.Stdin = strings.NewReader(b.String())
	return cmd.Run()
}

// journalCheckpoints reads back maat's checkpoints from the journal.
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
		for _, key := range []string{"MAAT_TRAIL_HEAD", "MAAT_TRAIL_HASH", "MAAT_ACTOR"} {
			if raw, ok := entry[key]; ok {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					cp[key] = s
				}
			}
		}
		if len(cp) > 0 {
			cps = append(cps, cp)
		}
	}
	return cps, nil
}
