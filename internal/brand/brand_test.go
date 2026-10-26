package brand

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFeatherArtIsBraille(t *testing.T) {
	lines := strings.Split(strings.TrimRight(feather, "\n"), "\n")
	if len(lines) < 6 || len(lines) > 10 {
		t.Fatalf("the banner feather should be 6 to 10 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if utf8.RuneCountInString(line) > 8 {
			t.Errorf("line %d is too wide for a banner: %q", i, line)
		}
		for _, r := range line {
			if r != ' ' && (r < 0x2800 || r > 0x28ff) {
				t.Errorf("line %d has a non-braille character %U", i, r)
			}
		}
	}
}

func TestBannerLayout(t *testing.T) {
	var b bytes.Buffer
	writeBanner(&b, []string{"M A A T", "second line"})
	out := strings.Split(b.String(), "\n")
	if !strings.HasSuffix(out[2], "M A A T") || !strings.HasSuffix(out[3], "second line") {
		t.Errorf("text should sit beside the body of the feather:\n%s", b.String())
	}
	col := strings.Index(out[2], "M A A T")
	if strings.Index(out[3], "second line") != col {
		t.Errorf("text lines should start in the same column:\n%s", b.String())
	}
}

func TestBannerSkipsNonTerminals(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	Banner(f, "M A A T")
	if fi, _ := f.Stat(); fi.Size() != 0 {
		t.Error("the banner must never be written to a file or a pipe")
	}
	r, w, _ := os.Pipe()
	defer r.Close()
	Banner(w, "M A A T")
	w.Close()
	var b bytes.Buffer
	b.ReadFrom(r)
	if b.Len() != 0 {
		t.Error("the banner must never be written to a pipe")
	}
}

func TestIconIsASmallPNG(t *testing.T) {
	uri := IconDataURI()
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("not a PNG data URI: %.40s", uri)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 64 || cfg.Height != 64 {
		t.Errorf("icon is %dx%d, want 64x64", cfg.Width, cfg.Height)
	}
	if len(uri) > 8*1024 {
		t.Errorf("icon data URI is %d bytes; keep it small, it is sent to every client", len(uri))
	}
}
