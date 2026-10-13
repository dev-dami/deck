// Package decklogo draws Deck's animated wordmark: lowercase "deck" beside
// Deck's mark, a deck of cards seen edge-on whose edges are the agents' live
// usage meters (see Edge). The letters are dot-matrix pixel art in braille
// cells, treated as a liquid surface.
//
// The package is pure. It owns no tea.Cmd and no timers: the host forwards
// mouse and key events, calls Advance on each animation frame, and asks
// View for the block. When Advance reports false the logo is static and the
// host should stop ticking.
//
// How it works. The wordmark is a bitmap at braille-dot resolution (2 dots
// wide, 4 dots tall per cell). A scalar brightness field is computed per dot:
//
//	field = softened letter mask (1-2 dots of glow falloff)
//	      + slow travelling caustic shimmer
//	      + highlight sweep after the intro
//	      + interactive ripples (signed expanding rings)
//
// A dot is lit when field > Bayer 4x4 threshold, so gradients read as pixels,
// not noise. The letters' own dots ("core" dots) are particles: in the intro
// they spring in from a scatter, and a click pushes them outward and lets them
// spring back. A cell's colour runs Faint -> Accent -> highlight by the mean
// level of its lit dots.
package decklogo

import (
	"math"
	"time"
)

// Effect parameters. Tune here.
const (
	// Intro.
	introDelaySpan = 0.26 // s: left-to-right stagger of the droplets
	introOmega     = 12.0 // spring angular frequency
	introZeta      = 0.72 // spring damping ratio (<1 gives a small settle wobble)
	introSettle    = 0.64 // s after a dot's delay when it is at rest
	introDuration  = 900 * time.Millisecond
	sweepStart     = 780 * time.Millisecond
	sweepDuration  = 800 * time.Millisecond
	sweepAmp       = 0.45 // brightness added at the crest of the sweep
	sweepSigma     = 6.0  // dots

	// Idle shimmer.
	shimmerBright = 0.15 // peak brightness modulation
	shimmerLevel  = 0.13 // peak colour-level modulation
	awakeFor      = 4 * time.Second
	shimmerRise   = 800 * time.Millisecond
	shimmerFall   = 2 * time.Second

	// Ripples.
	rippleSpeed  = 34.0 // dots per second
	rippleLambda = 8.0  // dots per cycle
	rippleSigma  = 5.0  // ring width (gaussian) in dots
	rippleLife   = 700 * time.Millisecond
	hoverAmp     = 0.30
	clickAmp     = 0.62
	hoverEvery   = 85 * time.Millisecond // min gap between hover ripples
	calmRate     = 6.0                   // extra exp decay per second after Calm

	// Splash (click displacement).
	splashSpeed         = 70.0 // dots/s the displacement wave travels
	splashOmega         = 24.0
	splashZeta          = 0.5
	splashLife          = 800 * time.Millisecond
	splashPushFull      = 3.0 // dots of peak displacement
	splashPushCompact   = 1.7
	splashRadiusFull    = 16.0
	splashRadiusCompact = 8.0

	baseLevel = 0.62 // colour level of a settled letter dot: exactly Accent
	glowGain  = 0.55 // brightness of the halo next to the letters
)

const (
	maxRipples  = 8
	maxSplashes = 4
	// tinyName is the width of " deck" after the small mark in the
	// one-line fallback.
	tinyName = 5
)

type sizeKind uint8

const (
	kindNone sizeKind = iota
	kindTiny          // one line: the small mark, then "deck"
	kindCompact
	kindFull
	kindHuge // the full wordmark at twice the size
)

type ripple struct {
	cx, cy float64 // dot coordinates
	start  time.Time
	amp    float64
	live   bool
}

type splash struct {
	cx, cy float64
	start  time.Time
	live   bool
}

type particle struct {
	tx, ty int32   // target dot
	sx, sy float32 // intro start dot
	delay  float32 // intro delay, seconds
}

