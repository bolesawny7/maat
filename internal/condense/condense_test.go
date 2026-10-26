package condense

import (
	"strings"
	"testing"
	"time"

	"github.com/bolesawny7/maat/internal/obs"
)

func TestRedactor(t *testing.T) {
	r := NewRedactor()
	cases := []struct {
		name        string
		in          string
		wantGone    []string // substrings that must not survive
		wantKept    []string // substrings that must survive
		wantAtLeast int
	}{
		{
			name:        "url password",
			in:          "connecting to postgres://admin:s3cr3tP@ss@db.internal:5432/app",
			wantGone:    []string{"s3cr3tP@ss"},
			wantKept:    []string{"postgres://admin", "db.internal"},
			wantAtLeast: 1,
		},
		{
			name:        "bearer token",
			in:          "GET /v1/x Authorization: Bearer abcdef0123456789ABCDEF",
			wantGone:    []string{"abcdef0123456789ABCDEF"},
			wantAtLeast: 1,
		},
		{
			name:        "aws key",
			in:          "env AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE set",
			wantGone:    []string{"AKIAIOSFODNN7EXAMPLE"},
			wantAtLeast: 1,
		},
		{
			name:        "key value secret",
			in:          `config: password = "hunter2hunter2" loaded`,
			wantGone:    []string{"hunter2hunter2"},
			wantKept:    []string{"password"},
			wantAtLeast: 1,
		},
		{
			name:        "github token",
			in:          "clone failed with ghp_0123456789abcdefABCDEF0123456789abcd",
			wantGone:    []string{"ghp_0123456789abcdefABCDEF0123456789abcd"},
			wantAtLeast: 1,
		},
		{
			name:        "benign line untouched",
			in:          "nginx: worker process 1234 started on 10.0.0.5",
			wantKept:    []string{"worker process 1234", "10.0.0.5"},
			wantAtLeast: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, n := r.Redact(tc.in)
			for _, s := range tc.wantGone {
				if strings.Contains(got, s) {
					t.Errorf("secret survived redaction: %q still in %q", s, got)
				}
			}
			for _, s := range tc.wantKept {
				if !strings.Contains(got, s) {
					t.Errorf("non-secret was lost: %q missing from %q", s, got)
				}
			}
			if n < tc.wantAtLeast {
				t.Errorf("redaction count = %d, want >= %d", n, tc.wantAtLeast)
			}
		})
	}
}

func TestRedactorDeterministic(t *testing.T) {
	r := NewRedactor()
	in := "token=abcdef123456 and password: longsecretvalue here"
	a, _ := r.Redact(in)
	b, _ := r.Redact(in)
	if a != b {
		t.Fatalf("redaction not deterministic: %q vs %q", a, b)
	}
}

func TestDrainClustersVariables(t *testing.T) {
	d := NewDrain()
	for i := 0; i < 50; i++ {
		d.Add("Accepted password for alice from 10.0.0." + itoa(i) + " port 22 ssh2")
	}
	for i := 0; i < 30; i++ {
		d.Add("Failed password for invalid user bob from 192.168.1." + itoa(i) + " port 2222 ssh2")
	}
	cs := d.Clusters()
	if len(cs) != 2 {
		t.Fatalf("expected 2 templates, got %d: %s", len(cs), templates(cs))
	}
	var accepted, failed *Cluster
	for _, c := range cs {
		if strings.Contains(c.Template(), "Accepted") {
			accepted = c
		}
		if strings.Contains(c.Template(), "Failed") {
			failed = c
		}
	}
	if accepted == nil || accepted.Count != 50 {
		t.Errorf("accepted cluster wrong: %+v", accepted)
	}
	if failed == nil || failed.Count != 30 {
		t.Errorf("failed cluster wrong: %+v", failed)
	}
	// The IP and port should be collapsed to wildcards.
	if !strings.Contains(accepted.Template(), wildcard) {
		t.Errorf("expected wildcard in template, got %q", accepted.Template())
	}
}

func TestDrainBoundsClusters(t *testing.T) {
	d := NewDrain()
	d.maxClusters = 50 // small cap for the test
	// Each message has five entirely distinct all-letter tokens, so no two
	// messages are similar enough to merge; every one would create a new cluster
	// if the cap didn't fold the excess into overflow.
	for i := 0; i < 300; i++ {
		msg := letters(i*5) + " " + letters(i*5+1) + " " + letters(i*5+2) + " " + letters(i*5+3) + " " + letters(i*5+4)
		d.Add(msg)
	}
	if len(d.Clusters()) > d.maxClusters+1 {
		t.Fatalf("clusters not bounded: got %d, cap %d (+1 overflow)", len(d.Clusters()), d.maxClusters)
	}
	// The overflow cluster must have absorbed the excess.
	var overflow *Cluster
	for _, c := range d.Clusters() {
		if c.Template() == "<overflow>" {
			overflow = c
		}
	}
	if overflow == nil || overflow.Count == 0 {
		t.Fatal("expected an overflow cluster to absorb excess messages")
	}
}

