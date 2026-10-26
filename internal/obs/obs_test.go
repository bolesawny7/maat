package obs

import (
	"testing"
	"time"
)

func TestSeverityString(t *testing.T) {
	if SevError.String() != "error" || SevInfo.String() != "info" {
		t.Error("severity names wrong")
	}
	if Severity(99).String() != "unknown" {
		t.Error("out-of-range severity should be unknown")
	}
}

func TestSeverityAtLeast(t *testing.T) {
	if !SevError.AtLeast(SevWarning) {
		t.Error("error should be at least as urgent as warning")
	}
	if SevInfo.AtLeast(SevWarning) {
		t.Error("info is less urgent than warning")
	}
	if !SevWarning.AtLeast(SevWarning) {
		t.Error("warning should satisfy at-least-warning")
	}
}

func TestEvidenceIDStableAndFormatted(t *testing.T) {
	a := EvidenceID("journal", "some template")
	b := EvidenceID("journal", "some template")
	c := EvidenceID("journal", "other template")
	if a != b {
		t.Error("evidence IDs must be stable for the same parts")
	}
	if a == c {
		t.Error("different parts should give different IDs")
	}
	if len(a) != len("ev_")+8 || a[:3] != "ev_" {
		t.Errorf("unexpected ID format: %q", a)
	}
}

func TestBudgetWithDefaults(t *testing.T) {
	b := Budget{}.WithDefaults()
	if b.MaxItems != DefaultMaxItems || b.MaxChars != DefaultMaxChars {
		t.Errorf("defaults not applied: %+v", b)
	}
	custom := Budget{MaxItems: 5, MaxChars: 100}.WithDefaults()
	if custom.MaxItems != 5 || custom.MaxChars != 100 {
		t.Errorf("custom values should be preserved: %+v", custom)
	}
}

func TestSortRecordsByTime(t *testing.T) {
	base := time.Unix(1000, 0)
	recs := []LogRecord{
		{Time: base.Add(2 * time.Second), Cursor: "c"},
		{Time: base, Cursor: "a"},
		{Time: base.Add(time.Second), Cursor: "b"},
		{Time: base, Cursor: "a0"}, // same time as "a"; cursor breaks the tie
	}
	SortRecordsByTime(recs)
	if !recs[0].Time.Equal(base) || recs[0].Cursor != "a" {
		t.Errorf("earliest-with-smallest-cursor should be first: %+v", recs[0])
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].Time.Before(recs[i-1].Time) {
			t.Errorf("not sorted ascending at %d", i)
		}
	}
}
