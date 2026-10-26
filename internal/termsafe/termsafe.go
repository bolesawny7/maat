// Package termsafe renders untrusted text for a human's terminal. Plan files,
// log lines and unit descriptions can carry ANSI escape sequences (which can move
// the cursor and repaint lines a reviewer already read), bidirectional overrides
// (which reorder what is displayed), and invalid UTF-8. Every such character is
// replaced by a visible escape, so what the reviewer sees is what the bytes say.
package termsafe

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Text makes s safe to print, keeping newlines and tabs.
func Text(s string) string { return render(s, false) }

// Line makes s safe to print on a single line: newlines and carriage returns
// are escaped too, so a value cannot fake extra lines of output.
func Line(s string) string { return render(s, true) }

func render(s string, oneLine bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n' || r == '\t':
			if oneLine {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f, isInvisibleFormat(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// isInvisibleFormat reports bidirectional controls, zero-width characters and
// line or paragraph separators: characters that change how surrounding text is
// displayed without being visible themselves.
func isInvisibleFormat(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f: // ALM, LRM, RLM
		return true
	case r >= 0x200b && r <= 0x200d: // zero-width space, non-joiner, joiner
		return true
	case r >= 0x2028 && r <= 0x202e: // line/paragraph separators, LRE..RLO
		return true
	case r >= 0x2066 && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	case r == 0xfeff: // zero-width no-break space
		return true
	}
	return false
}
