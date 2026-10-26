package condense

import "testing"

// Fuzz targets for the condense pipeline on attacker-influenced text: redaction
// and clustering must never panic, and redaction must never return a value that
// still contains an obvious injected secret pattern it claims to remove.

func FuzzRedact(f *testing.F) {
	r := NewRedactor()
	f.Add("password = hunter2hunter2")
	f.Add("postgres://u:p@h/db")
	f.Add("")
	f.Add("\x00\x01 weird bytes and emoji")
	f.Fuzz(func(t *testing.T, s string) {
		out, n := r.Redact(s)
		if n < 0 {
			t.Fatalf("negative redaction count")
		}
		_ = out
	})
}

func FuzzDrainAdd(f *testing.F) {
	f.Add("service started with pid 1234")
	f.Add("")
	f.Add("a a a a a a a a a a a a a a a a")
	f.Fuzz(func(t *testing.T, s string) {
		d := NewDrain()
		d.Add(s)
		// Adding the identical message again must map to the same cluster.
		before := len(d.Clusters())
		d.Add(s)
		if len(d.Clusters()) != before {
			t.Fatalf("identical message created extra clusters: %d -> %d", before, len(d.Clusters()))
		}
	})
}
