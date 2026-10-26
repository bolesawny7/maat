package main

import (
	"testing"
	"time"
)

func TestAggregate(t *testing.T) {
	scores := []Score{
		{Agent: "x", Expect: "diagnose_only", DiagnosisOK: true, EvidenceOK: true, RestraintOK: true, ToolCalls: 5},
		{Agent: "x", Expect: "diagnose_only", DiagnosisOK: false, RestraintOK: true, Adversarial: true, Hijacked: false, ToolCalls: 6},
		{Agent: "x", Expect: "fix", DiagnosisOK: true, FixOK: true, ToolCalls: 7},
		{Agent: "x", Error: "boom"},
	}
	a := aggregate(scores)
	if a.diagN != 3 || a.diagOK != 2 {
		t.Errorf("diagnosis agg wrong: %d/%d", a.diagOK, a.diagN)
	}
	if a.errs != 1 {
		t.Errorf("errors agg wrong: %d", a.errs)
	}
	if a.fixN != 1 || a.fixOK != 1 {
		t.Errorf("fix agg wrong: %d/%d", a.fixOK, a.fixN)
	}
	if a.resN != 2 || a.resOK != 2 {
		t.Errorf("restraint agg wrong: %d/%d", a.resOK, a.resN)
	}
	if a.advN != 1 || a.resisted != 1 {
		t.Errorf("injection agg wrong: resisted %d/%d", a.resisted, a.advN)
	}
}

func TestFracAndAvg(t *testing.T) {
	if frac(2, 8) != "2/8" || frac(0, 0) != "n/a" {
		t.Error("frac wrong")
	}
	if avg(18, 3) != "6.0" || avg(1, 0) != "n/a" {
		t.Error("avg wrong")
	}
}

func TestBackoffBounded(t *testing.T) {
	if backoff(1) != time.Second {
		t.Errorf("first backoff should be 1s, got %v", backoff(1))
	}
	if backoff(2) != 2*time.Second {
		t.Errorf("second backoff should be 2s, got %v", backoff(2))
	}
	// Must cap at 10s however large the attempt.
	if backoff(50) > 10*time.Second {
		t.Errorf("backoff must be capped at 10s, got %v", backoff(50))
	}
}
