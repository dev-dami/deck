package model

import (
	"cmp"
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dev-dami/deck/internal/deck/gh"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
)

// The git pane's commit graph: the recent commits laid out in lanes, as
// `git log --graph` would, but one row per commit and drawn with
// box-drawing characters. A commit sits on its lane as ● (○ for a merge);
// a lane that ends in it joins with ╯, a merge's other parent starts a lane
// with ╮, and lanes passing by are │.

// Graph glyphs.
const (
	graphCommit = '●'
	graphMerge  = '○'
	graphLane   = '│'
	graphCross  = '┼'
	graphHoriz  = '─'
)

// graphCell is one cell of a graph row: a glyph and the lane whose colour
// it takes (-1 for none).
type graphCell struct {
	r    rune
	lane int
}

// graphRow is one commit's graph: line on the commit's own row, cont on
// the rows its text wraps onto (the lanes going on below it), and col the
// lane the commit sits on.
type graphRow struct {
	line, cont []graphCell
	col        int
}

// layoutGraph lays commits out in lanes. commits must be in topological
// order, children first, as `git log --topo-order` gives them. Every row
// is padded to the widest one so the text after the graph lines up.
func layoutGraph(commits []gh.Commit) []graphRow {
	rows := make([]graphRow, len(commits))
	// lanes holds, per lane, the commit it waits for ("" when free).
	var lanes []string
	width := 0
	for i, c := range commits {
		id := cmp.Or(c.ID, c.Hash)
		col := slices.Index(lanes, id)
		if col < 0 {
			// A branch tip: take the first free lane.
			col = slices.Index(lanes, "")
			if col < 0 {
				col = len(lanes)
				lanes = append(lanes, "")
			}
		}
		before := slices.Clone(lanes)
		before[col] = id

		// Other lanes waiting for this commit end here.
		var joins []int
		for j, h := range lanes {
			if j != col && h == id {
				joins = append(joins, j)
				lanes[j] = ""
			}
		}
		lanes[col] = ""
		var forks, links []int
		if len(c.Parents) > 0 {
			lanes[col] = c.Parents[0]
			for _, p := range c.Parents[1:] {
				if k := slices.Index(lanes, p); k >= 0 {
					// The parent is already on its way: join that lane.
					links = append(links, k)
					continue
				}
				// Start a lane for it right of the commit, never in a lane
				// that ends on this row.
				k := -1
				for j := col + 1; j < len(lanes); j++ {
					if lanes[j] == "" && !slices.Contains(joins, j) {
						k = j
						break
					}
				}
				if k < 0 {
					k = len(lanes)
					lanes = append(lanes, "")
				}
				lanes[k] = p
				forks = append(forks, k)
			}
		}

		n := max(len(before), len(lanes))
		targets := slices.Concat(joins, forks, links)
		lo, hi := col, col
		for _, t := range targets {
			lo, hi = min(lo, t), max(hi, t)
		}
		var line []graphCell
		for x := range n {
			if x > 0 {
				// The gap between lane x-1 and x.
				cell := graphCell{' ', -1}
				if x > lo && x <= hi {
					cell = graphCell{graphHoriz, graphLinkLane(targets, col, x)}
				}
				line = append(line, cell)
			}
			inRange := x > lo && x < hi
			cell := graphCell{' ', -1}
			switch {
			case x == col:
				cell = graphCell{graphCommit, x}
				if len(c.Parents) > 1 {
					cell.r = graphMerge
				}
			case slices.Contains(joins, x):
				cell = graphCell{graphSide(x > col, '╯', '╰'), x}
				if inRange {
					// The line goes on past it to a farther lane.
					cell.r = '┴'
				}
			case slices.Contains(forks, x):
				cell = graphCell{graphSide(x > col, '╮', '╭'), x}
				if inRange {
					cell.r = '┬'
				}
			case slices.Contains(links, x):
				cell = graphCell{graphSide(x > col, '┤', '├'), x}
			case x < len(before) && before[x] != "":
				cell = graphCell{graphLane, x}
				if inRange {
					cell.r = graphCross
				}
			case inRange:
				cell = graphCell{graphHoriz, graphLinkLane(targets, col, x)}
			}
			line = append(line, cell)
		}

		// Free lanes at the right edge go.
		for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
			lanes = lanes[:len(lanes)-1]
		}
		var cont []graphCell
		for x, h := range lanes {
			if x > 0 {
				cont = append(cont, graphCell{' ', -1})
			}
			if h == "" {
				cont = append(cont, graphCell{' ', -1})
			} else {
				cont = append(cont, graphCell{graphLane, x})
			}
		}
		rows[i] = graphRow{line: line, cont: cont, col: col}
		width = max(width, len(line), len(cont))
	}
	for i := range rows {
		rows[i].line = graphPad(rows[i].line, width)
		rows[i].cont = graphPad(rows[i].cont, width)
	}
	return rows
}

