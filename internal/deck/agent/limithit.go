package agent

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LimitHit says a turn stopped because the signed-in account's plan ran out:
// the CLI refuses to go on until a window resets. Deck then holds the
// agent's work until ResetsAt instead of failing it (session/waits.go).
type LimitHit struct {
	// Window is the id of the window that ran out, when the CLI names it
	// ("five_hour", "primary", ...); "" when not known.
	Window string `json:",omitempty"`
	// ResetsAt is when the CLI says it may go on; zero when it does not say.
	ResetsAt time.Time `json:",omitzero"`
}

// limitWords are the phrases the CLIs use when the plan, not the request,
// ran out. A bare "rate limit" is left out on purpose: the CLIs retry those
// themselves and they pass in seconds.
var limitWords = regexp.MustCompile(`(?i)(usage limit|limit reached|hit your limit|out of (?:extra )?usage|quota exceeded|exceeded your (?:current )?quota|usage_limit_reached|usagelimitexceeded)`)

var (
	// Claude Code's older form: "Claude AI usage limit reached|1760000000".
	epochSuffix = regexp.MustCompile(`limit reached\|(\d{9,11})`)
	// "resets 3pm", "resets 10:30am (Europe/London)", "resets Oct 30, 5pm".
	resetsAt = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(?:on\s+)?((?:[a-z]{3,9}\.?\s+\d{1,2}(?:,|\s+at)?\s+)?\d{1,2}(?::\d{2})?\s*(?:[ap]\.?m\.?)?)\s*(?:\(([A-Za-z_]+(?:/[A-Za-z_+-]+)*)\))?`)
	// Codex: "try again at 3:15 PM", "try again at 15:15".
	tryAt = regexp.MustCompile(`(?i)try again (?:at|after)\s+(\d{1,2}(?::\d{2})?\s*(?:[ap]\.?m\.?)?)`)
	// Codex: "try again in 2 hours 13 minutes", "in 4 days 1 hour".
	tryIn    = regexp.MustCompile(`(?i)(?:try again|resets?) in\s+((?:\d+\s*(?:days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b[\s,]*(?:and\s+)?)+)`)
	durParts = regexp.MustCompile(`(?i)(\d+)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
)

// ParseLimitHit reads a CLI's error text for a plan limit that ran out, and
// the time it says the limit resets, read in now's zone unless the text
// names one. It returns nil when the text is about something else.
func ParseLimitHit(text string, now time.Time) *LimitHit {
	if !limitWords.MatchString(text) {
		return nil
	}
	h := &LimitHit{}
	if m := epochSuffix.FindStringSubmatch(text); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			h.ResetsAt = time.Unix(n, 0)
			return h
		}
	}
	if m := tryIn.FindStringSubmatch(text); m != nil {
		if d := parseDur(m[1]); d > 0 {
			h.ResetsAt = now.Add(d).Truncate(time.Minute)
			return h
		}
	}
	if m := resetsAt.FindStringSubmatch(text); m != nil {
		loc := now.Location()
		if m[2] != "" {
			if l, err := time.LoadLocation(m[2]); err == nil {
				loc = l
			}
		}
		h.ResetsAt = clockAfter(m[1], now.In(loc))
		return h
	}
	if m := tryAt.FindStringSubmatch(text); m != nil {
		h.ResetsAt = clockAfter(m[1], now)
	}
	return h
}

func parseDur(s string) time.Duration {
	var d time.Duration
	for _, m := range durParts.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		switch u := strings.ToLower(m[2]); {
		case strings.HasPrefix(u, "d"):
			d += time.Duration(n) * 24 * time.Hour
		case strings.HasPrefix(u, "h"):
			d += time.Duration(n) * time.Hour
		case strings.HasPrefix(u, "m"):
			d += time.Duration(n) * time.Minute
		default:
			d += time.Duration(n) * time.Second
		}
	}
	return d
}

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

var clockRe = regexp.MustCompile(`(?i)^(?:([a-z]{3})[a-z]*\.?\s+(\d{1,2})(?:,|\s+at)?\s+)?(\d{1,2})(?::(\d{2}))?\s*(?:([ap])\.?m\.?)?$`)

// clockAfter reads a wall-clock time ("3pm", "15:30", "Oct 30, 5pm") as the
// first such time after now, in now's zone; zero when it cannot.
func clockAfter(s string, now time.Time) time.Time {
	m := clockRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}
	}
	hour, _ := strconv.Atoi(m[3])
	minute, _ := strconv.Atoi(m[4])
	switch strings.ToLower(m[5]) {
	case "p":
		if hour < 12 {
			hour += 12
		}
	case "a":
		if hour == 12 {
			hour = 0
		}
	}
	if hour > 23 || minute > 59 {
		return time.Time{}
	}
	if m[1] != "" {
		mon, ok := months[strings.ToLower(m[1])]
		day, _ := strconv.Atoi(m[2])
		if !ok || day < 1 || day > 31 {
			return time.Time{}
		}
		t := time.Date(now.Year(), mon, day, hour, minute, 0, 0, now.Location())
		if t.Before(now.Add(-24 * time.Hour)) {
			t = t.AddDate(1, 0, 0) // "Jan 2" read in late December
		}
		return t
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}
