package buildinfo

import (
	"strings"
	"testing"
)

func TestStampedVersionWins(t *testing.T) {
	if got := Version("0.1.0"); got != "0.1.0" {
		t.Fatalf("Version(0.1.0) = %q", got)
	}
}

func TestUnstampedFallsBackToAModuleVersion(t *testing.T) {
	got := Version(Unstamped)
	if got == "" || got == "(devel)" || strings.HasPrefix(got, "v") {
		t.Fatalf("Version(Unstamped) = %q, want %q or a module version without the v", got, Unstamped)
	}
}
