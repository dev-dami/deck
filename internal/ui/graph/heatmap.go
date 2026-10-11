package graph

import (
	"slices"
	"time"
)

// heatGutter is the width of the weekday labels left of a heatmap.
const heatGutter = 4

// Heatmap draws daily values as a GitHub-style calendar: a column per week
// (Sunday at the top), a ■ per day coloured by quartile of the days with
// anything on them, month names above and Mon/Wed/Fri on the left. daily is
// oldest first and ends on end's day. Only the newest weeks that fit width
// are drawn, and no more weeks than daily covers.
func Heatmap(t Theme, width int, end time.Time, daily []float64) string {
	if len(daily) == 0 || width < heatGutter+1 {
		return ""
	}
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	offset := int(endDay.Weekday()) // days of end's week before it
	weeks := min((width-heatGutter+1)/2, (len(daily)+(6-offset)+6)/7)
	weeks = max(weeks, 1)
	firstSun := endDay.AddDate(0, 0, -offset-7*(weeks-1))

	// value is the value on the day k days after firstSun, and whether
	// that day is drawn at all (days after end are not).
	value := func(k int) (float64, bool) {
		day := firstSun.AddDate(0, 0, k)
		if day.After(endDay) {
			return 0, false
		}
		back := int(endDay.Sub(day).Round(24*time.Hour) / (24 * time.Hour))
		if i := len(daily) - 1 - back; i >= 0 {
			return daily[i], true
		}
		return 0, true
	}

	var nonzero []float64
	for k := range weeks * 7 {
		if v, ok := value(k); ok && v > 0 {
			nonzero = append(nonzero, v)
		}
	}
	slices.Sort(nonzero)
	quart := func(p float64) float64 {
		if len(nonzero) == 0 {
			return 0
		}
		return nonzero[int(p*float64(len(nonzero)-1))]
	}
	cuts := []float64{quart(0.25), quart(0.5), quart(0.75)}
	level := func(v float64) int {
		if v <= 0 {
			return 0
		}
		l := 1
		for _, c := range cuts {
			if v > c {
				l++
			}
		}
		return l
	}

	g := newGrid(heatGutter+2*weeks-1, 8)
	for _, d := range []struct {
		row  int
		name string
	}{{1, "Mon"}, {3, "Wed"}, {5, "Fri"}} {
		g.text(0, d.row+1, d.name, t.Subtle, false)
	}
	for w := range weeks {
		for d := range 7 {
			if v, ok := value(w*7 + d); ok {
				g.set(heatGutter+2*w, d+1, '■', t.heat(level(v)))
			}
		}
	}

	// Month names sit over the first week starting in that month. The
	// first column's month is dropped when the next name would crowd it.
	type mark struct {
		x int
		s string
	}
	var marks []mark
	for w := range weeks {
		sun := firstSun.AddDate(0, 0, 7*w)
		if w == 0 || sun.Month() != sun.AddDate(0, 0, -7).Month() {
			marks = append(marks, mark{heatGutter + 2*w, sun.Format("Jan")})
		}
	}
	if len(marks) > 1 && marks[0].x+len(marks[0].s) >= marks[1].x {
		marks = marks[1:]
	}
	edge := -1
	for _, m := range marks {
		if m.x > edge && m.x+len(m.s) <= g.w {
			g.text(m.x, 0, m.s, t.Subtle, false)
			edge = m.x + len(m.s)
		}
	}
	return g.render()
}
