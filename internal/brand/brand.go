// Package brand carries maat's logo into the places a person meets it outside
// a web page: a banner in the terminal, and the icon an MCP client can show
// next to the server. Both are generated from the same geometry as the SVG
// logo by scripts/make_logo.py.
package brand

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// feather is the logo drawn in braille: each character is a 2x4 grid of dots.
//
//go:embed feather.txt
var feather string

//go:embed maat-icon-64.png
var iconPNG []byte

// IconDataURI returns the app icon as a PNG data URI. The confined server has
// no network, so the icon travels inside the MCP initialize response rather
// than as a link a client would fetch.
func IconDataURI() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(iconPNG)
}

// IsTerminal reports whether f is a terminal (a character device), as opposed
// to a pipe or a file.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Banner writes the feather with the given lines of text beside it, but only
// when f is a terminal, so pipes, files, JSON output and the MCP channel never
// receive it.
func Banner(f *os.File, lines ...string) {
	if IsTerminal(f) {
		writeBanner(f, lines)
	}
}

// writeBanner lays the text out beside the body of the feather, below its curl.
func writeBanner(w io.Writer, text []string) {
	art := strings.Split(strings.TrimRight(feather, "\n"), "\n")
	width := 0
	for _, line := range art {
		width = max(width, utf8.RuneCountInString(line))
	}
	const firstTextRow = 2
	for i, line := range art {
		if j := i - firstTextRow; j >= 0 && j < len(text) {
			line += strings.Repeat(" ", width-utf8.RuneCountInString(line)+4) + text[j]
		}
		fmt.Fprintln(w, "  "+line)
	}
	fmt.Fprintln(w)
}
