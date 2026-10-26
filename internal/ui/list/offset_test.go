package list

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// naiveOffset is Offset as it was before the memo: the heights of every
// item above the viewport, rendered fresh.
func naiveOffset(l *List) int {
	off := 0
	for idx := 0; idx < l.offsetIdx; idx++ {
		off += l.getItem(idx).height
		if l.gap > 0 {
			off += l.gap
		}
	}
	return off + l.offsetLine
}

func offsetTestItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = newTrackedItem(strconv.Itoa(i), strings.Repeat("line\n", 1+i%4), true)
	}
	return items
}

// TestList_OffsetMemoMatchesWalk scrolls both ways, mutates items above
// and below the viewport, and changes the item set, the gap and the width,
// checking the memoized Offset against a full walk each step.
func TestList_OffsetMemoMatchesWalk(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(1))
	items := offsetTestItems(200)
	l := NewList(items...)
	l.SetGap(1)
	l.SetSize(30, 10)
	for step := range 2000 {
		switch r := rng.Intn(20); {
		case r < 12:
			l.ScrollBy(rng.Intn(41) - 20)
		case r < 15:
			// Grow or shrink an item, often one above the viewport.
			it := items[rng.Intn(len(items))].(*trackedItem)
			it.body = strings.Repeat("line\n", 1+rng.Intn(6))
			it.Bump()
		case r == 15:
			l.ScrollToBottom()
		case r == 16:
			l.ScrollToTop()
		case r == 17:
			l.SetGap(rng.Intn(3))
		case r == 18:
			l.SetSize(30+rng.Intn(3), 10)
		default:
			if rng.Intn(2) == 0 && len(items) > 10 {
				idx := rng.Intn(len(items))
				l.RemoveItem(idx)
				items = append(items[:idx:idx], items[idx+1:]...)
			} else {
				extra := offsetTestItems(3)
				l.AppendItems(extra...)
				items = append(items, extra...)
			}
		}
		got := l.Offset()
		require.Equal(t, naiveOffset(l), got, "step %d", step)
	}
}

// BenchmarkListOffset is a scrollbar frame on a long list scrolled to the
// bottom, with and without one line of scrolling per frame.
func BenchmarkListOffset(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		for _, scroll := range []bool{false, true} {
			name := "n=" + strconv.Itoa(n) + "/idle"
			if scroll {
				name = "n=" + strconv.Itoa(n) + "/scrolling"
			}
			b.Run(name, func(b *testing.B) {
				l := NewList(offsetTestItems(n)...)
				l.SetGap(1)
				l.SetSize(80, 40)
				l.RegisterRenderCallback(FocusedRenderCallback(l))
				l.ScrollToBottom()
				l.Offset()
				b.ReportAllocs()
				for i := 0; b.Loop(); i++ {
					if scroll {
						if i%2 == 0 {
							l.ScrollBy(-1)
						} else {
							l.ScrollBy(1)
						}
					}
					_ = l.Offset()
				}
			})
		}
	}
}
