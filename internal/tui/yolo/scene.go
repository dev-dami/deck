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
	tSign    = 420.0  // the ? sign's flight time
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

// sceneFull draws the 4-row strip: three rows of figure over a baseline.
func sceneFull(cv *canvas, t float64) {
	c := cv.w / 2
	a0 := c - 3 // ask's head at rest
	y0 := c + 2 // yolo's head at rest
	for x := 0; x < cv.w; x++ {
		cv.put(x, 3, '─', kFaint, false, 0)
	}

	yk := kWarn
	if t >= tWind {
		yk = kDanger
	}
	ax, yx := a0, y0
	var askP, yoloP sprite
	askHere := t < tHit
	var sparks []spark // relative to a0

	switch {
	case t < tScuffle:
		// Face off: step in from four columns out, bouncing out of phase.
		d := rnd(4 * (1 - easeOut(t/tScuffle)))
		ax, yx = a0-d, y0+d
		if int(t/100)%2 == 0 {
			askP, yoloP = askGuardA, yoloGuardB
		} else {
			askP, yoloP = askGuardB, yoloGuardA
		}

	case t < tWind:
		switch f := min(int((t-tScuffle)/scuffleF), 3); f {
		case 0: // yolo punches, ask blocks
			askP, yoloP = askBlock, yoloPunch
			sparks = []spark{{2, 1, '*'}, {2, 0, '✦'}}
		case 1: // ask jabs, yolo blocks
			askP, yoloP = askJab, yoloBlock
			sparks = []spark{{3, 1, '✦'}, {3, 0, '*'}}
		case 2: // fists meet
			askP, yoloP = askJab, yoloPunch
			sparks = []spark{{2, 0, '✦'}, {3, 0, '*'}}
		default: // yolo lands one, ask is pushed back a column
			askP, yoloP = askGuardB, yoloPunch
			ax = a0 - 1
			sparks = []spark{{2, 1, '✦'}, {2, 0, '*'}}
		}

	case t < tHit:
		// Ask cowers; yolo draws back a column, then lunges two.
		if int(t/50)%2 == 0 {
			askP = askGuardA
		} else {
			askP = askGuardB
		}
		if t < tLunge {
			yoloP = yoloWind
			yx = y0 + rnd((t-tWind)/(tLunge-tWind))
		} else {
			yoloP = yoloKick
			yx = y0 + 1 - rnd(2*(t-tLunge)/(tHit-tLunge))
		}

	default:
		yx = y0 - 1
		switch {
		case t < tHit+140:
			yoloP = yoloKick
		case t < tCheer:
			yoloP = yoloGuardA
		case int((t-tCheer)/125)%2 == 0:
			yoloP = yoloCheerA
		default:
			yoloP = yoloCheerB
		}
	}

	if askHere {
		cv.sprite(askP, ax-head, 0, kMuted)
	} else {
		flight(cv, t, a0)
	}
	cv.sprite(flip(yoloP), yx-head, 0, yk)
	for _, s := range sparks {
		cv.put(a0+s.dx, s.row, s.r, kWarn, false, 0)
	}

	if dt := t - tHit; dt >= 0 && dt < 200 {
		// The foot lands.
		if dt < 100 {
			cv.put(c-2, 1, '✦', kWarn, false, 0)
			cv.put(c-1, 0, '*', kWarn, false, 0)
		} else {
			cv.put(c-3, 1, '*', kWarn, false, 0)
			cv.put(c-2, 0, '✦', kWarn, false, 0)
		}
	}

	if w, k, bold, flash := stamp(t); w != "" {
		x := yx + 3
		cv.text(x, 0, w, k, bold)
		if flash {
			cv.put(x+len(w), 0, '✦', kWarn, false, 0)
			cv.put(x+len(w)+1, 1, '*', kWarn, false, 0)
		}
	}
}

// flightPos is where the kicked figure's head is at progress u: the column,
// and the top row of its two-row tumbling sprite (it leaves the ground, then
// holds its height).
func flightPos(a0 int, u float64) (x, y int) {
	x = rnd(float64(a0) - float64(a0+3)*launch(u))
	y = 1
	if u > 0.15 {
		y = 0
	}
	return x, y
}

// flight draws the kicked ask figure tumbling off the left edge, the ? sign
// spinning after it, and the dust each leaves.
func flight(cv *canvas, t float64, a0 int) {
	age := t - tHit
	if age < 0 {
		return
	}

	// Dust: a puff where it stood, and a trail along the path.
	if age < tDust {
		f := age / tDust
		cv.put(a0-1, 2, '.', kMuted, false, f)
		cv.put(a0+1, 2, '·', kMuted, false, f)
		cv.put(a0+3, 2, '.', kMuted, false, f)
	}
	for tau, i := 0.0, 0; tau <= math.Min(age, tFlight); tau, i = tau+17, i+1 {
		since := age - tau
		if since < 40 || since > tDust {
			continue
		}
		x, y := flightPos(a0, tau/tFlight)
		r := '·'
		if i%2 == 1 {
			r = '.'
		}
		cv.put(x+1, y+1, r, kMuted, false, since/tDust)
	}

	// The sign flies higher and a little behind, flipping as it goes.
	us := clamp01(age / tSign)
	if us < 1 {
		sx := rnd(float64(a0-2) - float64(a0+4)*us)
		sy := 1
		if us > 0.2 {
			sy = 0
		}
		r := '?'
		if int(age/70)%2 == 1 {
			r = '¿'
		}
		cv.put(sx, sy, r, kMuted, false, 0)
	}

	u := age / tFlight
	if u >= 1 {
		return
	}
	x, y := flightPos(a0, u)
	cv.sprite(tumble[int(u*6)%len(tumble)], x-1, y, kMuted)
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
