package yolo

import "math"

// Beats, in milliseconds from Start. Every position below is a function of
// these and the elapsed time, never of a frame count.
const (
	tScuffle = 300.0  // face-off ends, scuffle starts
	tWind    = 1000.0 // scuffle ends, yolo winds up
	tLunge   = 1100.0 // yolo lunges
	tHit     = 1160.0 // the foot lands; ask leaves
	tCheer   = 1400.0 // victory
	tStampA  = 1450.0 // word appears small
	tStampB  = 1500.0 // word flashes
	tStampC  = 1600.0 // word settles
	tFlight  = 340.0  // ask's flight time
	tSign    = 240.0  // the ? sign's flight time
	tDust    = 300.0  // life of a dust mote
	scuffleF = 175.0  // one scuffle frame
)

func clamp01(v float64) float64 {
	switch {
	case v < 0 || math.IsNaN(v):
		return 0
	case v > 1:
		return 1
	}
	return v
}

func rnd(v float64) int { return int(math.Round(v)) }

func easeOut(u float64) float64 {
	u = clamp01(u)
	return 1 - (1-u)*(1-u)
}

// launch is how far into its flight the ask figure is, eased so it leaves
// the kick fast and coasts.
func launch(u float64) float64 { return 1 - math.Pow(1-clamp01(u), 1.7) }

// spark is a compact-scene impact mark; dx is relative to the centre column.
type spark struct {
	dx, row int
	r       rune
}

// stamp is the word next to the winner: yolo, YOLO, yolo.
func stamp(t float64) (word string, k kind, bold, flash bool) {
	switch {
	case t < tStampA:
		return "", kNone, false, false
	case t < tStampB:
		return "yolo", kWarn, false, false
	case t < tStampC:
		return "YOLO", kDanger, true, true
	}
	return "yolo", kDanger, true, false
}

// who says which character a figure belongs to; it picks the accent colour.
type who uint8

const (
	whoAsk who = iota
	whoYolo
	whoSign
)

// fig is a pose placed on the canvas: x, y is its top-left cell.
type fig struct {
	s    sprite
	x, y int
	who  who
	k    kind
}

// mark is an impact spark at an absolute cell.
type mark struct {
	x, y int
	r    rune
}

// frame is everything the full scene draws at one instant, apart from the
// ground, the dust and the word.
type frame struct {
	figs  []fig
	marks []mark
	yx    int // yolo's head column, where the word is anchored
}

// layoutFull places the characters for the six-row figure strip centred on
// column c at time t. The two never share a cell: contact is shown by marks.
func layoutFull(c int, t float64) frame {
	a0 := c - 6 // ask's head at rest
	y0 := c + 6 // yolo's head at rest
	yk := kWarn
	if t >= tWind {
		yk = kDanger
	}
	var fr frame
	ax, yx := a0, y0
	var askS, yoloS sprite
	askHere := t < tHit

	switch {
	case t < tScuffle:
		// Face off: step in from four columns out, bouncing out of phase.
		d := rnd(4 * (1 - easeOut(t/tScuffle)))
		ax, yx = a0-d, y0+d
		if int(t/100)%2 == 0 {
			askS, yoloS = askGuardA, yoloGuardB
		} else {
			askS, yoloS = askGuardB, yoloGuardA
		}

	case t < tWind:
		switch f := min(int((t-tScuffle)/scuffleF), 3); f {
		case 0: // yolo jabs, ask blocks
			askS, yoloS = askBlock, yoloJab
			fr.marks = []mark{{a0 + 6, 2, '✦'}, {a0 + 5, 1, '*'}}
		case 1: // ask jabs, yolo blocks
			askS, yoloS = askJab, yoloBlock
			fr.marks = []mark{{a0 + 6, 2, '✦'}, {a0 + 7, 1, '*'}}
		case 2: // fists meet
			askS, yoloS = askJab, yoloJab
			fr.marks = []mark{{a0 + 6, 1, '✦'}, {a0 + 6, 2, '✶'}, {a0 + 6, 3, '*'}}
		default: // yolo lands one: ask is knocked back a column
			askS, yoloS = askFlinch, yoloJab
			ax, yx = a0-1, y0-2
			fr.marks = []mark{{a0 + 3, 2, '✶'}, {a0 + 2, 1, '*'}}
		}

	case t < tHit:
		// Ask cowers; yolo draws back two columns, then lunges six.
		if int(t/50)%2 == 0 {
			askS = askGuardA
		} else {
			askS = askGuardB
		}
		if t < tLunge {
			yoloS = yoloWind
			yx = y0 + rnd(2*(t-tWind)/(tLunge-tWind))
		} else {
			yoloS = yoloKick
			yx = y0 + 2 - rnd(6*(t-tLunge)/(tHit-tLunge))
		}

	default:
		yx = y0 - 4
		switch {
		case t < tHit+140:
			yoloS = yoloKick
		case t < tCheer:
			yoloS = yoloGuardA
		case int((t-tCheer)/125)%2 == 0:
			yoloS = yoloCheerA
		default:
			yoloS = yoloCheerB
		}
		if age := t - tHit; age < 200 {
			// The foot lands.
			if age < 100 {
				fr.marks = append(fr.marks, mark{a0 + 2, 3, '✦'}, mark{a0 + 3, 2, '*'})
			} else {
				fr.marks = append(fr.marks, mark{a0 + 2, 2, '✶'}, mark{a0 + 1, 3, '*'})
			}
		}
	}
	fr.yx = yx

	if askHere {
		fr.figs = append(fr.figs, fig{askS, ax - figHead, 0, whoAsk, kMuted})
	} else {
		fr.figs = append(fr.figs, flightFigs(t, a0)...)
	}
	fr.figs = append(fr.figs, fig{yoloS, yx - figHead, 0, whoYolo, yk})
	return fr
}

