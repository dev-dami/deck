package list

import (
	"strings"
	"testing"

	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

// matchedItem is a filterable item that counts SetMatch calls, which the
// filterable list makes once per item per filtering pass.
type matchedItem struct {
	*trackedItem
	filter  string
	matches int
}

func (m *matchedItem) Filter() string       { return m.filter }
func (m *matchedItem) SetMatch(fuzzy.Match) { m.matches++ }

// TestFilterableList_FiltersOncePerChange asserts repeated renders and
// FilteredItems calls reuse one filtering pass until the query, the items
// or an item's Filter value changes.
func TestFilterableList_FiltersOncePerChange(t *testing.T) {
	t.Parallel()

	a := &matchedItem{trackedItem: newTrackedItem("a", "alpha", true), filter: "alpha"}
	b := &matchedItem{trackedItem: newTrackedItem("b", "bravo", true), filter: "bravo"}
	f := NewFilterableList(a, b)
	f.SetSize(20, 5)

	f.SetFilter("al")
	passes := a.matches
	for range 5 {
		_ = f.Render()
		require.Len(t, f.FilteredItems(), 1)
	}
	require.Equal(t, passes, a.matches, "steady frames do not filter again")

	b.filter = "albatross"
	require.Len(t, f.FilteredItems(), 2, "a changed Filter value filters again")

	f.SetFilter("")
	require.Len(t, f.FilteredItems(), 2)
	f.SetItems(a)
	require.Len(t, f.FilteredItems(), 1, "new items filter again")
}

// TestList_SetItemsSameKeepsCache asserts setting the items a list already
// holds keeps the render cache and the cached total height.
func TestList_SetItemsSameKeepsCache(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha", true)
	b := newTrackedItem("b", "bravo", true)
	l := NewList(a, b)
	l.SetSize(20, 5)
	require.Equal(t, 2, l.TotalHeight())
	hits := a.renderHits + b.renderHits

	l.SetItems(a, b)
	require.Equal(t, 2, l.TotalHeight())
	_ = l.Render()
	require.Equal(t, hits, a.renderHits+b.renderHits, "same items render nothing again")

	l.SetItems(a)
	require.Equal(t, 1, l.TotalHeight(), "a new item set recomputes the total height")
}

// TestFilterableList_PreserveOrder asserts a list told to preserve order
// keeps matches in the caller's order on Render, while the default sorts
// them by fuzzy score.
func TestFilterableList_PreserveOrder(t *testing.T) {
	t.Parallel()

	// "ab" scores higher on "ab" than on "xxxxaxxxxb" (leading and gap
	// penalties); give the weaker match first so the two orders differ.
	weak := &matchedItem{trackedItem: newTrackedItem("w", "weak", true), filter: "xxxxaxxxxb"}
	strong := &matchedItem{trackedItem: newTrackedItem("s", "strong", true), filter: "ab"}
	ids := func(items []Item) []string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.(*matchedItem).id
		}
		return out
	}

	f := NewFilterableList(weak, strong)
	f.SetSize(20, 5)
	f.SetFilter("ab")
	require.Equal(t, []string{"s", "w"}, ids(f.FilteredItems()), "default sorts by score")

	f.SetPreserveOrder(true)
	require.Equal(t, []string{"w", "s"}, ids(f.FilteredItems()), "preserve order keeps the caller's order")
	out := f.Render()
	require.Less(t, strings.Index(out, "weak"), strings.Index(out, "strong"), "Render keeps the caller's order")
}
