package termimg

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dev-dami/deck/internal/brand"
)

// decodeSixel is just enough of a Sixel decoder to read back EncodeSixel.
func decodeSixel(t *testing.T, s string) *image.NRGBA {
	t.Helper()
	if !strings.HasPrefix(s, "\x1bP0;1;0q\"1;1;") || !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("not a sixel image: %q", s[:min(20, len(s))])
	}
	body := strings.TrimSuffix(strings.TrimPrefix(s, "\x1bP0;1;0q\"1;1;"), "\x1b\\")
	num := func(i int) (int, int) {
		j := i
		for j < len(body) && body[j] >= '0' && body[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(body[i:j])
		return n, j
	}
	w, i := num(0)
	h, i := num(i + 1)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	pal := map[int]color.NRGBA{}
	cur, x, band := 0, 0, 0
	put := func(ch byte, n int) {
		bits := int(ch) - 63
		for k := 0; k < n; k++ {
			for dy := 0; dy < 6; dy++ {
				if bits&(1<<dy) != 0 {
					img.SetNRGBA(x, band+dy, pal[cur])
				}
			}
			x++
		}
	}
	for i < len(body) {
		switch c := body[i]; {
		case c == '#':
			n, j := num(i + 1)
			if j < len(body) && body[j] == ';' { // a palette entry
				var v [4]int
				k := j
				for p := 0; p < 4; p++ {
					v[p], k = num(k + 1)
				}
				pal[n] = color.NRGBA{uint8(v[1] * 255 / 100), uint8(v[2] * 255 / 100), uint8(v[3] * 255 / 100), 255}
				i = k
				continue
			}
			cur, i = n, j
		case c == '!':
			n, j := num(i + 1)
			put(body[j], n)
			i = j + 1
		case c == '$':
			x = 0
			i++
		case c == '-':
			x, band = 0, band+6
			i++
		case c >= 63 && c <= 126:
			put(c, 1)
			i++
		default:
			t.Fatalf("unexpected byte %q at %d", c, i)
		}
	}
	return img
}

func TestSixelRoundTripsTheLogos(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		logo, ok := brand.Logo(kind)
		if !ok {
			t.Fatalf("no %s logo", kind)
		}
		src := Fit(logo, 40, 40)
		got := decodeSixel(t, EncodeSixel(src))
		bad := 0
		for y := 0; y < 40; y++ {
			for x := 0; x < 40; x++ {
				a, b := src.NRGBAAt(x, y), got.NRGBAAt(x, y)
				if (a.A >= 128) != (b.A > 0) {
					bad++
					continue
				}
				if a.A >= 128 {
					d := func(p, q uint8) int { return max(int(p)-int(q), int(q)-int(p)) }
					if d(a.R, b.R)+d(a.G, b.G)+d(a.B, b.B) > 60 {
						bad++
					}
				}
			}
		}
		if bad > 0 {
			t.Errorf("%s: %d of 1600 pixels differ after a sixel round trip", kind, bad)
		}
		if out := os.Getenv("LOOK_OUT"); out != "" {
			f, _ := os.Create(out + "-" + kind + ".png")
			_ = png.Encode(f, got)
			f.Close()
		}
	}
}

func TestKittyPlaceholdersAreOneCellEach(t *testing.T) {
	if w := ansi.StringWidth(KittyCells(0, 2)); w != 2 {
		t.Fatalf("two placeholder cells are %d wide", w)
	}
	if c := KittyColor(0x7A0001); c != "#7A0001" {
		t.Fatalf("colour = %s", c)
	}
}

func TestKittyChunksLargeImages(t *testing.T) {
	logo, _ := brand.Logo("claude")
	s := KittyTransmit(7, logo, 2, 1)
	if !strings.HasPrefix(s, "\x1b_Ga=T,U=1,f=100,i=7,c=2,r=1,q=2,m=1;") || !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("transmit = %q…", s[:60])
	}
	for _, chunk := range strings.Split(s, "\x1b\\")[:strings.Count(s, "\x1b\\")] {
		if i := strings.IndexByte(chunk, ';'); i < 0 || len(chunk)-i-1 > 4096 {
			t.Fatalf("a chunk over 4096 bytes")
		}
	}
	if !strings.Contains(s, "m=0;") {
		t.Fatal("the last chunk has m=0")
	}
}

func TestDetect(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	for _, c := range []struct {
		env  func(string) string
		want Protocol
	}{
		{env(), None},
		{env("WT_SESSION", "x"), Sixel},
		{env("KITTY_WINDOW_ID", "1"), Kitty},
		{env("TERM_PROGRAM", "ghostty"), Kitty},
		{env("TERM_PROGRAM", "WezTerm"), ITerm},
		{env("TERM_PROGRAM", "iTerm.app"), ITerm},
		{env("WT_SESSION", "x", "DECK_IMAGES", "off"), None},
		{env("DECK_IMAGES", "kitty"), Kitty},
	} {
		if got := Detect(c.env); got != c.want {
			t.Errorf("Detect = %v, want %v", got, c.want)
		}
	}
}