// flightPose is the kicked ask at progress u: its head column, the sprite it
// has rotated to, and the sprite's top row. It is knocked back upright, then
// tumbles, leaving the ground and climbing to the top rows.
func flightPose(a0 int, u float64) (xh, top int, s sprite) {
	xh = rnd(float64(a0) - float64(a0+5)*launch(u))
	if u < 0.15 {
		return xh, 0, askFly
	}
	top = 1
	if u >= 0.5 {
		top = 0
	}
	return xh, top, tumble[int(u*6)%len(tumble)]
}

func (s sprite) width() int { return len([]rune(s[0])) }

// flightFigs places the tumbling ask and the ? sign spinning after it.
func flightFigs(t float64, a0 int) []fig {
	age := t - tHit
	var out []fig
	// The sign flies higher and a little behind, flipping as it goes.
	if us := clamp01(age / tSign); us < 1 {
		// It starts faster than the ask so the body never catches it.
		sx := a0 - 5 - rnd(float64(a0-2)*(1-math.Pow(1-us, 3)))
		sy := 2
		switch {
		case us > 0.8:
			sy = 0
		case us > 0.5:
			sy = 1
		}
		out = append(out, fig{signs[int(age/70)%2], sx, sy, whoSign, kFg})
	}
	if u := age / tFlight; u < 1 {
		xh, top, s := flightPose(a0, u)
		out = append(out, fig{s, xh - s.width()/2, top, whoAsk, kMuted})
	}
	return out
}

// dustFull draws the puff where the ask stood and the trail along its path.
func dustFull(cv *canvas, t float64, a0 int) {
	age := t - tHit
	if age < 0 {
		return
	}
	ground := cv.h - 2
	if age < tDust {
		f := age / tDust
		cv.put(a0-2, ground, '.', kMuted, false, f)
		cv.put(a0-1, ground, '·', kMuted, false, f)
		cv.put(a0+1, ground, '·', kMuted, false, f)
		cv.put(a0+2, ground, '.', kMuted, false, f)
		cv.put(a0, ground-1, '·', kMuted, false, f)
	}
	for tau, i := 0.0, 0; tau <= math.Min(age, tFlight); tau, i = tau+17, i+1 {
		since := age - tau
		if since < 40 || since > tDust {
			continue
		}
		xh, top, s := flightPose(a0, tau/tFlight)
		r := '·'
		if i%2 == 1 {
			r = '.'
		}
		cv.put(xh+s.width()/2, top+len(s)-1, r, kMuted, false, since/tDust)
	}
}

