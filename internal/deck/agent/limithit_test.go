package agent

import (
	"testing"
	"time"
)

func TestParseLimitHit(t *testing.T) {
	loc := time.FixedZone("test", 0)
	now := time.Date(2026, 10, 27, 7, 47, 0, 0, loc)
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, loc) }
	cases := []struct {
		text string
		hit  bool
		want time.Time
	}{
		{"Claude AI usage limit reached|1761552000", true, time.Unix(1761552000, 0)},
		{"5-hour limit reached ∙ resets 10am", true, at(27, 10, 0)},
		{"You've hit your limit · resets 3:30pm", true, at(27, 15, 30)},
		{"Weekly limit reached ∙ resets Oct 30, 5pm", true, at(30, 17, 0)},
		{"5-hour limit reached ∙ resets 7am", true, at(28, 7, 0)}, // already past today
		{"You've hit your usage limit. Upgrade to Pro or try again in 2 hours 13 minutes.", true, at(27, 10, 0)},
		{"You've hit your usage limit. Try again at 3:15 PM.", true, at(27, 15, 15)},
		{"Quota exceeded for this model", true, time.Time{}},
		{"API Error: 429 rate limit, retrying", false, time.Time{}},
		{"file not found", false, time.Time{}},
	}
	for _, c := range cases {
		h := ParseLimitHit(c.text, now)
		if (h != nil) != c.hit {
			t.Errorf("%q: hit = %v, want %v", c.text, h != nil, c.hit)
			continue
		}
		if h != nil && !h.ResetsAt.Equal(c.want) {
			t.Errorf("%q: resets %v, want %v", c.text, h.ResetsAt, c.want)
		}
	}
}

func TestParseLimitHitZone(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zone data")
	}
	now := time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)
	h := ParseLimitHit("You've hit your limit · resets 3pm (America/New_York)", now)
	if h == nil {
		t.Fatal("no hit")
	}
	if want := time.Date(2026, 10, 27, 15, 0, 0, 0, ny); !h.ResetsAt.Equal(want) {
		t.Errorf("resets %v, want %v", h.ResetsAt, want)
	}
}
