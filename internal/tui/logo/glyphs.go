package logo

import (
	"math"
	"strings"
	"sync"
)

// bitmap is a dot-level image: one bool per braille dot.
type bitmap struct {
	w, h int
	px   []bool
}

func newBitmap(w, h int) *bitmap { return &bitmap{w: w, h: h, px: make([]bool, w*h)} }

func (b *bitmap) at(x, y int) bool {
	if x < 0 || y < 0 || x >= b.w || y >= b.h {
		return false
	}
	return b.px[y*b.w+x]
}

func (b *bitmap) set(x, y int) {
	if x >= 0 && y >= 0 && x < b.w && y < b.h {
		b.px[y*b.w+x] = true
	}
}

// String renders the bitmap as '#' and '.' rows (used by tests and tuning).
func (b *bitmap) String() string {
	var sb strings.Builder
	for y := 0; y < b.h; y++ {
		for x := 0; x < b.w; x++ {
			if b.at(x, y) {
				sb.WriteByte('#')
			} else {
				sb.WriteByte('.')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

var (
	fullOnce, compactOnce sync.Once
	fullBM, compactBM     *bitmap
)

func fullBitmap() *bitmap {
	fullOnce.Do(func() { fullBM = buildFull() })
	return fullBM
}

func compactBitmap() *bitmap {
	compactOnce.Do(func() { compactBM = buildCompact() })
	return compactBM
}

// Full-size geometry, in dots. Ascender top is y=0, x-height top y=6,
// baseline y=20 (exclusive); strokes are 3 dots.
const (
	fullH      = 20
	fullXTop   = 6
	fullStroke = 3.0
	fullBowl   = 14 // bowl letter width
	fullGap    = 3  // between letters
	fullDiaGap = 6  // diamond to d
	fullDia    = 9  // diamond width/height
)

// capsule reports whether (px,py) is within r of the segment a-b.
func capsule(px, py, ax, ay, bx, by, r float64) bool {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = ((px-ax)*dx + (py-ay)*dy) / l2
	}
	t = math.Max(0, math.Min(1, t))
	qx, qy := ax+t*dx, ay+t*dy
	return math.Hypot(px-qx, py-qy) <= r
}

// ring reports whether (px,py) is inside the elliptical ring.
func ring(px, py, cx, cy, rx, ry, th float64) bool {
	o := math.Pow((px-cx)/rx, 2) + math.Pow((py-cy)/ry, 2)
	if o > 1 {
		return false
	}
	irx, iry := rx-th, ry-th
	i := math.Pow((px-cx)/irx, 2) + math.Pow((py-cy)/iry, 2)
	return i >= 1
}

func buildFull() *bitmap {
	const (
		bw = fullBowl
		cy = 13.0
		ry = 7.0
	)
	rx := float64(bw) / 2
	th := fullStroke

	// letter origins
	xd := fullDia + fullDiaGap
	xe := xd + bw + fullGap
	xc := xe + bw + fullGap
	xk := xc + bw + fullGap
	kw := 13
	total := xk + kw

	b := newBitmap(total, fullH)

	// diamond
	for y := 0; y < fullDia; y++ {
		for x := 0; x < fullDia; x++ {
			if abs(x-4)+abs(y-4) <= 4 {
				b.set(x, y+9)
			}
		}
	}

	stem := func(ox int, x0 int) {
		// x0: left x of the stem inside the letter. Rounded top, flat bottom.
		for y := 0; y < fullH; y++ {
			for x := 0; x < int(th); x++ {
				px, py := float64(ox+x0+x)+0.5, float64(y)+0.5
				if y >= 2 || capsule(px, py, float64(ox+x0)+th/2, 1.5, float64(ox+x0)+th/2, 2, th/2) {
					b.set(ox+x0+x, y)
				}
			}
		}
	}

	// d: bowl + right stem
	for y := fullXTop; y < fullH; y++ {
		for x := 0; x < bw; x++ {
			if ring(float64(x)+0.5, float64(y)+0.5, rx, cy, rx, ry, th) {
				b.set(xd+x, y)
			}
		}
	}
	stem(xd, bw-int(th))

	// e: bowl + bar, open lower right
	for y := fullXTop; y < fullH; y++ {
		for x := 0; x < bw; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if ring(px, py, rx, cy, rx, ry, th) && !(x >= 8 && y >= 15 && y <= 16) {
				b.set(xe+x, y)
			}
			if y >= 12 && y <= 14 && math.Pow((px-rx)/rx, 2)+math.Pow((py-cy)/ry, 2) <= 1 {
				b.set(xe+x, y)
			}
		}
	}

	// c: bowl, open right
	for y := fullXTop; y < fullH; y++ {
		for x := 0; x < bw; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if ring(px, py, rx, cy, rx, ry, th) && !(px > rx+1 && math.Abs(py-cy) < 3.6) {
				b.set(xc+x, y)
			}
		}
	}

	// k: left stem + two arms
	stem(xk, 0)
	for y := fullXTop; y < fullH; y++ {
		for x := int(th); x < kw; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if capsule(px, py, 3.2, 14.2, 11.4, 7.6, 1.55) || capsule(px, py, 6.4, 12.0, 11.6, 18.4, 1.55) {
				b.set(xk+x, y)
			}
		}
	}
	return b
}

func buildCompact() *bitmap {
	diamond := []string{
		".....",
		".....",
		"..#..",
		".###.",
		"#####",
		".###.",
		"..#..",
		".....",
	}
	d := []string{
		"....##",
		"....##",
		".#####",
		"##..##",
		"##..##",
		"##..##",
		"##..##",
		".#####",
	}
	e := []string{
		"......",
		"......",
		".####.",
		"##..##",
		"######",
		"##....",
		"##..##",
		".####.",
	}
	c := []string{
		"......",
		"......",
		".####.",
		"##..##",
		"##....",
		"##....",
		"##..##",
		".####.",
	}
	k := []string{
		"##....",
		"##....",
		"##..##",
		"##.##.",
		"####..",
		"####..",
		"##.##.",
		"##..##",
	}
	parts := [][]string{diamond, d, e, c, k}
	gaps := []int{4, 2, 2, 2}
	total := 0
	for i, p := range parts {
		total += len(p[0])
		if i < len(gaps) {
			total += gaps[i]
		}
	}
	b := newBitmap(total, 8)
	x0 := 0
	for i, p := range parts {
		for y, row := range p {
			for x, ch := range row {
				if ch == '#' {
					b.set(x0+x, y)
				}
			}
		}
		x0 += len(p[0])
		if i < len(gaps) {
			x0 += gaps[i]
		}
	}
	return b
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