// sceneFull draws the strip: six rows of figures over a baseline.
func sceneFull(cv *canvas, t float64) {
	c := cv.w / 2
	for x := 0; x < cv.w; x++ {
		cv.put(x, cv.h-1, '─', kFaint, false, 0)
	}
	dustFull(cv, t, c-6)

	fr := layoutFull(c, t)
	for _, f := range fr.figs {
		cv.fig(f, t)
	}
	for _, m := range fr.marks {
		cv.put(m.x, m.y, m.r, kWarn, false, 0)
	}
	if w, k, bold, flash := stamp(t); w != "" {
		x := fr.yx + 7
		cv.text(x, 1, w, k, bold)
		if flash {
			cv.put(x+len(w), 1, '✦', kWarn, false, 0)
			cv.put(x+len(w)+1, 2, '*', kWarn, false, 0)
			cv.put(x-1, 0, '*', kWarn, false, 0)
		}
	}
}

// sceneCompact draws the one-row version: tiny figures, same beats.
func sceneCompact(cv *canvas, t float64) {
	c := cv.w / 2
	a0 := c - 3
	y0 := c + 2
	yk := kWarn
	if t >= tWind {
		yk = kDanger
	}

	ax, yx := a0, y0
	al, yl := '/', '\\' // limb glyphs: ask's right side, yolo's left side
	askHere := t < tHit
	var sparks []spark // dx relative to c

	switch {
	case t < tScuffle:
		d := rnd(3 * (1 - easeOut(t/tScuffle)))
		ax, yx = a0-d, y0+d
		if int(t/100)%2 == 1 {
			al, yl = '|', '|'
		}
	case t < tWind:
		switch f := min(int((t-tScuffle)/scuffleF), 3); f {
		case 0:
			al, yl = '|', '-'
			sparks = []spark{{0, 0, '*'}}
		case 1:
			al, yl = '-', '|'
			sparks = []spark{{-1, 0, '✦'}}
		case 2:
			al, yl = '-', '-'
			sparks = []spark{{-1, 0, '*'}, {0, 0, '✦'}}
		default:
			al, yl = '/', '-'
			ax = a0 - 1
			sparks = []spark{{-1, 0, '✦'}}
		}
	case t < tHit:
		al = '/'
		if t < tLunge {
			yl = '/'
			yx = y0 + rnd((t-tWind)/(tLunge-tWind))
		} else {
			yl = '-'
			yx = y0 + 1 - rnd(2*(t-tLunge)/(tHit-tLunge))
		}
	default:
		yx = y0 - 1
		yl = '-'
		if t >= tHit+140 {
			yl = '\\'
		}
	}

	if askHere {
		cv.put(ax, 0, 'o', kMuted, false, 0)
		cv.put(ax+1, 0, al, kMuted, false, 0)
	} else {
		flightCompact(cv, t, a0)
	}

	if t >= tCheer {
		l, r := '\\', '/'
		if int((t-tCheer)/125)%2 == 1 {
			l, r = '/', '\\'
		}
		cv.put(yx-1, 0, l, yk, false, 0)
		cv.put(yx, 0, 'o', yk, false, 0)
		cv.put(yx+1, 0, r, yk, false, 0)
	} else {
		cv.put(yx-1, 0, yl, yk, false, 0)
		cv.put(yx, 0, 'o', yk, false, 0)
	}
	for _, s := range sparks {
		cv.put(c+s.dx, 0, s.r, kWarn, false, 0)
	}
	if dt := t - tHit; dt >= 0 && dt < 200 {
		cv.put(c-1, 0, '✦', kWarn, false, 0)
	}
	if w, k, bold, _ := stamp(t); w != "" {
		cv.text(yx+3, 0, w, k, bold)
	}
}

// flightCompact is the one-row tumble: a head leading a spinning limb, with a
// dust trail.
func flightCompact(cv *canvas, t float64, a0 int) {
	age := t - tHit
	if age < 0 {
		return
	}
	pos := func(u float64) int { return rnd(float64(a0) - float64(a0+3)*launch(u)) }
	for tau, i := 0.0, 0; tau <= math.Min(age, tFlight); tau, i = tau+17, i+1 {
		since := age - tau
		if since < 40 || since > tDust {
			continue
		}
		r := '·'
		if i%2 == 1 {
			r = '.'
		}
		cv.put(pos(tau/tFlight)+2, 0, r, kMuted, false, since/tDust)
	}
	u := age / tFlight
	if u >= 1 {
		return
	}
	x := pos(u)
	cv.put(x, 0, 'o', kMuted, false, 0)
	cv.put(x+1, 0, []rune{'/', '-', '\\', '|'}[int(u*6)%4], kMuted, false, 0)
}
