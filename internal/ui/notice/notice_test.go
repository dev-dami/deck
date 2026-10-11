package notice

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreOrderAndIDs(t *testing.T) {
	t.Parallel()
	var s Store
	a := s.Push(Notice{Body: "first"})
	b := s.Push(Notice{Body: "second", Source: SourceGit})
	require.Equal(t, 1, a.ID)
	require.Equal(t, 2, b.ID)
	require.False(t, a.Time.IsZero())
	require.Equal(t, SourceDeck, a.Source)
	list := s.List()
	require.Len(t, list, 2)
	require.Equal(t, "second", list[0].Body, "newest first")
	require.Equal(t, "first", list[1].Body)

	got, ok := s.Get(1)
	require.True(t, ok)
	require.Equal(t, "first", got.Body)
	s.Remove(1)
	_, ok = s.Get(1)
	require.False(t, ok)
	require.Equal(t, 1, s.Len())
}

func TestStoreUnread(t *testing.T) {
	t.Parallel()
	var s Store
	require.Equal(t, 0, s.Unread())
	s.Push(Notice{Body: "a"})
	s.Push(Notice{Body: "b"})
	require.Equal(t, 2, s.Unread())
	s.MarkRead()
	require.Equal(t, 0, s.Unread())
	s.Push(Notice{Body: "c"})
	require.Equal(t, 1, s.Unread())
	s.Clear()
	require.Equal(t, 0, s.Unread())
	require.Equal(t, 0, s.Len())
	n := s.Push(Notice{Body: "d"})
	require.Equal(t, 4, n.ID, "ids keep counting after a clear")
	require.Equal(t, 1, s.Unread())
}

func TestStoreCapsHistory(t *testing.T) {
	t.Parallel()
	var s Store
	for i := 0; i < MaxHistory+10; i++ {
		s.Push(Notice{Body: "x"})
	}
	require.Equal(t, MaxHistory, s.Len())
	require.Equal(t, MaxHistory+10, s.List()[0].ID)
	_, ok := s.Get(1)
	require.False(t, ok, "the oldest went")
}

func TestToastExpiry(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	var ts Toasts
	ts.Add(Notice{ID: 1, Level: Info}, now)
	ts.Add(Notice{ID: 2, Level: Error}, now)
	require.Equal(t, 2, ts.Len())
	require.Equal(t, 2, ts.List()[0].Notice.ID, "newest first")

	require.False(t, ts.Expire(now.Add(ToastTTL-time.Millisecond)))
	require.True(t, ts.Expire(now.Add(ToastTTL)))
	require.Equal(t, 1, ts.Len())
	require.Equal(t, 2, ts.List()[0].Notice.ID, "errors stay longer")
	require.True(t, ts.Expire(now.Add(ErrorTTL)))
	require.Equal(t, 0, ts.Len())
}

func TestToastsKeepNewestThree(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	var ts Toasts
	for i := 1; i <= 5; i++ {
		ts.Add(Notice{ID: i}, now)
	}
	require.Equal(t, MaxToasts, ts.Len())
	require.Equal(t, 5, ts.List()[0].Notice.ID)
	require.Equal(t, 3, ts.List()[2].Notice.ID)
	require.True(t, ts.Dismiss(4))
	require.False(t, ts.Dismiss(4))
	require.Equal(t, 2, ts.Len())
}

func TestToastHoverPauses(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	var ts Toasts
	ts.Add(Notice{ID: 1}, now)
	require.True(t, ts.Hover(1, now.Add(time.Second)))
	require.False(t, ts.Running())
	require.Equal(t, time.Duration(0), ts.Next(now.Add(time.Minute), false), "nothing runs down while held")
	require.False(t, ts.Expire(now.Add(time.Hour)))

	// Let go an hour later: the four seconds it had left start again.
	later := now.Add(time.Hour)
	require.True(t, ts.Hover(0, later))
	require.True(t, ts.Running())
	require.Equal(t, 4*time.Second, ts.List()[0].Remaining(later))
	require.False(t, ts.Expire(later.Add(4*time.Second-time.Millisecond)))
	require.True(t, ts.Expire(later.Add(4*time.Second)))
}

func TestToastNext(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	var ts Toasts
	require.Equal(t, time.Duration(0), ts.Next(now, true))
	ts.Add(Notice{ID: 1}, now)
	require.Equal(t, time.Second/30, ts.Next(now, true), "sliding in")
	require.Equal(t, ToastTTL, ts.Next(now, false), "motion off: just the expiry")
	require.Equal(t, ToastTTL-SlideIn, ts.Next(now.Add(SlideIn), true))
}

func TestSlideProgress(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	tt := Toast{Shown: now}
	require.Equal(t, 1.0, SlideProgress(tt, now, false))
	require.Equal(t, 0.0, SlideProgress(tt, now, true))
	require.Equal(t, 1.0, SlideProgress(tt, now.Add(SlideIn), true))
	mid := SlideProgress(tt, now.Add(SlideIn/2), true)
	require.Greater(t, mid, 0.5)
	require.Less(t, mid, 1.0)
}

func TestNoticeText(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a: b", Notice{Title: "a", Body: "b"}.Text())
	require.Equal(t, "b", Notice{Body: "b"}.Text())
	require.Equal(t, "a", Notice{Title: "a"}.Text())
	require.Equal(t, "15:04", Stamp(time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC), time.Date(2026, 1, 2, 18, 0, 0, 0, time.UTC)))
	require.Equal(t, "Jan 2", Stamp(time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC), time.Date(2026, 1, 3, 18, 0, 0, 0, time.UTC)))
	require.Equal(t, []string{"a …"}, clip([]string{"a", "b"}, 1, 10))
	require.True(t, strings.HasSuffix(clip([]string{"aaaa", "b"}, 1, 4)[0], "…"))
}
