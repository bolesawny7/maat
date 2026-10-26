package termsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextEscapesControlSequences(t *testing.T) {
	cases := map[string]string{
		"plain text":                  "plain text",
		"keeps\nnewlines\tand tabs":   "keeps\nnewlines\tand tabs",
		"clear\x1b[2Jscreen":          `clear\x1b[2Jscreen`,
		"up\x1b[3Aand overwrite\r":    `up\x1b[3Aand overwrite\x0d`,
		"bell\x07 and del\x7f":        `bell\x07 and del\x7f`,
		"c1 csi \u009b31m":            `c1 csi \u009b31m`,
		"bidi \u202eesrever\u202c":    `bidi \u202eesrever\u202c`,
		"isolate \u2066x\u2069":       `isolate \u2066x\u2069`,
		"zero\u200bwidth":             `zero\u200bwidth`,
		"bad utf8 \xff\xfe":           `bad utf8 \xff\xfe`,
		"unicode is fine: Ma'at, Θωθ": "unicode is fine: Ma'at, Θωθ",
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLineEscapesNewlines(t *testing.T) {
	if got := Line("a\nb\r\nc\td"); got != `a\x0ab\x0d\x0ac\x09d` {
		t.Errorf("Line = %q", got)
	}
}

func FuzzText(f *testing.F) {
	for _, s := range []string{"", "x", "\x1b[31mred", "\u202e", "\xff", "a\nb"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Line(s)
		if !utf8.ValidString(out) {
			t.Fatalf("output is not valid UTF-8: %q", out)
		}
		for _, r := range out {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || isInvisibleFormat(r) {
				t.Fatalf("control or format character %U survived in %q", r, out)
			}
		}
		if strings.Contains(Text(s), "\x1b") {
			t.Fatalf("escape survived Text: %q", s)
		}
	})
}
