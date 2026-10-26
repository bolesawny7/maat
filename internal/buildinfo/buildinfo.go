// Package buildinfo works out which version a maat binary is.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Unstamped is the version of a binary built without
// -ldflags "-X main.version=...".
const Unstamped = "0.0.0-dev"

// Version returns stamped, unless the binary wasn't stamped. Then it returns
// the module version Go recorded, which is the tag for
// `go install github.com/bolesawny7/maat/cmd/maatctl@v0.1.0` and a
// pseudo-version for a build in a git checkout.
func Version(stamped string) string {
	if stamped != Unstamped {
		return stamped
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return stamped
}