// Model is the logo state. Use New.
type Model struct {
	colors Palette

	// Available area and chosen layout.
	aw, ah     int
	kind       sizeKind
	cols, rows int // the wordmark's drawn size in cells
	dw, dh     int // the wordmark's drawn size in dots
	py         int // the wordmark's rows of padding above and below
	ew         int // cells of card edges left of the wordmark

	// The agents' card edges, and whether they are rising (the intro).
	edges     []Edge
	epal      []palEntry
	edgeOn    bool
	edgeStart time.Time

	// Precomputed per-dot data (len dw*dh).
	glow, near, base []float32
	core             []particle
	rowS1, rowC1     []float32 // static row terms for the shimmer waves
	rowS2, rowC2     []float32
	colS1, colC1     []float32 // per-frame column terms
	colS2, colC2     []float32
	colSw            []float32
	field, level     []float32

	// Per-cell scratch.
	bits []uint8
	sum  []float32
	cnt  []uint8

	// Animation state.
	t0           time.Time // reference for shimmer phase
	now          time.Time
	introOn      bool
	introStart   time.Time
	lastInteract time.Time
	lastHover    time.Time
	hoverX       int
	hoverY       int
	calmAt       time.Time
	amp          float64 // shimmer envelope 0..1
	ripples      [maxRipples]ripple
	nextRipple   int
	splashes     [maxSplashes]splash
	nextSplash   int
	wasMoving    bool
	dirty        bool
	cache        string
	buf          []byte

	pal []palEntry
}

// New returns a logo in its static, finished state. Call Start to play the
// intro.
func New(dark bool) *Model {
	return NewWithPalette(DefaultPalette(dark))
}

// NewWithPalette returns a logo drawn in the given colours, in its static,
// finished state.
func NewWithPalette(p Palette) *Model {
	return &Model{colors: p.fill(DefaultPalette(true)), hoverX: -1, hoverY: -1, dirty: true}
}

// SetDark switches between Deck's own dark and light palette.
func (m *Model) SetDark(dark bool) {
	m.SetPalette(DefaultPalette(dark))
}

// SetPalette draws the logo in the given colours, typically the active
// theme's (see logo.PaletteFor). Unset colours keep their current value.
func (m *Model) SetPalette(p Palette) {
	p = p.fill(m.colors)
	if !p.equal(m.colors) {
		m.colors = p
		m.pal = nil
		m.epal = nil
		m.dirty = true
	}
}

// SetSize tells the logo how much room it has (cells). It picks the full,
// compact or tiny wordmark and adds a little padding for ripples when room
// allows.
func (m *Model) SetSize(w, h int) {
	if w == m.aw && h == m.ah && m.kind != kindNone {
		return
	}
	m.aw, m.ah = w, h
	m.rebuild()
	m.dirty = true
}

// Bounds returns the drawn size in cells (0, 0 when nothing fits).
func (m *Model) Bounds() (w, h int) { return m.ew + m.cols, m.rows }

// Start begins the intro at now. With reduced motion it does nothing visible.
func (m *Model) Start(now time.Time) {
	m.now = now
	m.clearFX()
	m.lastInteract = now
	m.amp = 0
	m.introOn = !reduced() && m.kind >= kindCompact
	m.introStart = now
	m.edgeOn, m.edgeStart = m.introOn, now
	m.wasMoving = true
	m.dirty = true
}

// Calm damps the logo faster; call it on any keypress. It never wakes it.
func (m *Model) Calm(now time.Time) {
	m.calmAt = now
	// Shorten the awake window so the shimmer settles within about a second.
	if cut := now.Add(-awakeFor + time.Second); m.lastInteract.After(cut) {
		m.lastInteract = cut
	}
}

