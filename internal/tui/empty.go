package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dev-dami/deck/internal/tui/motion"
	"github.com/dev-dami/deck/internal/tui/theme"
)

// The empty state: no thread yet, or a new thread nobody has joined. It shows
// the animated logo, the tagline fading in under it, and three faint hints.

const (
	// introTagLag is how long after the logo starts that the tagline begins to
	// fade in (the logo's own intro runs about a second).
	introTagLag = 700 * time.Millisecond
	introTag    = 600 * time.Millisecond

	// fullLogoRows is the height of the full-size logo. Anything shorter is the
	// compact or tiny one.
	fullLogoRows = 6
)

// emptyPlan is how the empty state is laid out in a w x h area.
type emptyPlan struct {
	w, h         int
	logoW, logoH int // cells drawn by the logo; 0 when it draws nothing
	tag, hints   bool
	top, left    int // top row of the block, left column of the logo
	lines        int // rows in the block
}

// emptyVisible reports whether the empty state is on screen.
func (m *model) emptyVisible() bool {
	th, ok := m.selThread()
	return !ok || len(th.Members) == 0
}

// startIntro begins the one-time logo animation. The logo needs its size
// first (see planEmpty), so this runs once the layout is known.
func (m *model) startIntro(now time.Time) {
	m.logo.Start(now)
	m.logoStarted = true
	m.logoMoving = true
	m.introTag = motion.NewFade(introTag, motion.EaseOutCubic)
	m.introTag.Start(now.Add(introTagLag))
}

func (m *model) introMoving() bool { return m.emptyVisible() && m.introTag.Moving(m.now) }

// planEmpty chooses what fits in w x h and where it goes. The logo is sized
// for each candidate: first with tagline and hints, then with the tagline,
// then alone, and the first that still allows the full-size logo wins; if none
// does, the first that draws anything.
func (m *model) planEmpty(w, h int) emptyPlan {
	if m.plan.w == w && m.plan.h == h && m.plan.lines > 0 {
		return m.plan
	}
	cands := []struct {
		tag, hints bool
		extra      int
	}{{true, true, 6}, {true, false, 2}, {false, false, 0}}

	pick := -1
	var bw, bh int
	try := func(i int, full bool) bool {
		m.logo.SetSize(w, max(0, h-cands[i].extra))
		bw, bh = m.logo.Bounds()
		if bh == 0 {
			return false
		}
		return !full || bh >= fullLogoRows
	}
	for i := range cands {
		if try(i, true) {
			pick = i
			break
		}
	}
	for i := 0; pick < 0 && i < len(cands); i++ {
		if try(i, false) {
			pick = i
		}
	}
	p := emptyPlan{w: w, h: h}
	if pick < 0 { // too small for any logo: plain text
		p.logoW, p.logoH = sw(theme.GlyphAgent+" "+theme.Wordmark), 1
		p.tag, p.hints = h >= 3, h >= 8
		m.logo.SetSize(0, 0)
	} else {
		try(pick, false) // leave the logo sized for the winner
		p.logoW, p.logoH = bw, bh
		p.tag, p.hints = cands[pick].tag, cands[pick].hints
	}
	p.lines = p.logoH
	if p.tag {
		p.lines += 2
	}
	if p.hints {
		p.lines += 4
	}
	p.top = max(0, (h-p.lines)/2-1)
	p.left = max(0, (w-p.logoW)/2)
	m.plan = p
	return p
}

// emptyHints are the three faint lines under the tagline.
func (m *model) emptyHints() []string {
	kind := m.newKind
	dir := tilde(m.newDir, m.home)
	if th, ok := m.selThread(); ok && th.Dir != "" {
		dir = tilde(th.Dir, m.home)
	}
	hints := []string{"type to start " + kind + " in " + dir}
	other := ""
	for _, k := range m.kinds {
		if k != kind {
			other = k
			break
		}
	}
	if other != "" {
		hints = append(hints, "@"+other+" to start with "+other+" instead")
	}
	return append(hints, "/ for commands")
}

// emptyLines draws the centred logo, tagline and hints in a w x h area.
func (m *model) emptyLines(w, h int) []string {
	p := m.planEmpty(w, h)

	var block []string
	var logoLines []string
	if v := m.logo.View(); v != "" {
		logoLines = strings.Split(v, "\n")
	} else { // the logo draws nothing here: the plain mark
		logoLines = []string{sAccent.Render(theme.GlyphAgent) + " " + sBold.Render(theme.Wordmark)}
	}
	pad := strings.Repeat(" ", p.left)
	for _, l := range logoLines {
		block = append(block, pad+l)
	}
	if p.tag {
		tagCol := m.ramp(theme.Faint, theme.Muted, m.introTag.Progress(m.now))
		block = append(block, "", centre(tagCol.Render(theme.Tagline), w))
	}
	if p.hints {
		hints := m.emptyHints()
		maxHint := 0
		for _, hline := range hints {
			maxHint = max(maxHint, sw(hline))
		}
		hl := max(0, (w-maxHint)/2)
		block = append(block, "")
		for _, hline := range hints {
			block = append(block, strings.Repeat(" ", hl)+sFaint.Render(trunc(hline, w)))
		}
	}

	out := make([]string, 0, h)
	for i := 0; i < p.top; i++ {
		out = append(out, "")
	}
	out = append(out, block...)
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}

// logoMouse forwards the pointer to the logo when it is over it. It returns the
// command that wakes the clock, or nil when the pointer was elsewhere.
func (m *model) logoMouse(msg tea.MouseMsg, click bool) tea.Cmd {
	if !m.emptyVisible() || motion.Reduced() {
		return nil
	}
	p := m.plan
	bw, bh := m.logo.Bounds()
	if p.logoH == 0 || bw == 0 || bh == 0 {
		return nil
	}
	x, y := msg.X-m.logoX, msg.Y-m.logoY
	if x < 0 || y < 0 || x >= bw || y >= bh {
		return nil
	}
	m.now = time.Now()
	m.logo.Mouse(x, y, click, m.now)
	m.logoMoving = true
	return m.wake()
}
