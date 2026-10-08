package yolo

import (
	"time"

	"github.com/lucasb-eyer/go-colorful"
)

// font is a 5-row block font with just the letters YOLO needs.
var font = map[rune][5]string{
	'Y': {"#...#", ".#.#.", "..#..", "..#..", "..#.."},
	'O': {".###.", "#...#", "#...#", "#...#", ".###."},
	'L': {"#....", "#....", "#....", "#....", "#####"},
}

const (
	letterW = 5
	letterH = 5
	gap     = 2 // font pixels between letters
)

// wordLayer is the big YOLO, centred on the screen.
type wordLayer struct {
	x0, y0, scale int
	cols          int // width in font pixels
	esc           string
}

// word returns the YOLO layer at time t, or nil when it isn't showing or the
// screen is too small for it.
func (a *Anim) word(w, h int, t time.Duration) *wordLayer {
	if t < wordOn || t >= wordOff {
		return nil
	}
	cols := 4*letterW + 3*gap
	scale := 2 // font pixels are two cells wide so the letters look square
	if cols*scale > w-4 {
		scale = 1
	}
	if cols*scale > w-2 || letterH > h-2 {
		return nil
	}
	// Pop: the letters flash white-hot, then settle to a bright tone.
	p := clamp01(float64(t-wordOn) / float64(popFor))
	base, hot := "#FFFFFF", "#FFFFFF"
	if a.dark {
		base = "#F4F7F6"
	} else {
		base, hot = "#101413", "#000000"
	}
	cb, _ := colorful.Hex(base)
	ch, _ := colorful.Hex(hot)
	c := ch.BlendLab(cb, p).Clamped().Hex()
	return &wordLayer{
		x0:    (w - cols*scale) / 2,
		y0:    (h - letterH) / 2,
		scale: scale,
		cols:  cols,
		esc:   sgr(c, c, true),
	}
}

// at reports whether cell (x, y) is part of a letter.
func (l *wordLayer) at(x, y int) bool {
	row := y - l.y0
	if row < 0 || row >= letterH || x < l.x0 {
		return false
	}
	px := (x - l.x0) / l.scale
	if px >= l.cols {
		return false
	}
	for i, r := range "YOLO" {
		start := i * (letterW + gap)
		if px >= start && px < start+letterW {
			return font[r][row][px-start] == '#'
		}
	}
	return false
}
