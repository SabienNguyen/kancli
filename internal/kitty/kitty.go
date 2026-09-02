// Package kitty renders images inline using the kitty graphics protocol
// with Unicode placeholders, so pictures scroll, clip and disappear with
// the text around them like any other cell content.
package kitty

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // register decoders for the formats attachments use
	_ "image/jpeg" //
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

const (
	// placeholder is the private-use character terminals replace with
	// image cells.
	placeholder = '\U0010EEEE'
	// chunkSize is the maximum base64 payload per escape sequence.
	chunkSize = 4096
	// maxPixels bounds the longer side of a transmitted image.
	maxPixels = 640
)

// Supported reports whether the terminal described by env (usually
// os.Getenv) understands graphics placeholders. tmux and screen do not pass
// the sequences through, so they are treated as unsupported.
func Supported(env func(string) string) bool {
	if env("TMUX") != "" || strings.HasPrefix(env("TERM"), "screen") {
		return false
	}
	switch {
	case env("KITTY_WINDOW_ID") != "",
		strings.Contains(env("TERM"), "kitty"),
		env("GHOSTTY_RESOURCES_DIR") != "",
		strings.EqualFold(env("TERM_PROGRAM"), "ghostty"):
		return true
	}
	return false
}

// IsImage reports whether ref looks like a local image file.
func IsImage(ref string) bool {
	switch strings.ToLower(filepath.Ext(ref)) {
	case ".png", ".jpg", ".jpeg", ".gif":
	default:
		return false
	}
	if strings.Contains(ref, "://") {
		return false
	}
	_, err := os.Stat(expand(ref))
	return err == nil
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Image is an encoded picture ready to be placed in a grid of cells.
type Image struct {
	ID   uint32
	Cols int
	Rows int
	// transmit carries the pixels; it is zero-width on screen.
	transmit string
}

// Load reads, scales and encodes the image at path so it fits within
// maxCols by maxRows cells. Cells are assumed to be twice as tall as they
// are wide.
func Load(path string, maxCols, maxRows int) (*Image, error) {
	data, err := os.ReadFile(expand(path))
	if err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	img := shrink(src, maxPixels)
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("%s: empty image", filepath.Base(path))
	}
	cols, rows := fit(w, h, maxCols, maxRows)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	id := imageID(path)
	return &Image{ID: id, Cols: cols, Rows: rows, transmit: transmit(id, cols, rows, buf.Bytes())}, nil
}

// fit chooses a cell size that keeps the aspect ratio inside the bounds.
func fit(w, h, maxCols, maxRows int) (cols, rows int) {
	maxCols, maxRows = max(1, maxCols), max(1, maxRows)
	cols = maxCols
	rows = max(1, int(float64(cols)*float64(h)/float64(w)/2+0.5))
	if rows > maxRows {
		rows = maxRows
		cols = max(1, int(float64(rows)*2*float64(w)/float64(h)+0.5))
	}
	return min(cols, maxCols), rows
}

// imageID derives a stable placeholder id (1..255) from the path so the
// same picture keeps its id across renders.
func imageID(path string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(path)) //nolint:errcheck // hash writes never fail
	return h.Sum32()%255 + 1
}

// transmit builds the chunked escape sequences that upload the PNG and
// declare it a virtual (placeholder) image.
func transmit(id uint32, cols, rows int, pngData []byte) string {
	enc := base64.StdEncoding.EncodeToString(pngData)
	var sb strings.Builder
	first := true
	for len(enc) > 0 {
		n := min(chunkSize, len(enc))
		chunk, rest := enc[:n], enc[n:]
		more := 0
		if len(rest) > 0 {
			more = 1
		}
		sb.WriteString("\x1b_G")
		if first {
			fmt.Fprintf(&sb, "a=T,U=1,q=2,f=100,i=%d,c=%d,r=%d,", id, cols, rows)
			first = false
		}
		fmt.Fprintf(&sb, "m=%d;%s\x1b\\", more, chunk)
		enc = rest
	}
	return sb.String()
}

// Lines renders the placeholder grid. The first line also carries the
// pixel upload so a terminal that has not seen the image yet can show it.
func (im *Image) Lines() []string {
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (im.ID>>16)&0xff, (im.ID>>8)&0xff, im.ID&0xff)
	out := make([]string, im.Rows)
	for r := 0; r < im.Rows; r++ {
		var sb strings.Builder
		if r == 0 {
			sb.WriteString(im.transmit)
		}
		sb.WriteString(fg)
		for c := 0; c < im.Cols; c++ {
			sb.WriteRune(placeholder)
			sb.WriteRune(diacritics[r%len(diacritics)])
			sb.WriteRune(diacritics[c%len(diacritics)])
		}
		sb.WriteString("\x1b[39m")
		out[r] = sb.String()
	}
	return out
}

// shrink scales img down so its longer side is at most limit pixels, using
// a box filter. Images already small enough are returned unchanged.
func shrink(img image.Image, limit int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= limit && h <= limit {
		if rgba, ok := img.(*image.NRGBA); ok {
			return rgba
		}
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
		return dst
	}
	scale := float64(limit) / float64(max(w, h))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0 := b.Min.Y + y*h/nh
		y1 := b.Min.Y + max((y+1)*h/nh, y*h/nh+1)
		for x := 0; x < nw; x++ {
			x0 := b.Min.X + x*w/nw
			x1 := b.Min.X + max((x+1)*w/nw, x*w/nw+1)
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					c := color.NRGBAModel.Convert(img.At(xx, yy)).(color.NRGBA)
					r += uint64(c.R)
					g += uint64(c.G)
					bl += uint64(c.B)
					a += uint64(c.A)
					n++
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: uint8(a / n)})
		}
	}
	return dst
}
