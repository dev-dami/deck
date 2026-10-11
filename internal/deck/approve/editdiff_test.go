package approve

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// apply rebuilds the old and new text an edit script describes.
func apply(ops []editOp) (old, new []string) {
	for _, o := range ops {
		if o.op != agent.OpAdd {
			old = append(old, o.text)
		}
		if o.op != agent.OpDel {
			new = append(new, o.text)
		}
	}
	return old, new
}

func edits(ops []editOp) (n int) {
	for _, o := range ops {
		if o.op != agent.OpContext {
			n++
		}
	}
	return n
}

func TestMyersMatchesLCS(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	words := []string{"a", "b", "c", "d", "e", "f"}
	gen := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = words[rng.Intn(len(words))]
		}
		return out
	}
	for i := 0; i < 300; i++ {
		a, b := gen(rng.Intn(40)+1), gen(rng.Intn(40)+1)
		lcs := lcsOps(a, b)
		my, ok := myersOps(a, b)
		if !ok {
			t.Fatalf("myers gave up on %v %v", a, b)
		}
		if edits(my) != edits(lcs) {
			t.Fatalf("myers %d edits, lcs %d, for %v -> %v", edits(my), edits(lcs), a, b)
		}
		oa, ob := apply(my)
		if strings.Join(oa, ",") != strings.Join(a, ",") || strings.Join(ob, ",") != strings.Join(b, ",") {
			t.Fatalf("myers script does not rebuild %v -> %v: %v", a, b, my)
		}
		for j := 1; j < len(my); j++ { // deletions come first in a run of changes
			if my[j-1].op == agent.OpAdd && my[j].op == agent.OpDel {
				t.Fatalf("addition before deletion in %v", my)
			}
		}
	}
}

func TestLargeEditDiffsStayReal(t *testing.T) {
	// 1500 x 1500 lines is past the LCS table: Myers diffs it.
	var old, nw []string
	for i := 0; i < 1500; i++ {
		old = append(old, fmt.Sprintf("line %d", i))
		nw = append(nw, fmt.Sprintf("line %d", i))
	}
	old[700], nw[700] = "old middle", "new middle"
	nw[10] = "changed near the top"
	old[1490] = "changed near the end"
	d := BuildEditDiff("big.txt", strings.Join(old, "\n"), strings.Join(nw, "\n"))
	if d.Added != 3 || d.Removed != 3 || len(d.Hunks) != 3 {
		t.Fatalf("added %d removed %d hunks %d", d.Added, d.Removed, len(d.Hunks))
	}
	// Two unrelated texts past Myers' bounds fall back to one replace hunk.
	var x, y []string
	for i := 0; i < 3000; i++ {
		x = append(x, fmt.Sprintf("x%d", i))
		y = append(y, fmt.Sprintf("y%d", i))
	}
	d = BuildEditDiff("swap.txt", strings.Join(x, "\n"), strings.Join(y, "\n"))
	if d.Added != 3000 || d.Removed != 3000 || len(d.Hunks) != 1 {
		t.Fatalf("replace: added %d removed %d hunks %d", d.Added, d.Removed, len(d.Hunks))
	}
}
