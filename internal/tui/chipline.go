package tui

import (
	"github.com/dev-dami/deck/internal/session"
)

// chipLine is the line above the composer: who is in the thread (their ◆
// chips, tinted, with the model each runs) and where it works, and on the
// right where the text you are typing would go and the permission mode. An
// empty thread shows the kind, model and directory it will start with.
func (m *model) chipLine(w int) string {
	line, _ := m.chipLayout(w)
	return line
}

// modelLabel is the model a member runs, as the chip names it.
func (m *model) modelLabel(a session.AgentInfo) string {
	model := a.RunningModel
	if model == "" {
		model = a.Model
	}
	if model == "" {
		model = m.defaultModel(a.Kind)
	}
	if model == "" {
		model = "default"
	}
	return model
}

// chipLayout draws the chip line and returns the spans a click can hit. The
// spans are in cells from the left edge of the main area, so they always agree
// with what is drawn.
func (m *model) chipLayout(w int) (string, []chipHit) {
	if m.prompt.active() {
		return "  " + sWarn.Render(trunc(m.prompt.label(m), w-2)), nil
	}
	if m.pick.open {
		return m.pickChipLine(w), nil
	}
	if m.edit != nil {
		return "  " + sWarn.Render(trunc(editLabel, w-2)), nil
	}
	th, ok := m.selThread()
	sep := sFaint.Render(" " + glyphDot + " ")
	dir := m.newDir
	members := ok && len(th.Members) > 0
	if members {
		dir = th.Dir
	} else if ok && th.Dir != "" {
		dir = th.Dir
	}

	// build lays out the left side; models says whether every member names its
	// model (a lone member always does).
	build := func(models, meters bool) (string, []chipHit) {
		left := "  "
		var hits []chipHit
		if members {
			for i, a := range th.Members {
				if i > 0 {
					left += "  "
				}
				x0 := sw(left)
				left += m.chip(a)
				if models || len(th.Members) == 1 {
					left += sep + sFaint.Render(m.modelLabel(a))
				}
				if ms := meter(a); meters && ms != "" {
					left += sep + ms
				}
				hits = append(hits, chipHit{x0: x0, x1: sw(left), kind: hitModel, agent: a.ID})
			}
			return left, hits
		}
		kind := m.newKind
		model := m.newModelFor(kind)
		if model == "" {
			model = "default"
		}
		x0 := sw(left)
		left += sMuted.Render(kind) + sep + sFaint.Render(model)
		return left, append(hits, chipHit{x0: x0, x1: sw(left), kind: hitModel})
	}

	hint := addressHint(m.comp.Value(), th, m.kinds)
	if m.menu.open() { // still choosing the name: the menu says it
		hint = ""
	}
	mode := m.modeChip()
	mw := sw(mode)

	var left string
	var hits []chipHit
	hintW := 0
fit:
	for _, lv := range [][2]bool{{true, true}, {false, true}, {false, false}} {
		for _, h := range []string{hint, ""} {
			hw := 0
			if h != "" {
				hw = sw(h) + 2
			}
			left, hits = build(lv[0], lv[1])
			if sw(left)+2+hw+mw+1 <= w {
				hint, hintW = h, hw
				break fit
			}
		}
	}
	if sw(left)+2+hintW+mw+1 > w { // nothing fits: cut the left side
		hint, hintW = "", 0
		limit := max(0, w-mw-3)
		left = trunc(left, limit)
		var kept []chipHit
		for _, h := range hits {
			if h.x0 < limit {
				h.x1 = min(h.x1, limit)
				kept = append(kept, h)
			}
		}
		hits = kept
	}

	if room := w - sw(left) - 3 - hintW - mw - 1 - 2; room >= 4 {
		if ok && th.Branch != "" { // an isolated thread: its branch says where it works
			left += sep + sAccent.Render(trunc(glyphBranch+" "+th.Branch, room))
		} else {
			left += sep + sFaint.Render(shortPath(tilde(dir, m.home), room))
		}
	}
	line := padTo(left, w-mw-1-hintW)
	if hint != "" {
		hs := sFaint.Render(hint)
		if hintIsWarning(hint) {
			hs = sWarn.Render(hint)
		}
		line += hs + "  "
	}
	hits = append(hits, chipHit{x0: w - mw - 1, x1: w, kind: hitMode})
	return trunc(line+mode, w), hits
}

// chipAt finds the chip-line span under screen column x (relative to the main
// area).
func (m *model) chipAt(x int) (chipHit, bool) {
	_, hits := m.chipLayout(m.g.mainW)
	for _, h := range hits {
		if x >= h.x0 && x < h.x1 {
			return h, true
		}
	}
	return chipHit{}, false
}
