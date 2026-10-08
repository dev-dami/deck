package yolo

import "strings"

// sprite is a pose: equal-width rows, spaces transparent. Figures are 3 rows
// by 5 columns with the head in column 2, so a figure is placed by its head
// column. The strokes are fixed: o for a head, | / \ for limbs, ─ for a
// stretched arm or leg. Poses are drawn facing right; flip mirrors them.
type sprite []string

// flip mirrors a pose left to right, swapping the slants.
func flip(s sprite) sprite {
	out := make(sprite, len(s))
	for i, row := range s {
		rs := []rune(row)
		for l, r := 0, len(rs)-1; l <= r; l, r = l+1, r-1 {
			rs[l], rs[r] = mirrorRune(rs[r]), mirrorRune(rs[l])
		}
		out[i] = string(rs)
	}
	return out
}

func mirrorRune(r rune) rune {
	switch r {
	case '/':
		return '\\'
	case '\\':
		return '/'
	}
	return r
}

// head is the column of the head inside a figure sprite.
const head = 2

// The cautious one. The ? is the sign in its back hand.
var (
	askGuardA = sprite{"  o  ", "?/|\\ ", " / \\ "}
	askGuardB = sprite{"  o  ", "?/|\\ ", "/   \\"}
	askJab    = sprite{"  o  ", "?/|──", " / \\ "}
	askBlock  = sprite{"  o| ", "?/|| ", " / \\ "}
)

// The wild one.
var (
	yoloGuardA = sprite{"  o  ", " /|\\ ", " / \\ "}
	yoloGuardB = sprite{"  o  ", " /|\\ ", "/   \\"}
	yoloPunch  = sprite{"  o  ", " /|──", " / \\ "}
	yoloBlock  = sprite{"  o| ", " /|| ", " / \\ "}
	yoloWind   = sprite{"  o  ", " /|\\ ", "  |/ "}
	yoloKick   = sprite{"  o  ", " /|\\ ", "  |──"}
	yoloCheerA = sprite{" \\o/ ", "  |  ", " / \\ "}
	yoloCheerB = sprite{" \\o/ ", "  |  ", "  |  "}
)

// tumble are the poses of the kicked figure, 2 rows by 3 columns, in the
// order it rotates: upright, head left, upside down, head right.
var tumble = []sprite{
	{" o ", "/|\\"},
	{"o─/", "  \\"},
	{"\\|/", " o "},
	{"\\─o", "/  "},
}

func init() {
	// Every pose must be rectangular, or a column would drift.
	for _, s := range []sprite{askGuardA, askGuardB, askJab, askBlock,
		yoloGuardA, yoloGuardB, yoloPunch, yoloBlock, yoloWind, yoloKick,
		yoloCheerA, yoloCheerB} {
		if len(s) != 3 {
			panic("yolo: figure pose is not 3 rows")
		}
		for _, row := range s {
			if n := len([]rune(row)); n != 5 {
				panic("yolo: figure pose row is not 5 columns: " + strings.TrimSpace(row))
			}
		}
	}
}
