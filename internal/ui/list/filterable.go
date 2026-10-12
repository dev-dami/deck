package list

import (
	"slices"

	"github.com/sahilm/fuzzy"
)

// FilterableItem is an item that can be filtered via a query.
type FilterableItem interface {
	Item
	// Filter returns the value to be used for filtering.
	Filter() string
}

// MatchSettable is an interface for items that can have their match indexes
// and match score set.
type MatchSettable interface {
	SetMatch(fuzzy.Match)
}

// FilterableList is a list that takes filterable items that can be filtered
// via a settable query.
type FilterableList struct {
	*List
	items []FilterableItem
	query string

	// filtered memoizes the filter result: dialogs ask for it on every
	// frame (Render does too), and a fuzzy match over every item per
	// frame is the bulk of a large list's frame. It holds while the
	// items, their Filter values and the query hold.
	filtered memoFilter
}

// memoFilter is a memoized filter result and what it depends on.
type memoFilter struct {
	ok    bool
	query string
	keys  []string
	items []Item
}

// NewFilterableList creates a new filterable list.
func NewFilterableList(items ...FilterableItem) *FilterableList {
	f := &FilterableList{
		List:  NewList(),
		items: items,
	}
	f.RegisterRenderCallback(FocusedRenderCallback(f.List))
	f.SetItems(items...)
	return f
}

// SetItems sets the list items and updates the filtered items.
func (f *FilterableList) SetItems(items ...FilterableItem) {
	f.items = items
	f.filtered.ok = false
	fitems := make([]Item, len(items))
	for i, item := range items {
		fitems[i] = item
	}
	f.List.SetItems(fitems...)
}

// AppendItems appends items to the list and updates the filtered items.
func (f *FilterableList) AppendItems(items ...FilterableItem) {
	f.items = append(f.items, items...)
	f.filtered.ok = false
	itms := make([]Item, len(f.items))
	for i, item := range f.items {
		itms[i] = item
	}
	f.List.SetItems(itms...)
}

// PrependItems prepends items to the list and updates the filtered items.
func (f *FilterableList) PrependItems(items ...FilterableItem) {
	f.items = append(items, f.items...)
	f.filtered.ok = false
	itms := make([]Item, len(f.items))
	for i, item := range f.items {
		itms[i] = item
	}
	f.List.SetItems(itms...)
}

// SetFilter sets the filter query and updates the list items.
func (f *FilterableList) SetFilter(q string) {
	f.query = q
	f.List.SetItems(f.FilteredItems()...)
	f.ScrollToTop()
}

// FilterableItemsSource is a type that implements [fuzzy.Source] for filtering
// [FilterableItem]s.
type FilterableItemsSource []FilterableItem

// Len returns the length of the source.
func (f FilterableItemsSource) Len() int {
	return len(f)
}

// String returns the string representation of the item at index i.
func (f FilterableItemsSource) String(i int) string {
	return f[i].Filter()
}

// FilteredItems returns the visible items after filtering. The result is
// the caller's to keep.
func (f *FilterableList) FilteredItems() []Item {
	return slices.Clone(f.filteredItems())
}

// filteredItems returns the memoized filter result, filtering again when
// the items' Filter values or the query changed since. The result is
// shared with the memo and must not be modified.
func (f *FilterableList) filteredItems() []Item {
	c := &f.filtered
	if c.ok && c.query == f.query && len(c.keys) == len(f.items) {
		same := true
		for i, item := range f.items {
			if item.Filter() != c.keys[i] {
				same = false
				break
			}
		}
		if same {
			return c.items
		}
	}
	keys := make([]string, len(f.items))
	for i, item := range f.items {
		keys[i] = item.Filter()
	}
	*c = memoFilter{ok: true, query: f.query, keys: keys, items: f.filter()}
	return c.items
}

// filter matches the items against the query.
func (f *FilterableList) filter() []Item {
	if f.query == "" {
		items := make([]Item, len(f.items))
		for i, item := range f.items {
			if ms, ok := item.(MatchSettable); ok {
				ms.SetMatch(fuzzy.Match{})
				item = ms.(FilterableItem)
			}
			items[i] = item
		}
		return items
	}

	items := FilterableItemsSource(f.items)
	matches := fuzzy.FindFrom(f.query, items)
	matchedItems := []Item{}
	resultSize := len(matches)
	for i := range resultSize {
		match := matches[i]
		item := items[match.Index]
		if ms, ok := item.(MatchSettable); ok {
			ms.SetMatch(match)
			item = ms.(FilterableItem)
		}
		matchedItems = append(matchedItems, item)
	}

	return matchedItems
}

// Render renders the filterable list.
func (f *FilterableList) Render() string {
	f.List.SetItems(f.FilteredItems()...)
	return f.List.Render()
}
