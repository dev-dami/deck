package approve

import (
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
)

const diffContext = 3

// maxLCSCells bounds the LCS table (4 bytes a cell); larger inputs go to
// myersOps, whose memory grows with the number of edits instead.
const maxLCSCells = 250_000

// Myers' bounds: past them an edit falls back to a full replace hunk.
const (
	maxEditD    = 1000      // edits
	maxEditWork = 8_000_000 // steps (about what a 4M-cell table took)
)

type editOp struct {
	op   agent.LineOp
	text string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// BuildEditDiff builds a unified-style diff between old and new text for path.
// An empty old with non-empty new is reported as a new file.
func BuildEditDiff(path, old, new string) *agent.FileDiff {
	a, b := splitLines(old), splitLines(new)
	d := &agent.FileDiff{Path: path, New: old == "" && new != "", Deleted: new == "" && old != ""}
	ops := diffOps(a, b)
	for _, o := range ops {
		switch o.op {
		case agent.OpAdd:
			d.Added++
		case agent.OpDel:
			d.Removed++
		}
	}
	d.Hunks = buildHunks(ops)
	return d
}

func diffOps(a, b []string) []editOp {
	// common prefix / suffix
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var ops []editOp
	for _, l := range a[:pre] {
		ops = append(ops, editOp{agent.OpContext, l})
	}
	ops = append(ops, lcsOps(ma, mb)...)
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, editOp{agent.OpContext, l})
	}
	return ops
}

func lcsOps(a, b []string) []editOp {
	n, m := len(a), len(b)
	var ops []editOp
	if n > 0 && m > 0 && n*m > maxLCSCells {
		if ops, ok := myersOps(a, b); ok {
			return ops
		}
	}
	if n == 0 || m == 0 || n*m > maxLCSCells {
		for _, l := range a {
			ops = append(ops, editOp{agent.OpDel, l})
		}
		for _, l := range b {
			ops = append(ops, editOp{agent.OpAdd, l})
		}
		return ops
	}
	// dp[i][j] = LCS length of a[i:], b[j:]
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, editOp{agent.OpContext, a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, editOp{agent.OpDel, a[i]})
			i++
		default:
			ops = append(ops, editOp{agent.OpAdd, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, editOp{agent.OpDel, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, editOp{agent.OpAdd, b[j]})
	}
	return ops
}

// myersOps is a shortest edit script from a to b by Myers' O(ND) algorithm,
// for inputs too big for the LCS table. It keeps one row of the search per
// edit (O(D²) memory), and gives up (false) past maxEditD edits or
// maxEditWork steps. Within each run of changes deletions come first, as
// lcsOps has them.
func myersOps(a, b []string) ([]editOp, bool) {
	ids := map[string]int32{}
	intern := func(ls []string) []int32 {
		out := make([]int32, len(ls))
		for i, l := range ls {
			id, ok := ids[l]
			if !ok {
				id = int32(len(ids))
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	ia, ib := intern(a), intern(b)
	n, m := len(a), len(b)
	maxD := min(n+m, maxEditD)
	off := maxD + 1
	v := make([]int32, 2*maxD+3)
	var trace [][]int32 // trace[d][k+d] is v[k] after step d
	work := 0
	end := -1
search:
	for d := 0; d <= maxD; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = int(v[off+k+1]) // down: an insertion
			} else {
				x = int(v[off+k-1]) + 1 // right: a deletion
			}
			y := x - k
			for x < n && y < m && ia[x] == ib[y] {
				x++
				y++
				work++
			}
			v[off+k] = int32(x)
			work++
			if x >= n && y >= m {
				end = d
				break search
			}
		}
		if work > maxEditWork {
			return nil, false
		}
		trace = append(trace, append([]int32(nil), v[off-d:off+d+1]...))
	}
	if end < 0 {
		return nil, false
	}

	// Walk back from (n, m), collecting the script in reverse.
	rev := make([]editOp, 0, n+m)
	x, y := n, m
	for d := end; d > 0; d-- {
		prev := trace[d-1]
		at := func(k int) int { return int(prev[k+d-1]) }
		k := x - y
		pk := k - 1
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			rev = append(rev, editOp{agent.OpContext, a[x-1]})
			x--
			y--
		}
		if pk == k+1 {
			rev = append(rev, editOp{agent.OpAdd, b[py]})
		} else {
			rev = append(rev, editOp{agent.OpDel, a[px]})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		rev = append(rev, editOp{agent.OpContext, a[x-1]})
		x--
		y--
	}
	ops := make([]editOp, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		ops = append(ops, rev[i])
	}
	// Deletions before additions in each run of changes.
	for i := 0; i < len(ops); {
		if ops[i].op == agent.OpContext {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].op != agent.OpContext {
			j++
		}
		run := ops[i:j]
		sorted := make([]editOp, 0, len(run))
		for _, o := range run {
			if o.op == agent.OpDel {
				sorted = append(sorted, o)
			}
		}
		for _, o := range run {
			if o.op == agent.OpAdd {
				sorted = append(sorted, o)
			}
		}
		copy(run, sorted)
		i = j
	}
	return ops, true
}

func buildHunks(ops []editOp) []agent.Hunk {
	// indexes of changed ops
	var changed []int
	for i, o := range ops {
		if o.op != agent.OpContext {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	// line numbers before each op
	oldNo := make([]int, len(ops)+1)
	newNo := make([]int, len(ops)+1)
	for i, o := range ops {
		oldNo[i+1], newNo[i+1] = oldNo[i], newNo[i]
		if o.op != agent.OpAdd {
			oldNo[i+1]++
		}
		if o.op != agent.OpDel {
			newNo[i+1]++
		}
	}

	var hunks []agent.Hunk
	start := max(changed[0]-diffContext, 0)
	end := min(changed[0]+diffContext+1, len(ops)) // exclusive
	flush := func() {
		h := agent.Hunk{OldStart: oldNo[start] + 1, NewStart: newNo[start] + 1}
		for _, o := range ops[start:end] {
			h.Lines = append(h.Lines, agent.DiffLine{Op: o.op, Text: o.text})
			if o.op != agent.OpAdd {
				h.OldLines++
			}
			if o.op != agent.OpDel {
				h.NewLines++
			}
		}
		if h.OldLines == 0 {
			h.OldStart = oldNo[start]
		}
		if h.NewLines == 0 {
			h.NewStart = newNo[start]
		}
		hunks = append(hunks, h)
	}
	for _, c := range changed[1:] {
		if c-diffContext <= end {
			end = min(c+diffContext+1, len(ops))
			continue
		}
		flush()
		start = c - diffContext
		end = min(c+diffContext+1, len(ops))
	}
	flush()
	return hunks
}