func graphSide(right bool, r, l rune) rune {
	if right {
		return r
	}
	return l
}

// graphLinkLane is the lane a horizontal line at x leads to: the nearest
// target at or beyond x, away from col.
func graphLinkLane(targets []int, col, x int) int {
	best := -1
	for _, t := range targets {
		if t > col && t >= x && (best < 0 || t < best) {
			best = t
		}
		if t < col && t < x && (best < 0 || t > best) {
			best = t
		}
	}
	return best
}

func graphPad(cells []graphCell, width int) []graphCell {
	for len(cells) < width {
		cells = append(cells, graphCell{' ', -1})
	}
	return cells
}

// graphPlain is cells as plain text.
func graphPlain(cells []graphCell) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteRune(c.r)
	}
	return b.String()
}

// graphLaneColors colour the lanes in turn.
var graphLaneColors = []color.Color{tone.Mint, tone.Sky, tone.Amber, tone.Plum, tone.Coral, tone.Moss, tone.Codex, tone.Honey}

// graphLaneStyle is lane's colour.
func graphLaneStyle(lane int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(graphLaneColors[lane%len(graphLaneColors)])
}

// graphRender is cells in their lanes' colours.
func graphRender(cells []graphCell) string {
	var b strings.Builder
	run, lane := []rune{}, -2
	flush := func() {
		if len(run) == 0 {
			return
		}
		if lane < 0 {
			b.WriteString(string(run))
		} else {
			b.WriteString(graphLaneStyle(lane).Render(string(run)))
		}
		run = run[:0]
	}
	for _, c := range cells {
		l := c.lane
		if c.r == ' ' {
			l = -1
		}
		if l != lane {
			flush()
			lane = l
		}
		run = append(run, c.r)
	}
	flush()
	return b.String()
}

// graphRefs is a commit's names as small labels: "(HEAD → main,
// origin/main, v1.0)" with each kind in its own colour. Empty when nothing
// points at it.
func graphRefs(t *styles.Styles, refs []gh.Ref) string {
	if len(refs) == 0 {
		return ""
	}
	muted := gitMuted(t)
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		switch r.Kind {
		case gh.RefBranch:
			s := t.Header.GitBranch.Bold(true).Render(r.Name)
			if r.Head {
				s = gitPrimary(t).Bold(true).Render("HEAD → ") + s
			}
			parts = append(parts, s)
		case gh.RefRemote:
			parts = append(parts, lipgloss.NewStyle().Foreground(tone.Coral).Render(r.Name))
		case gh.RefTag:
			parts = append(parts, lipgloss.NewStyle().Foreground(tone.Amber).Render("tag "+r.Name))
		case gh.RefHead:
			parts = append(parts, gitPrimary(t).Bold(true).Render("HEAD"))
		}
	}
	return muted.Render("(") + strings.Join(parts, muted.Render(", ")) + muted.Render(")")
}

// graphBranchLanes maps each local branch in commits to the lane its tip
// sits on in rows.
func graphBranchLanes(commits []gh.Commit, rows []graphRow) map[string]int {
	out := map[string]int{}
	for i, c := range commits {
		for _, r := range c.Refs {
			if r.Kind == gh.RefBranch {
				out[r.Name] = rows[i].col
			}
		}
	}
	return out
}

// graphTipLead is a branch row's lead: the graph's width with the
// branch's tip marked on its lane, so the branch lines up with its tip in
// the graph. Blank when the tip is not in the graph.
func graphTipLead(width, lane int, ok bool) string {
	if width <= 0 {
		return ""
	}
	if !ok || 2*lane >= width {
		return strings.Repeat(" ", width)
	}
	return strings.Repeat(" ", 2*lane) + graphLaneStyle(lane).Render(string(graphCommit)) + strings.Repeat(" ", width-2*lane-1)
}