// letters returns a short all-letter token derived from i (no digits, so the
// maskers don't collapse it).
func letters(i int) string {
	const a = "abcdefghijklmnopqrstuvwxyz"
	if i == 0 {
		return "a"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{a[i%26]}, b...)
		i /= 26
	}
	return string(b)
}

func TestDrainSeparatesByLength(t *testing.T) {
	d := NewDrain()
	d.Add("disk full on /var")
	d.Add("link is down")
	if len(d.Clusters()) != 2 {
		t.Fatalf("messages of different lengths must not merge: %s", templates(d.Clusters()))
	}
}

func TestDrainDeterministic(t *testing.T) {
	msgs := []string{
		"service nginx failed with code 1",
		"service nginx failed with code 2",
		"started job 4567 for thing",
		"service sshd failed with code 7",
	}
	run := func() []string {
		d := NewDrain()
		for _, m := range msgs {
			d.Add(m)
		}
		return templatesList(d.Clusters())
	}
	a, b := run(), run()
	if strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatalf("Drain not deterministic:\n%v\n%v", a, b)
	}
}

func TestSummarizeRanksAndBudgets(t *testing.T) {
	base := time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)
	var recs []obs.LogRecord
	// A flood of low-severity noise.
	for i := 0; i < 1000; i++ {
		recs = append(recs, obs.LogRecord{
			Time: base.Add(time.Duration(i) * time.Second), Source: "journal",
			Unit: "noisy.service", Priority: obs.SevInfo,
			Message: "handled request id " + itoa(i),
		})
	}
	// A single critical line near the end.
	recs = append(recs, obs.LogRecord{
		Time: base.Add(2000 * time.Second), Source: "journal",
		Unit: "db.service", Priority: obs.SevCritical,
		Message: "database corruption detected on shard 9",
	})

	sum := Summarize(recs, nil, nil, obs.Budget{MaxItems: 5, MaxChars: 4000})
	if sum.TotalRecords != 1001 {
		t.Fatalf("TotalRecords = %d, want 1001", sum.TotalRecords)
	}
	if len(sum.Templates) == 0 {
		t.Fatal("no templates returned")
	}
	// The critical line must rank first despite being vastly outnumbered.
	if !strings.Contains(sum.Templates[0].Template, "corruption") {
		t.Errorf("critical line did not rank first; got %q", sum.Templates[0].Template)
	}
	if sum.Templates[0].MaxPriority != obs.SevCritical {
		t.Errorf("priority not propagated: %v", sum.Templates[0].MaxPriority)
	}
}

func TestSummarizeEvidenceIDStable(t *testing.T) {
	recs := []obs.LogRecord{
		{Time: time.Unix(1, 0), Source: "journal", Message: "alpha one"},
		{Time: time.Unix(2, 0), Source: "journal", Message: "alpha two"},
	}
	a := Summarize(recs, nil, nil, obs.Budget{})
	b := Summarize(recs, nil, nil, obs.Budget{})
	if a.Templates[0].EvidenceID != b.Templates[0].EvidenceID {
		t.Fatal("evidence IDs are not stable across runs")
	}
	if !strings.HasPrefix(a.Templates[0].EvidenceID, "ev_") {
		t.Errorf("unexpected evidence ID format: %q", a.Templates[0].EvidenceID)
	}
}

func TestSummarizeRedactsExamples(t *testing.T) {
	recs := []obs.LogRecord{
		{Time: time.Unix(1, 0), Source: "journal", Message: "login token=supersecretvalue123 ok"},
	}
	sum := Summarize(recs, nil, nil, obs.Budget{})
	if sum.Redactions == 0 {
		t.Fatal("expected at least one redaction")
	}
	if strings.Contains(string(sum.Templates[0].Example), "supersecretvalue123") {
		t.Errorf("secret leaked into example: %q", sum.Templates[0].Example)
	}
}

// helpers

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func templates(cs []*Cluster) string { return strings.Join(templatesList(cs), " || ") }

func templatesList(cs []*Cluster) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Template()
	}
	return out
}
