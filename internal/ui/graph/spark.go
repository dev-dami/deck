package graph

import (
	"image/color"
	"math"
)

// sparkRunes are a sparkline's eight heights, lowest first.
var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// SparkOption configures Spark.
type SparkOption func(*sparkOpts)

type sparkOpts struct{ last bool }

// WithLastValue ends the sparkline with the newest value, compacted.
func WithLastValue() SparkOption { return func(o *sparkOpts) { o.last = true } }

// Spark draws values, oldest first, as one row of blocks in c scaled from
// zero to the largest. The peak is drawn in Fg and the low point in Dim so
// both stand out; zeros sit on the floor in Grid. Values longer than the
// row are averaged into it, so the whole range is always shown.
func Spark(values []float64, width int, t Theme, c color.Color, opts ...SparkOption) string {
	var o sparkOpts
	for _, f := range opts {
		f(&o)
	}
	if width <= 0 || len(values) == 0 {
		return ""
	}
	if c == nil {
		c = t.Accent
	}
	label := ""
	if o.last {
		label = " " + Compact(values[len(values)-1])
	}
	cols := width - len(label)
	if cols < 1 {
		label, cols = "", width
	}
	vs := resample(values, cols)

	peak, low := 0, 0
	for i, v := range vs {
		if v >= vs[peak] {
			peak = i
		}
		if v <= vs[low] {
			low = i
		}
	}
	top := vs[peak]
	g := newGrid(len(vs)+len(label), 1)
	for i, v := range vs {
		level := 0
		if top > 0 {
			level = int(math.Round(max(v, 0) / top * float64(len(sparkRunes)-1)))
		}
		fg, marked := c, len(vs) > 2 && vs[low] < top
		switch {
		case marked && i == peak:
			fg = t.Fg
		case marked && i == low:
			fg = t.Dim
		case v <= 0:
			fg = t.Grid
		}
		g.cells[0][i] = cell{r: sparkRunes[level], fg: fg, bold: marked && i == peak}
	}
	g.text(len(vs), 0, label, t.Subtle, false)
	return g.render()
}

// resample averages values into n buckets; it returns values as they are
// when they already fit.
func resample(values []float64, n int) []float64 {
	if len(values) <= n {
		return values
	}
	out := make([]float64, n)
	for j := range out {
		lo, hi := j*len(values)/n, (j+1)*len(values)/n
		sum := 0.0
		for _, v := range values[lo:hi] {
			sum += v
		}
		out[j] = sum / float64(hi-lo)
	}
	return out
}