// Mouse reports the pointer at cell (x, y) relative to the logo's top-left.
// Motion emits a gentle ripple; click emits a stronger one and a splash that
// briefly displaces the dots. Out-of-bounds positions are ignored.
func (m *Model) Mouse(x, y int, click bool, now time.Time) {
	if reduced() || m.kind < kindCompact {
		return
	}
	x -= m.ew // the edges do not ripple
	if x < 0 || y < 0 || x >= m.cols || y >= m.rows {
		return
	}
	if now.After(m.now) {
		m.now = now
	}
	cx, cy := float64(2*x+1), float64(4*y+2)
	if click {
		m.addRipple(cx, cy, now, clickAmp)
		m.addSplash(cx, cy, now)
		m.lastHover = now
	} else {
		if x == m.hoverX && y == m.hoverY {
			return
		}
		m.hoverX, m.hoverY = x, y
		if now.Sub(m.lastHover) < hoverEvery {
			m.lastInteract = now
			m.wasMoving = true
			return
		}
		m.lastHover = now
		m.addRipple(cx, cy, now, hoverAmp)
	}
	m.lastInteract = now
	m.dirty = true
	m.wasMoving = true
}

func (m *Model) addRipple(cx, cy float64, now time.Time, amp float64) {
	m.ripples[m.nextRipple] = ripple{cx: cx, cy: cy, start: now, amp: amp, live: true}
	m.nextRipple = (m.nextRipple + 1) % maxRipples
}

func (m *Model) addSplash(cx, cy float64, now time.Time) {
	m.splashes[m.nextSplash] = splash{cx: cx, cy: cy, start: now, live: true}
	m.nextSplash = (m.nextSplash + 1) % maxSplashes
}

func (m *Model) clearFX() {
	for i := range m.ripples {
		m.ripples[i].live = false
	}
	for i := range m.splashes {
		m.splashes[i].live = false
	}
}

// Advance steps the animation to now and reports whether anything is still
// moving. When it returns false the logo is static: the host stops ticking
// until the next Start or Mouse.
func (m *Model) Advance(now time.Time) (moving bool) {
	if reduced() || m.kind < kindCompact {
		if m.introOn || m.amp != 0 || m.edgeOn {
			m.introOn, m.amp, m.edgeOn = false, 0, false
			m.clearFX()
			m.dirty = true
		}
		m.now = now
		return false
	}
	dt := now.Sub(m.now)
	if dt < 0 {
		dt = 0
	}
	if dt > 100*time.Millisecond {
		dt = 100 * time.Millisecond
	}
	if now.After(m.now) {
		m.now = now
	}

	if m.introOn {
		if now.Sub(m.introStart) >= sweepStart+sweepDuration {
			m.introOn = false
			m.amp = 1 // continue from the intro's envelope (already 1)
		} else {
			moving = true
		}
	}
	if m.edgeOn {
		if now.Sub(m.edgeStart) >= edgeRise {
			m.edgeOn = false
			m.dirty = true
		} else {
			moving = true
		}
	}
	for i := range m.ripples {
		r := &m.ripples[i]
		if r.live && now.Sub(r.start) >= rippleLife {
			r.live = false
		}
		moving = moving || r.live
	}
	splashEnd := splashLife + time.Duration(m.splashRadius()/splashSpeed*float64(time.Second))
	for i := range m.splashes {
		s := &m.splashes[i]
		if s.live && now.Sub(s.start) >= splashEnd {
			s.live = false
		}
		moving = moving || s.live
	}

	target := 0.0
	if !m.introOn && now.Sub(m.lastInteract) < awakeFor {
		target = 1
	}
	switch {
	case m.amp < target:
		m.amp = math.Min(target, m.amp+dt.Seconds()/shimmerRise.Seconds())
	case m.amp > target:
		m.amp = math.Max(target, m.amp-dt.Seconds()/shimmerFall.Seconds())
	}
	moving = moving || m.amp > 0

	if moving || m.wasMoving {
		m.dirty = true
	}
	m.wasMoving = moving
	return moving
}

func (m *Model) splashRadius() float64 {
	if m.kind >= kindFull {
		return splashRadiusFull
	}
	return splashRadiusCompact
}

func (m *Model) splashPush() float64 {
	if m.kind >= kindFull {
		return splashPushFull
	}
	return splashPushCompact
}

