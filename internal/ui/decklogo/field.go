package decklogo

import (
	"math"
	"time"
)

// Shimmer wave geometry (dots, seconds). Two slow travelling waves in
// different directions interfere into a caustic-like drift.
var (
	shimKx1 = 2 * math.Pi / 30
	shimKy1 = 2 * math.Pi / 44
	shimW1  = 2 * math.Pi / 6.5
	shimKx2 = 2 * math.Pi / 19
	shimKy2 = -2 * math.Pi / 26
	shimW2  = 2 * math.Pi / 9.0
)

// bayer4 is the 4x4 ordered-dither matrix, normalised to thresholds in (0,1).
var bayer4 = func() (t [16]float32) {
	m := [16]int{
		0, 8, 2, 10,
		12, 4, 14, 6,
		3, 11, 1, 9,
		15, 7, 13, 5,
	}
	for i, v := range m {
		t[i] = (float32(v) + 0.5) / 16
	}
	return
}()

func threshold(x, y int) float32 { return bayer4[(y&3)<<2|(x&3)] }

// brailleBit[col][row] is the bit of a dot inside a braille cell.
var brailleBit = [2][4]uint8{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

// splashPeak normalises the splash response to a peak of 1.
var splashPeak = func() float64 {
	wd := splashOmega * math.Sqrt(1-splashZeta*splashZeta)
	peak := 0.0
	for a := 0.0; a < 1; a += 0.002 {
		peak = math.Max(peak, math.Exp(-splashZeta*splashOmega*a)*math.Sin(wd*a))
	}
	return peak
}()

// blur applies a [1 2 1]/4 kernel passes times in each axis, clamping at the
// edges. Interior values of wide shapes stay at 1.
func blur(src []float32, w, h, passes int) []float32 {
	a := append([]float32(nil), src...)
	b := make([]float32, len(a))
	for p := 0; p < passes; p++ {
		for y := 0; y < h; y++ {
			row := y * w
			for x := 0; x < w; x++ {
				l, r := x-1, x+1
				if l < 0 {
					l = 0
				}
				if r >= w {
					r = w - 1
				}
				b[row+x] = (a[row+l] + 2*a[row+x] + a[row+r]) / 4
			}
		}
		for y := 0; y < h; y++ {
			u, d := y-1, y+1
			if u < 0 {
				u = 0
			}
			if d >= h {
				d = h - 1
			}
			for x := 0; x < w; x++ {
				a[y*w+x] = (b[u*w+x] + 2*b[y*w+x] + b[d*w+x]) / 4
			}
		}
	}
	return a
}

// calmFactor decays an effect that started at start once Calm has been called.
func (m *Model) calmFactor(start time.Time) float64 {
	if m.calmAt.After(start) && m.now.After(m.calmAt) {
		return math.Exp(-calmRate * m.now.Sub(m.calmAt).Seconds())
	}
	return 1
}

type activeRipple struct {
	cx, cy, R, lo2, hi2, amp float64
}

// computeField fills m.field (brightness vs the Bayer threshold) and m.level
// (colour level 0..1) for every dot at m.now. No allocation.
func (m *Model) computeField() {
	now := m.now
	if m.t0.IsZero() {
		m.t0 = now
	}
	t := now.Sub(m.t0).Seconds()
	// amp is the motion envelope shared by the shimmer and the halo: it fades
	// in during the intro, holds while awake and falls to zero as the logo
	// settles, so the resting frame is the clean letterforms only.
	amp := float32(m.amp)
	if m.introOn {
		amp = smooth((now.Sub(m.introStart).Seconds() - 0.55) / 0.35)
	}
	shim := amp > 0.001

	if shim {
		for x := 0; x < m.dw; x++ {
			fx := float64(x)
			a1 := shimKx1*fx - shimW1*t
			a2 := shimKx2*fx - shimW2*t + 1.7
			m.colS1[x], m.colC1[x] = float32(math.Sin(a1)), float32(math.Cos(a1))
			m.colS2[x], m.colC2[x] = float32(math.Sin(a2)), float32(math.Cos(a2))
		}
	}

	// Intro glow and sweep.
	sweeping := false
	if m.introOn {
		el := now.Sub(m.introStart).Seconds()
		p := (el - sweepStart.Seconds()) / sweepDuration.Seconds()
		if p > 0 && p < 1 {
			sweeping = true
			e := 1 - math.Pow(1-p, 2.2)
			xs := -2*sweepSigma + e*(float64(m.dw)+4*sweepSigma)
			for x := 0; x < m.dw; x++ {
				u := (float64(x) - xs) / sweepSigma
				m.colSw[x] = float32(math.Exp(-u * u))
			}
		}
	}

	// Active ripples.
	var rs [maxRipples]activeRipple
	nr := 0
	for i := range m.ripples {
		r := &m.ripples[i]
		if !r.live {
			continue
		}
		age := now.Sub(r.start).Seconds()
		if age < 0 {
			age = 0
		}
		life := rippleLife.Seconds()
		env := 1 - age/life
		if env <= 0 {
			continue
		}
		R := rippleSpeed * age
		a := r.amp * env * env * m.calmFactor(r.start) / (1 + R/24)
		if a < 0.005 {
			continue
		}
		lo, hi := math.Max(0, R-3*rippleSigma), R+3*rippleSigma
		rs[nr] = activeRipple{r.cx, r.cy, R, lo * lo, hi * hi, a}
		nr++
	}

	invSig2 := 1 / (rippleSigma * rippleSigma)
	kLam := 2 * math.Pi / rippleLambda
	dw := m.dw
	for y := 0; y < m.dh; y++ {
		fy := float64(y) + 0.5
		rs1, rc1, rs2, rc2 := m.rowS1[y], m.rowC1[y], m.rowS2[y], m.rowC2[y]
		for x := 0; x < dw; x++ {
			i := y*dw + x
			f := m.glow[i]
			if f < 1 {
				f *= amp // halo exists only while there is motion
			}
			l := m.base[i]
			if shim {
				s := 0.5 * (m.colS1[x]*rc1 + m.colC1[x]*rs1 + m.colS2[x]*rc2 + m.colC2[x]*rs2)
				w := m.near[i]
				f += amp * shimmerBright * s * (0.25 + 0.75*w)
				l += amp * shimmerLevel * s * w
			}
			if sweeping {
				g := m.colSw[x]
				w := m.near[i]
				f += g * sweepAmp * (0.3 + 0.7*w)
				l += g * 0.32 * w
			}
			if nr > 0 {
				fx := float64(x) + 0.5
				var v float64
				for k := 0; k < nr; k++ {
					r := &rs[k]
					dx, dy := fx-r.cx, fy-r.cy
					d2 := dx*dx + dy*dy
					if d2 < r.lo2 || d2 > r.hi2 {
						continue
					}
					u := math.Sqrt(d2) - r.R
					v += r.amp * math.Exp(-u*u*invSig2) * math.Cos(kLam*u)
				}
				f += float32(v)
				l += float32(v * 0.8)
			}
			m.field[i] = f
			m.level[i] = l
		}
	}
}

func smooth(x float64) float32 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	return float32(x * x * (3 - 2*x))
}

