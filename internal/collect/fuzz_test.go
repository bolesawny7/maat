package collect

import (
	"strings"
	"testing"
)

// These fuzz targets assert that the parsers for attacker-influenced input never
// panic, however malformed the input. This is the T3 defence from the security
// model: a malicious log line must not be able to crash a parser.

func FuzzParseJournalJSON(f *testing.F) {
	f.Add(`{"MESSAGE":"hi","PRIORITY":"3","__REALTIME_TIMESTAMP":"1761421200000000"}`)
	f.Add(`{"MESSAGE":[72,105],"_TRANSPORT":"kernel"}`)
	f.Add("not json\n{partial")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = ParseJournalJSON(strings.NewReader(s))
	})
}

func FuzzParseAppArmorDenial(f *testing.F) {
	f.Add(`apparmor="DENIED" operation="open" profile="p" name="/x"`)
	f.Add(`random text`)
	f.Add(`apparmor="DENIED" =broken "unterminated`)
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = ParseAppArmorDenial(s)
	})
}

func FuzzParseMountinfo(f *testing.F) {
	f.Add("28 1 259:2 / / rw,relatime shared:1 - ext4 /dev/vda2 rw")
	f.Add("garbage - - -")
	f.Add("1 2 3")
	f.Fuzz(func(t *testing.T, s string) {
		_ = ParseMountinfo(s)
	})
}

func FuzzParseProcNet(f *testing.F) {
	f.Add("header\n   0: 0100007F:1538 00000000:0000 0A x x x x x 101 0 12345")
	f.Add("")
	f.Add("   0: ZZZ:ZZZ x")
	f.Fuzz(func(t *testing.T, s string) {
		_ = parseProcNet("tcp", s)
	})
}

func FuzzParseStat(f *testing.F) {
	f.Add("1234 (proc) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14")
	f.Add("1 () R")
	f.Add("no parens here")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseStat(s)
	})
}