// rebuild picks the layout for the available area and precomputes all fields.
func (m *Model) rebuild() {
	var bm *bitmap
	ew := m.edgesWidth()
	tiny := smallWidth(len(m.edges)) + tinyName
	switch {
	case m.aw >= ew+(hugeBitmap().w+1)/2 && m.ah >= hugeBitmap().h/4:
		m.kind, bm = kindHuge, hugeBitmap()
	case m.aw >= ew+(fullBitmap().w+1)/2 && m.ah >= fullBitmap().h/4:
		m.kind, bm = kindFull, fullBitmap()
	case m.aw >= ew+(compactBitmap().w+1)/2 && m.ah >= compactBitmap().h/4:
		m.kind, bm = kindCompact, compactBitmap()
	case m.aw >= tiny && m.ah >= 1:
		m.kind = kindTiny
	default:
		m.kind = kindNone
	}
	m.pal = nil
	m.cache = ""
	m.ew, m.py = 0, 0
	switch m.kind {
	case kindNone:
		m.cols, m.rows, m.dw, m.dh = 0, 0, 0, 0
		m.core = nil
		return
	case kindTiny:
		m.cols, m.rows, m.dw, m.dh = tiny, 1, 0, 0
		m.core = nil
		return
	}

	cols0, rows0 := (bm.w+1)/2, bm.h/4
	px := clampInt((m.aw-ew-cols0)/2, 0, 2)
	py := clampInt((m.ah-rows0)/2, 0, 1)
	m.ew, m.py = ew, py
	m.cols, m.rows = cols0+2*px, rows0+2*py
	m.dw, m.dh = m.cols*2, m.rows*4
	ox := px*2 + (cols0*2-bm.w)/2
	oy := py * 4

	n := m.dw * m.dh
	mask := make([]float32, n)
	m.core = m.core[:0]
	rng := uint32(0x9E3779B9)
	next := func() float32 {
		rng ^= rng << 13
		rng ^= rng >> 17
		rng ^= rng << 5
		return float32(rng&0xFFFFFF) / float32(0x1000000)
	}
	for y := 0; y < bm.h; y++ {
		for x := 0; x < bm.w; x++ {
			if !bm.at(x, y) {
				continue
			}
			tx, ty := ox+x, oy+y
			mask[ty*m.dw+tx] = 1
			m.core = append(m.core, particle{
				tx: int32(tx), ty: int32(ty),
				sx:    next() * float32(m.dw),
				sy:    next() * float32(m.dh),
				delay: float32(introDelaySpan)*float32(tx)/float32(m.dw) + next()*0.03,
			})
		}
	}

	g := blur(mask, m.dw, m.dh, 2)
	wide := blur(mask, m.dw, m.dh, 22)
	m.glow = make([]float32, n)
	m.base = make([]float32, n)
	m.near = make([]float32, n)
	for i := range mask {
		if mask[i] > 0 {
			m.glow[i] = 1
			m.base[i] = baseLevel
		} else {
			m.glow[i] = g[i] * glowGain
			m.base[i] = g[i] * baseLevel
		}
		m.near[i] = float32(math.Min(1, float64(wide[i])*2.2))
	}

	m.field = make([]float32, n)
	m.level = make([]float32, n)
	m.colS1, m.colC1 = make([]float32, m.dw), make([]float32, m.dw)
	m.colS2, m.colC2 = make([]float32, m.dw), make([]float32, m.dw)
	m.colSw = make([]float32, m.dw)
	m.rowS1, m.rowC1 = make([]float32, m.dh), make([]float32, m.dh)
	m.rowS2, m.rowC2 = make([]float32, m.dh), make([]float32, m.dh)
	for y := 0; y < m.dh; y++ {
		b1 := shimKy1 * float64(y)
		b2 := shimKy2 * float64(y)
		m.rowS1[y], m.rowC1[y] = float32(math.Sin(b1)), float32(math.Cos(b1))
		m.rowS2[y], m.rowC2[y] = float32(math.Sin(b2)), float32(math.Cos(b2))
	}
	nc := m.cols * m.rows
	m.bits = make([]uint8, nc)
	m.sum = make([]float32, nc)
	m.cnt = make([]uint8, nc)
	m.buf = make([]byte, 0, (m.cols*3+16+m.ew*24)*m.rows*2)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
