package yolo

// sprite is a hand-drawn pose: equal-width rows, spaces transparent. Every
// rune is a single-width glyph (a test pins that).
//
// Both characters are figSize wide and figRows tall with the head in column
// figHead, so a figure is placed by its head column. Ask faces right and
// yolo faces left; each is drawn by hand, not mirrored.
type sprite []string

const (
	figRows = 6  // rows of a standing figure
	figSize = 11 // columns of a standing figure
	figHead = 5  // head column inside a figure sprite
)

// ask: the careful one. Tidy hair, round glasses, collar and tie, loafers,
// and a [?] sign in the back hand.
var (
	askGuardA = sprite{
		"   .---.   ",
		"   (o_o)   ",
		"[?]/|V|\\   ",
		"    |_|    ",
		"    | |    ",
		"   _| |_   ",
	}
	askGuardB = sprite{ // blink, wider stance
		"   .---.   ",
		"   (-_-)   ",
		"[?]/|V|\\   ",
		"    |_|    ",
		"    / \\    ",
		"   _/ \\_   ",
	}
	askJab = sprite{
		"   .---.   ",
		"   (o_o)   ",
		"[?]/|V|═══o",
		"    |_|    ",
		"    / \\    ",
		"   _/ \\_   ",
	}
	askBlock = sprite{
		"   .---.   ",
		"   (o_o) | ",
		"[?]/|V|\\/  ",
		"    |_|    ",
		"    | |    ",
		"   _| |_   ",
	}
	askFlinch = sprite{ // head knocked back, arms flailing
		"  .---.    ",
		"  (>_<)    ",
		"[?]\\|V|/   ",
		"    |_|    ",
		"    | |    ",
		"   _| |_   ",
	}
	askFly = sprite{ // the instant after the kick: sign already gone
		"  .---.    ",
		"  (>_<)    ",
		"   \\|V|/   ",
		"    |_|    ",
		"    / \\    ",
		"   _/ \\_   ",
	}
)

// tumble are the poses of the kicked ask, 4 rows by 9 columns with the head
// centred when upright, in the order it rotates: upright, head left, upside
// down, head right.
var tumble = []sprite{
	{
		"  .---.  ",
		"  (>_<)  ",
		"  \\|V|/  ",
		"   / \\   ",
	},
	{
		"         ",
		"     /   ",
		"(>_<)V==<",
		"     \\   ",
	},
	{
		"  _| |_  ",
		"   | |   ",
		"  /|^|\\  ",
		"  (>_<)  ",
	},
	{
		"         ",
		"    \\    ",
		">==^(>_<)",
		"    /    ",
	},
}

// signs are the ? sign once it is airborne: it flips as it spins.
var signs = []sprite{{"[?]"}, {"[¿]"}}

// yolo: the wild one. Spiky hair with a headband (the = knot and ~ ≈ tails
// flutter), shades, an open jacket and chunky sneakers.
var (
	yoloGuardA = sprite{
		"   /\\/\\/\\=~",
		"   (⌐■_■) ≈",
		"  /|[ ]|\\  ",
		"   |[_]|   ",
		"    / \\    ",
		"  ==/ \\==  ",
	}
	yoloGuardB = sprite{ // squatting
		"   /\\/\\/\\=≈",
		"   (⌐■_■)~ ",
		"  /|[ ]|\\  ",
		"   |[_]|   ",
		"    / \\    ",
		"   =/ \\=   ",
	}
	yoloJab = sprite{
		"   /\\/\\/\\=~",
		"   (⌐■_■) ≈",
		"o═══[ ]|\\  ",
		"   |[_]|   ",
		"   /   \\   ",
		"  =/   \\=  ",
	}
	yoloBlock = sprite{
		"   /\\/\\/\\=~",
		" | (⌐■_■) ≈",
		"  \\/[ ]|\\  ",
		"   |[_]|   ",
		"    / \\    ",
		"  ==/ \\==  ",
	}
	yoloWind = sprite{ // leaning back, knee cocked
		"    /\\/\\/\\=",
		"    (⌐■_■)≈",
		"  /|[ ]|\\  ",
		"   |[_]|   ",
		"   <==|    ",
		"      |==  ",
	}
	yoloKick = sprite{ // the leg out flat, with its swoosh
		"   /\\/\\/\\=~",
		"   (⌐■_■) ≈",
		"  \\|[ ]|/  ",
		"⌒⌒⌒|[_]|   ",
		"<=====|    ",
		"      |==  ",
	}
	yoloCheerA = sprite{ // left fist up
		"o  /\\/\\/\\=~",
		" \\ (⌐■_■) ≈",
		"  \\|[ ]|\\  ",
		"   |[_]|   ",
		"    / \\    ",
		"  ==/ \\==  ",
	}
	yoloCheerB = sprite{ // right fist up, hopping
		"o  /\\/\\/\\=o",
		" \\ (⌐■_■)/≈",
		"  \\|[ ]|/  ",
		"   |[_]|   ",
		"    | |    ",
		"  ==| |==  ",
	}
)

func init() {
	// Every pose must be rectangular, or a column would drift.
	check := func(name string, s sprite, rows, cols int) {
		if len(s) != rows {
			panic("yolo: " + name + " has the wrong number of rows")
		}
		for _, row := range s {
			if n := len([]rune(row)); n != cols {
				panic("yolo: " + name + " row is the wrong width: " + row)
			}
		}
	}
	for name, s := range map[string]sprite{
		"askGuardA": askGuardA, "askGuardB": askGuardB, "askJab": askJab,
		"askBlock": askBlock, "askFlinch": askFlinch, "askFly": askFly,
		"yoloGuardA": yoloGuardA, "yoloGuardB": yoloGuardB, "yoloJab": yoloJab,
		"yoloBlock": yoloBlock, "yoloWind": yoloWind, "yoloKick": yoloKick,
		"yoloCheerA": yoloCheerA, "yoloCheerB": yoloCheerB,
	} {
		check(name, s, figRows, figSize)
	}
	for i, s := range tumble {
		check("tumble"+string(rune('0'+i)), s, 4, 9)
	}
}
