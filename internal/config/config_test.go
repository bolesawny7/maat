package config

import (
	"path/filepath"
	"testing"
)

func TestDataDirPrecedence(t *testing.T) {
	// MAAT_DATA wins over everything.
	t.Setenv("MAAT_DATA", "/custom/data")
	t.Setenv("SNAP_USER_COMMON", "/snap/common")
	if got := DataDir(); got != "/custom/data" {
		t.Errorf("MAAT_DATA should win, got %q", got)
	}
}

func TestDataDirSnapUserCommon(t *testing.T) {
	t.Setenv("MAAT_DATA", "")
	t.Setenv("SNAP_USER_COMMON", "/home/u/snap/maat/common")
	if got := DataDir(); got != "/home/u/snap/maat/common" {
		t.Errorf("SNAP_USER_COMMON should be used, got %q", got)
	}
}

func TestDataDirXDGFallback(t *testing.T) {
	t.Setenv("MAAT_DATA", "")
	t.Setenv("SNAP_USER_COMMON", "")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	// Point HOME at an empty temp dir so the ~/snap/maat/common bridge doesn't match.
	t.Setenv("HOME", t.TempDir())
	want := filepath.Join("/xdg/state", "maat")
	if got := DataDir(); got != want {
		t.Errorf("XDG fallback: got %q, want %q", got, want)
	}
}

func TestDerivedPaths(t *testing.T) {
	t.Setenv("MAAT_DATA", "/d")
	if PlansDir() != "/d/plans" {
		t.Errorf("PlansDir = %q", PlansDir())
	}
	if TrailPath() != "/d/trail/trail.jsonl" {
		t.Errorf("TrailPath = %q", TrailPath())
	}
	if ConfigPath() != "/d/config.yaml" {
		t.Errorf("ConfigPath = %q", ConfigPath())
	}
}

func TestLoadMissingConfigIsZero(t *testing.T) {
	t.Setenv("MAAT_DATA", t.TempDir()) // empty dir, no config.yaml
	c, err := Load()
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if len(c.RedactExtra) != 0 || c.MaxItems != 0 {
		t.Errorf("expected zero config, got %+v", c)
	}
}
