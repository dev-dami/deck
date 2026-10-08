package approve

import (
	"strings"

	"github.com/dev-dami/deck/internal/agent"
)

const diffContext = 3

// maxLCSCells bounds the LCS table; larger inputs fall back to a full replace hunk.
const maxLCSCells = 4_000_000

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
