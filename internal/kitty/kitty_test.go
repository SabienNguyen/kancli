package kitty

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSupported(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM": "xterm-kitty"}, true},
		{map[string]string{"KITTY_WINDOW_ID": "1"}, true},
		{map[string]string{"TERM_PROGRAM": "ghostty"}, true},
		{map[string]string{"TERM": "xterm-256color"}, false},
		{map[string]string{"TERM": "xterm-kitty", "TMUX": "/tmp/tmux"}, false},
		{map[string]string{"TERM": "screen-256color", "KITTY_WINDOW_ID": "1"}, false},
	}
	for _, c := range cases {
		got := Supported(func(k string) string { return c.env[k] })
		if got != c.want {
			t.Errorf("Supported(%v) = %v", c.env, got)
		}
	}
}

func writePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "pic.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAndLines(t *testing.T) {
	path := writePNG(t, 1600, 400)
	im, err := Load(path, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	if im.Cols != 40 || im.Rows != 5 {
		t.Errorf("cells = %dx%d, want 40x5", im.Cols, im.Rows)
	}
	lines := im.Lines()
	if len(lines) != im.Rows {
		t.Fatalf("%d lines", len(lines))
	}
	if !strings.HasPrefix(lines[0], "\x1b_Ga=T,U=1,q=2,f=100,i=") || !strings.Contains(lines[0], ",c=40,r=5,m=") {
		t.Errorf("first line lacks the upload: %.80q", lines[0])
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != im.Cols {
			t.Errorf("line %d is %d cells wide, want %d", i, w, im.Cols)
		}
		if strings.Count(l, string(rune(placeholder))) != im.Cols {
			t.Errorf("line %d has the wrong number of placeholders", i)
		}
	}
	// Chunks stay under the protocol limit and all but the last continue.
	up := transmit(7, 4, 2, make([]byte, 9000))
	if n := strings.Count(up, "m=1;"); n != 2 || strings.Count(up, "m=0;") != 1 {
		t.Errorf("upload is not chunked properly:\n%.100s", up)
	}
	for _, chunk := range strings.Split(up, "\x1b\\") {
		if _, payload, ok := strings.Cut(chunk, ";"); ok && len(payload) > chunkSize {
			t.Errorf("chunk of %d bytes exceeds %d", len(payload), chunkSize)
		}
	}
	if !IsImage(path) || IsImage("https://x.test/a.png") || IsImage(filepath.Join(t.TempDir(), "missing.png")) {
		t.Error("IsImage")
	}
}

func TestFitKeepsAspect(t *testing.T) {
	cols, rows := fit(100, 100, 60, 8)
	if rows != 8 || cols != 16 {
		t.Errorf("square in 60x8 = %dx%d, want 16x8", cols, rows)
	}
	cols, rows = fit(300, 100, 30, 20)
	if cols != 30 || rows != 5 {
		t.Errorf("wide in 30x20 = %dx%d, want 30x5", cols, rows)
	}
}

func TestShrink(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	small := shrink(img, 640)
	if b := small.Bounds(); b.Dx() != 640 || b.Dy() != 320 {
		t.Errorf("shrunk to %v", b)
	}
	if s := shrink(image.NewNRGBA(image.Rect(0, 0, 10, 10)), 640); s.Bounds().Dx() != 10 {
		t.Error("small images must not grow")
	}
}
