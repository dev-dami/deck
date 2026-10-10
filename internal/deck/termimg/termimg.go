// Package termimg draws small images in a terminal: the providers' logos in
// `deck doctor`, and inside the TUI where the terminal can keep an image in
// ordinary text cells (kitty's Unicode placeholders).
//
// Three protocols: kitty graphics (kitty, Ghostty), iTerm2 inline images
// (iTerm2, WezTerm) and Sixel (Windows Terminal 1.22+, foot, mlterm, xterm
// with sixel). The protocol is guessed from the environment; DECK_IMAGES
// (off, kitty, iterm, sixel) overrides the guess.
package termimg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sort"
	"strings"
)

// Protocol is a way of drawing images in a terminal.
type Protocol int

const (
	None Protocol = iota
	Kitty
	ITerm
	Sixel
)

func (p Protocol) String() string {
	return [...]string{"none", "kitty", "iterm", "sixel"}[p]
}

// Detect guesses the protocol the terminal speaks from its environment.
func Detect(getenv func(string) string) Protocol {
	switch strings.ToLower(getenv("DECK_IMAGES")) {
	case "off", "none", "0", "false":
		return None
	case "kitty":
		return Kitty
	case "iterm":
		return ITerm
	case "sixel":
		return Sixel
	}
	term, prog := strings.ToLower(getenv("TERM")), strings.ToLower(getenv("TERM_PROGRAM"))
	switch {
	case getenv("KITTY_WINDOW_ID") != "", strings.Contains(term, "kitty"), prog == "ghostty", strings.Contains(term, "ghostty"):
		return Kitty
	case prog == "iterm.app", prog == "wezterm":
		return ITerm
	case getenv("WT_SESSION") != "", strings.Contains(term, "foot"), strings.Contains(term, "mlterm"), strings.Contains(term, "sixel"):
		return Sixel
	}
	return None
}

// Cell size in pixels assumed for Sixel, which draws in pixels; the other
// protocols are told the size in cells and scale the image themselves.
const cellW, cellH = 10, 20

// Draw returns the escape sequence that draws img at the cursor, cols x rows
// cells, or "" for None.
func Draw(p Protocol, img image.Image, cols, rows int) string {
	switch p {
	case Kitty:
		return kittyAPC(fmt.Sprintf("a=T,f=100,c=%d,r=%d,C=1,q=2", cols, rows), pngOf(img))
	case ITerm:
		return fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1:%s\x07",
			cols, rows, base64.StdEncoding.EncodeToString(pngOf(img)))
	case Sixel:
		return EncodeSixel(Fit(img, cols*cellW, rows*cellH))
	}
	return ""
}

// ---- kitty: Unicode placeholders ----

// KittyTransmit sends img to the terminal under id with a virtual placement
// cols x rows cells, shown wherever KittyCells puts its placeholders.
func KittyTransmit(id uint32, img image.Image, cols, rows int) string {
	return kittyAPC(fmt.Sprintf("a=T,U=1,f=100,i=%d,c=%d,r=%d,q=2", id, cols, rows), pngOf(img))
}

// KittyCells is one row of placeholder cells for image id. The caller draws
// them with the foreground colour KittyColor(id).
func KittyCells(row, cols int) string {
	var b strings.Builder
	for c := 0; c < cols; c++ {
		b.WriteRune(placeholder)
		b.WriteRune(diacritics[row])
		b.WriteRune(diacritics[c])
	}
	return b.String()
}

// KittyMark is one row of placeholder cells for image id, coloured with the
// exact 24-bit colour that names it (written directly: a colour library's
// conversions can be off by one, which would name another image).
func KittyMark(id uint32, cols int) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16&0xFF, id>>8&0xFF, id&0xFF) + KittyCells(0, cols) + "\x1b[39m"
}

// KittyColor is the 24-bit colour that names image id to the terminal.
func KittyColor(id uint32) string { return fmt.Sprintf("#%06X", id&0xFFFFFF) }

const placeholder = '\U0010EEEE'

// diacritics number rows and columns of a placeholder (the start of kitty's
// rowcolumn-diacritics table).
var diacritics = []rune{0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F}

func kittyAPC(ctrl string, data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for first := true; first || enc != ""; first = false {
		chunk := enc
		if len(chunk) > 4096 {
			chunk = chunk[:4096]
		}
		enc = enc[len(chunk):]
		more := 0
		if enc != "" {
			more = 1
		}
		b.WriteString("\x1b_G")
		if first {
			b.WriteString(ctrl + ",")
		}
		fmt.Fprintf(&b, "m=%d;%s\x1b\\", more, chunk)
	}
	return b.String()
}