func clamp01f(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// introPos returns the spring position factor s (1 at the start scatter,
// 0 at the target) for a dot that has been released for tl seconds.
func introSpring(tl float64) float64 {
	if tl <= 0 {
		return 1
	}
	if tl >= introSettle {
		return 0
	}
	wd := introOmega * math.Sqrt(1-introZeta*introZeta)
	return math.Exp(-introZeta*introOmega*tl) * (math.Cos(wd*tl) + introZeta*introOmega/wd*math.Sin(wd*tl))
}

// plot lights dot (x,y) at colour level lvl.
func (m *Model) plot(x, y int, lvl float32) {
	if x < 0 || y < 0 || x >= m.dw || y >= m.dh {
		return
	}
	c := (y>>2)*m.cols + x>>1
	bit := brailleBit[x&1][y&3]
	if m.bits[c]&bit != 0 {
		return
	}
	m.bits[c] |= bit
	m.sum[c] += clamp01f(lvl)
	m.cnt[c]++
}

// compose builds the per-cell bit masks and mean levels for the current frame.
func (m *Model) compose() {
	for i := range m.bits {
		m.bits[i], m.sum[i], m.cnt[i] = 0, 0, 0
	}
	m.computeField()

	// Halo and any background lit by ripples/shimmer: every non-core dot.
	dw := m.dw
	for y := 0; y < m.dh; y++ {
		for x := 0; x < dw; x++ {
			i := y*dw + x
			if m.glow[i] >= 1 {
				continue
			}
			if m.field[i] > threshold(x, y) {
				m.plot(x, y, m.level[i])
			}
		}
	}

	// Core dots (the letters) as particles.
	now := m.now
	intro := m.introOn
	var el float64
	if intro {
		el = now.Sub(m.introStart).Seconds()
	}
	nsp := 0
	for i := range m.splashes {
		if m.splashes[i].live {
			nsp++
		}
	}
	R := m.splashRadius()
	push := m.splashPush()
	wd := splashOmega * math.Sqrt(1-splashZeta*splashZeta)

	for k := range m.core {
		p := &m.core[k]
		tx, ty := int(p.tx), int(p.ty)
		i := ty*dw + tx
		px, py := float64(tx), float64(ty)
		lvl := m.level[i]

		if intro {
			s := introSpring(el - float64(p.delay))
			px += (float64(p.sx) - px) * s
			py += (float64(p.sy) - py) * s
			q := float32(1 - math.Min(1, math.Abs(s)))
			lvl = 0.1 + q*(lvl-0.1)
		} else if m.field[i] <= threshold(tx, ty) {
			continue
		}

		if nsp > 0 {
			for j := range m.splashes {
				sp := &m.splashes[j]
				if !sp.live {
					continue
				}
				dx, dy := float64(tx)+0.5-sp.cx, float64(ty)+0.5-sp.cy
				d := math.Hypot(dx, dy)
				if d >= R {
					continue
				}
				age := now.Sub(sp.start).Seconds()
				a := age - d/splashSpeed
				if a <= 0 {
					continue
				}
				resp := math.Exp(-splashZeta*splashOmega*a) * math.Sin(wd*a) / splashPeak
				fall := 1 - d/R
				amt := push * fall * resp * m.calmFactor(sp.start)
				if d < 0.5 {
					dx, dy, d = 0, -1, 1
				}
				px += dx / d * amt
				py += dy / d * amt
				lvl += float32(math.Abs(resp)*fall) * 0.25
			}
		}
		m.plot(int(math.Floor(px+0.5)), int(math.Floor(py+0.5)), lvl)
	}
}