func pngOf(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// ---- scaling ----

// Fit scales img down to fit w x h pixels, keeping its aspect ratio and
// centring it, by averaging the source pixels under each target pixel.
func Fit(img image.Image, w, h int) *image.NRGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	scale := min(float64(w)/float64(sw), float64(h)/float64(sh))
	tw, th := max(1, int(float64(sw)*scale)), max(1, int(float64(sh)*scale))
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	ox, oy := (w-tw)/2, (h-th)/2
	for y := 0; y < th; y++ {
		y0, y1 := b.Min.Y+y*sh/th, b.Min.Y+max((y+1)*sh/th, y*sh/th+1)
		for x := 0; x < tw; x++ {
			x0, x1 := b.Min.X+x*sw/tw, b.Min.X+max((x+1)*sw/tw, x*sw/tw+1)
			var r, g, bl, a, n float64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
					al := float64(c.A)
					r += float64(c.R) * al
					g += float64(c.G) * al
					bl += float64(c.B) * al
					a += al
					n++
				}
			}
			if a == 0 {
				continue
			}
			out.SetNRGBA(ox+x, oy+y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(bl / a), uint8(a / n)})
		}
	}
	return out
}

// ---- sixel ----

// EncodeSixel encodes img as Sixel. Pixels less than half opaque are left
// transparent; the rest take the nearest of at most 255 palette colours.
func EncodeSixel(img *image.NRGBA) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// a palette: drop low bits until the image has at most 255 colours
	var key func(c color.NRGBA) uint32
	var counts map[uint32]int
	for bits := uint(5); bits >= 1; bits-- {
		shift := 8 - bits
		key = func(c color.NRGBA) uint32 {
			return uint32(c.R>>shift)<<16 | uint32(c.G>>shift)<<8 | uint32(c.B>>shift)
		}
		counts = map[uint32]int{}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y); c.A >= 128 {
					counts[key(c)]++
				}
			}
		}
		if len(counts) <= 255 {
			break
		}
	}
	keys := make([]uint32, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	index := map[uint32]int{}
	sum := map[uint32][4]int{} // the true average colour of each palette entry
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y); c.A >= 128 {
				s := sum[key(c)]
				sum[key(c)] = [4]int{s[0] + int(c.R), s[1] + int(c.G), s[2] + int(c.B), s[3] + 1}
			}
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "\x1bP0;1;0q\"1;1;%d;%d", w, h)
	for i, k := range keys {
		index[k] = i + 1 // 0 stays transparent
		s := sum[k]
		pct := func(v int) int { return (v/s[3]*100 + 127) / 255 }
		fmt.Fprintf(&out, "#%d;2;%d;%d;%d", i+1, pct(s[0]), pct(s[1]), pct(s[2]))
	}
	px := make([]int, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y); c.A >= 128 {
				px[y*w+x] = index[key(c)]
			}
		}
	}
	for band := 0; band < h; band += 6 {
		used := map[int]bool{}
		for y := band; y < min(band+6, h); y++ {
			for x := 0; x < w; x++ {
				if p := px[y*w+x]; p > 0 {
					used[p] = true
				}
			}
		}
		colours := make([]int, 0, len(used))
		for p := range used {
			colours = append(colours, p)
		}
		sort.Ints(colours)
		for ci, p := range colours {
			if ci > 0 {
				out.WriteByte('$') // back to the start of the band
			}
			fmt.Fprintf(&out, "#%d", p)
			run, last := 0, byte(0)
			flush := func() {
				switch {
				case run == 0:
				case run >= 4:
					fmt.Fprintf(&out, "!%d%c", run, last)
				default:
					out.WriteString(strings.Repeat(string(last), run))
				}
			}
			for x := 0; x < w; x++ {
				bits := 0
				for dy := 0; dy < 6 && band+dy < h; dy++ {
					if px[(band+dy)*w+x] == p {
						bits |= 1 << dy
					}
				}
				ch := byte(63 + bits)
				if ch == last {
					run++
					continue
				}
				flush()
				run, last = 1, ch
			}
			flush()
		}
		out.WriteByte('-') // next band
	}
	out.WriteString("\x1b\\")
	return out.String()
}
