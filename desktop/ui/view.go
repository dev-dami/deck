package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/desktop/backend"
	"github.com/dev-dami/deck/desktop/platform"
	"github.com/dev-dami/deck/desktop/settings"
	"github.com/dev-dami/deck/desktop/state"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// view is one frame's copy of what is shown, taken under the locks so
// layout never races the event goroutines.
type view struct {
	set      settings.Settings
	projects []projView
	status   string
	proj     string
	sess     string
	recents  []string
	opening  []string
	waiting  int

	cur       *projView
	session   *deckv1.Session
	msgs      []*deckv1.Message
	approvals []*deckv1.Approval
	roster    []*deckv1.AgentLine
	queue     []*deckv1.QueuedItem
	changes   []*deckv1.ChangedFile
	models    map[string][]*deckv1.ModelChoice // by kind
}

type projView struct {
	key, name string
	theme     *deckv1.Theme
	sessions  []*deckv1.Session
	pending   map[string]int
	version   string
}

func (a *App) snapshot() view {
	a.mu.Lock()
	v := view{set: a.set, status: a.status, proj: a.proj, sess: a.sess, models: map[string][]*deckv1.ModelChoice{}}
	versions := map[string]string{}
	for d, p := range a.projects {
		versions[d] = p.Info.GetVersion()
	}
	for _, d := range a.set.Recents {
		if a.projects[d] == nil && !a.opening[d] {
			v.recents = append(v.recents, d)
		}
	}
	for d := range a.opening {
		v.opening = append(v.opening, filepath.Base(d))
	}
	a.mu.Unlock()
	slices.Sort(v.opening)
	a.store.View(func(ps []*state.Project) {
		for _, p := range ps {
			pv := projView{key: p.Key, name: p.Name, theme: p.Theme, sessions: slices.Clone(p.Sessions), pending: map[string]int{}, version: versions[p.Key]}
			for sid, as := range p.Approvals {
				pv.pending[sid] = len(as)
				v.waiting += len(as)
			}
			v.projects = append(v.projects, pv)
			if p.Key != v.proj {
				continue
			}
			v.msgs = slices.Clone(p.Messages[v.sess])
			v.approvals = slices.Clone(p.Approvals[v.sess])
			v.roster = slices.Clone(p.Roster[v.sess])
			v.queue = slices.Clone(p.Queue[v.sess])
			v.changes = slices.Clone(p.Changes[v.sess])
			for k, ms := range p.Models {
				if sid, kind, ok := strings.Cut(k, "|"); ok && sid == v.sess {
					v.models[kind] = ms
				}
			}
			for _, s := range p.Sessions {
				if s.GetId() == v.sess {
					v.session = s
				}
			}
		}
	})
	for i := range v.projects {
		if v.projects[i].key == v.proj {
			v.cur = &v.projects[i]
		}
	}
	return v
}

// --- small helpers ---

func sp(v float32) unit.Sp { return unit.Sp(v) }
func unitDp(v int) unit.Dp { return unit.Dp(v) }

func fill(gtx C, c color.NRGBA, radius unit.Dp) {
	r := gtx.Dp(radius)
	defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, r).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

// card draws w on a rounded background.
func card(gtx C, bg color.NRGBA, pad unit.Dp, w layout.Widget) D {
	return layout.Background{}.Layout(gtx,
		func(gtx C) D { fill(gtx, bg, 8); return D{Size: gtx.Constraints.Min} },
		func(gtx C) D { return layout.UniformInset(pad).Layout(gtx, w) },
	)
}

func (a *App) label(size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	l := material.Label(a.th, a.sp(size), txt)
	l.Color = c
	return l
}

func (a *App) bold(size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	l := a.label(size, c, txt)
	l.Font.Weight = font.Bold
	return l
}

func (a *App) line(size unit.Sp, c color.NRGBA, txt string) layout.Widget {
	l := a.label(size, c, txt)
	l.MaxLines = 1
	return l.Layout
}

func (a *App) hline(gtx C) D {
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
	paint.FillShape(gtx.Ops, a.col.Line, clip.Rect{Max: size}.Op())
	return D{Size: size}
}

func (a *App) vline(gtx C) D {
	size := image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)
	paint.FillShape(gtx.Ops, a.col.Line, clip.Rect{Max: size}.Op())
	return D{Size: size}
}

// row is a full-width clickable line, highlighted when on.
func (a *App) row(gtx C, btn *widget.Clickable, on bool, w layout.Widget) D {
	return material.Clickable(gtx, btn, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		bg := color.NRGBA{}
		if on {
			bg = a.col.Card
		} else if btn.Hovered() {
			bg = a.col.Line
		}
		return card(gtx, bg, 6, w)
	})
}

// button is a filled button.
func (a *App) button(gtx C, btn *widget.Clickable, txt string, bg color.NRGBA) D {
	b := material.Button(a.th, btn, txt)
	b.Background = bg
	b.Color = a.col.OnAccent
	if bg == a.col.Card || bg == a.col.Line {
		b.Color = a.col.Fg
	}
	b.TextSize = a.sp(13)
	b.CornerRadius = 6
	b.Inset = layout.Inset{Top: 6, Bottom: 6, Left: 12, Right: 12}
	return b.Layout(gtx)
}

// chip is a small outlined toggle.
func (a *App) chip(gtx C, btn *widget.Clickable, txt string, on bool) D {
	return material.Clickable(gtx, btn, func(gtx C) D {
		bg, fg := a.col.Card, a.col.Fg
		if on {
			bg, fg = a.col.Accent, a.col.OnAccent
		} else if btn.Hovered() {
			bg = a.col.Line
		}
		return card(gtx, bg, 6, a.line(12, fg, txt))
	})
}

// segmented is a row of chips of which one is on.
func (a *App) segmented(gtx C, key string, opts [][2]string, cur string, pick func(string)) D {
	kids := make([]layout.FlexChild, 0, len(opts))
	for i, o := range opts {
		btn := a.click(key + ":" + o[0])
		if btn.Clicked(gtx) && o[0] != cur {
			pick(o[0])
		}
		kids = append(kids, layout.Rigid(func(gtx C) D {
			in := layout.Inset{}
			if i > 0 {
				in.Left = 6
			}
			return in.Layout(gtx, func(gtx C) D { return a.chip(gtx, btn, o[1], o[0] == cur) })
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
}

// link is clickable text.
func (a *App) link(gtx C, btn *widget.Clickable, txt string, c color.NRGBA) D {
	return material.Clickable(gtx, btn, func(gtx C) D {
		if btn.Hovered() {
			c = a.col.Fg
		}
		return layout.UniformInset(4).Layout(gtx, a.line(12, c, txt))
	})
}

func (a *App) editor(gtx C, ed *widget.Editor, hint string) D {
	return card(gtx, a.col.Card, 8, func(gtx C) D {
		e := material.Editor(a.th, ed, hint)
		e.TextSize = a.sp(13)
		e.HintColor = a.col.Muted
		return e.Layout(gtx)
	})
}

// --- frame ---

func (a *App) shortcuts(gtx C) {
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, a)
	area.Pop()
	for {
		ev, ok := gtx.Event(
			key.Filter{Name: "N", Required: key.ModShortcut},
			key.Filter{Name: "O", Required: key.ModShortcut},
			key.Filter{Name: ",", Required: key.ModShortcut},
			key.Filter{Name: "B", Required: key.ModShortcut},
		)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case "N":
			proj, _ := a.selected()
			a.newThread(proj)
		case "O":
			a.pickFolder()
		case ",":
			a.page = pageSettings
		case "B":
			a.saveSettings(func(s *settings.Settings) { s.PanelOpen = !s.PanelOpen })
		}
	}
}

func (a *App) layout(gtx C) D {
	paint.Fill(gtx.Ops, a.col.Bg)
	a.shortcuts(gtx)
	a.mu.Lock()
	a.attach = append(a.attach, a.pendingAttach...)
	a.pendingAttach = nil
	a.mu.Unlock()

	v := a.snapshot()
	a.windowTitle(v.waiting)
	showPanel := a.page == pageChat && v.set.PanelOpen && v.session != nil
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return a.layoutRail(gtx, v) }),
		layout.Rigid(a.vline),
		layout.Flexed(1, func(gtx C) D {
			if a.page == pageSettings {
				return a.layoutSettings(gtx, v)
			}
			return a.layoutMain(gtx, v)
		}),
		layout.Rigid(func(gtx C) D {
			if !showPanel {
				return D{}
			}
			return layout.Flex{}.Layout(gtx, layout.Rigid(a.vline), layout.Rigid(func(gtx C) D { return a.layoutPanel(gtx, v) }))
		}),
	)
}

// --- rail ---

func (a *App) layoutRail(gtx C, v view) D {
	width := gtx.Dp(280)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
	paint.FillShape(gtx.Ops, a.col.Rail, clip.Rect{Max: image.Pt(width, gtx.Constraints.Max.Y)}.Op())

	for {
		ev, ok := a.pathEd.Update(gtx)
		if !ok {
			break
		}
		if s, ok := ev.(widget.SubmitEvent); ok && strings.TrimSpace(s.Text) != "" {
			a.open(s.Text)
			a.pathEd.SetText("")
		}
	}
	openBtn := a.click("open")
	if openBtn.Clicked(gtx) {
		a.pickFolder()
	}
	setBtn := a.click("settings")
	if setBtn.Clicked(gtx) {
		if a.page == pageSettings {
			a.page = pageChat
		} else {
			a.page = pageSettings
		}
	}
	dismiss := a.click("dismiss")
	if dismiss.Clicked(gtx) {
		a.setStatus("")
	}
	search := strings.ToLower(strings.TrimSpace(a.searchEd.Text()))

	var items []layout.Widget
	items = append(items, func(gtx C) D {
		return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Flexed(1, a.bold(22, a.col.Accent, "Deck").Layout),
				layout.Rigid(func(gtx C) D { return a.link(gtx, setBtn, "Settings", a.col.Muted) }),
			)
		})
	})
	items = append(items, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D { return a.button(gtx, openBtn, "Open folder…   Ctrl+O", a.col.Accent) })
	})
	items = append(items, func(gtx C) D {
		return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx C) D { return a.editor(gtx, &a.pathEd, "…or paste a folder path") })
	})
	if len(v.opening) > 0 {
		items = append(items, func(gtx C) D {
			return layout.Inset{Bottom: 10}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Max = image.Pt(gtx.Dp(14), gtx.Dp(14))
						return material.Loader(a.th).Layout(gtx)
					}),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Left: 8}.Layout(gtx, a.line(12, a.col.Muted, "Starting "+strings.Join(v.opening, ", ")+"…"))
					}),
				)
			})
		})
	}
	if v.status != "" {
		statusCol := a.col.Danger
		if strings.HasPrefix(v.status, "✓") {
			statusCol = a.col.Accent
		}
		items = append(items, func(gtx C) D {
			return layout.Inset{Bottom: 10}.Layout(gtx, func(gtx C) D {
				return card(gtx, a.col.Card, 8, func(gtx C) D {
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(1, a.label(12, statusCol, v.status).Layout),
						layout.Rigid(func(gtx C) D { return a.link(gtx, dismiss, "✕", a.col.Muted) }),
					)
				})
			})
		})
	}
	if len(v.projects) > 0 {
		items = append(items, func(gtx C) D {
			return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx C) D { return a.editor(gtx, &a.searchEd, "Search threads") })
		})
	}

	for _, p := range v.projects {
		items = append(items, a.projectItems(gtx, v, p, search)...)
	}

	if len(v.recents) > 0 {
		items = append(items, func(gtx C) D {
			return layout.Inset{Top: 18, Bottom: 4}.Layout(gtx, a.line(11, a.col.Muted, "RECENT"))
		})
		for _, d := range v.recents {
			btn := a.click("recent:" + d)
			if btn.Clicked(gtx) {
				a.open(d)
			}
			items = append(items, func(gtx C) D {
				return a.row(gtx, btn, false, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(a.line(13, a.col.Fg, filepath.Base(d))),
						layout.Rigid(a.line(11, a.col.Muted, d)),
					)
				})
			})
		}
	}

	return layout.UniformInset(14).Layout(gtx, func(gtx C) D {
		return material.List(a.th, &a.rail).Layout(gtx, len(items), func(gtx C, i int) D { return items[i](gtx) })
	})
}

func (a *App) projectItems(gtx C, v view, p projView, search string) []layout.Widget {
	newBtn := a.click("new:" + p.key)
	if newBtn.Clicked(gtx) {
		a.newThread(p.key)
	}
	menuBtn := a.click("menu:" + p.key)
	if menuBtn.Clicked(gtx) {
		a.menuOpen[p.key] = !a.menuOpen[p.key]
	}
	actions := []struct {
		key, label string
		run        func()
	}{
		{"explorer", "Show in folder", func() { a.fail(platform.Reveal(p.key)) }},
		{"editor", "Open in VS Code", func() { a.fail(platform.OpenEditor(p.key)) }},
		{"terminal", "Terminal", func() { a.fail(platform.OpenTerminal(p.key)) }},
		{"close", "Close project", func() { a.closeProject(p.key) }},
	}
	for _, act := range actions {
		if a.click(act.key + ":" + p.key).Clicked(gtx) {
			a.menuOpen[p.key] = false
			act.run()
		}
	}

	var items []layout.Widget
	items = append(items, func(gtx C) D {
		return layout.Inset{Top: 12, Bottom: 4}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					l := a.bold(13, a.col.Fg, p.name)
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx C) D { return a.link(gtx, newBtn, "+ New", a.col.Accent) }),
				layout.Rigid(func(gtx C) D { return a.link(gtx, menuBtn, "•••", a.col.Muted) }),
			)
		})
	})
	if a.menuOpen[p.key] {
		items = append(items, func(gtx C) D {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D {
				return card(gtx, a.col.Card, 6, func(gtx C) D {
					kids := make([]layout.FlexChild, 0, len(actions))
					for _, act := range actions {
						btn := a.click(act.key + ":" + p.key)
						c := a.col.Fg
						if act.key == "close" {
							c = a.col.Danger
						}
						kids = append(kids, layout.Rigid(func(gtx C) D { return a.row(gtx, btn, false, a.line(13, c, act.label)) }))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
				})
			})
		})
	}
	shown := 0
	for _, s := range p.sessions {
		t := s.GetTitle()
		if t == "" {
			t = "New thread"
		}
		if search != "" && !strings.Contains(strings.ToLower(t+" "+s.GetLastText()), search) {
			continue
		}
		shown++
		btn := a.click("sess:" + p.key + "|" + s.GetId())
		if btn.Clicked(gtx) {
			a.selectSession(p.key, s.GetId())
		}
		on := p.key == v.proj && s.GetId() == v.sess && a.page == pageChat
		pending := p.pending[s.GetId()]
		items = append(items, func(gtx C) D {
			return a.row(gtx, btn, on, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(a.line(13, a.col.Fg, t)),
							layout.Rigid(func(gtx C) D {
								if s.GetLastText() == "" {
									return D{}
								}
								return a.line(11, a.col.Muted, ago(s.GetUpdatedAt())+"  "+oneLine(s.GetLastText()))(gtx)
							}),
						)
					}),
					layout.Rigid(func(gtx C) D {
						switch {
						case pending > 0:
							return layout.Inset{Left: 6}.Layout(gtx, func(gtx C) D {
								return card(gtx, a.col.Busy, 3, a.line(11, a.col.OnAccent, fmt.Sprintf("%d", pending)))
							})
						case s.GetBusy():
							return layout.Inset{Left: 6}.Layout(gtx, func(gtx C) D {
								gtx.Constraints.Max = image.Pt(gtx.Dp(12), gtx.Dp(12))
								return material.Loader(a.th).Layout(gtx)
							})
						}
						return D{}
					}),
				)
			})
		})
	}
	if shown == 0 {
		msg := "No threads yet. Ctrl+N starts one."
		if search != "" {
			msg = "No thread matches."
		}
		items = append(items, func(gtx C) D { return layout.Inset{Left: 6}.Layout(gtx, a.line(12, a.col.Muted, msg)) })
	}
	return items
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

func ago(ms int64) string {
	if ms == 0 {
		return ""
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// --- chat page ---

func (a *App) centered(gtx C, title, sub string, btn *widget.Clickable, btnText string) D {
	return layout.Center.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(a.bold(20, a.col.Fg, title).Layout),
			layout.Rigid(func(gtx C) D { return layout.Inset{Top: 6, Bottom: 16}.Layout(gtx, a.label(14, a.col.Muted, sub).Layout) }),
			layout.Rigid(func(gtx C) D {
				if btn == nil {
					return D{}
				}
				return a.button(gtx, btn, btnText, a.col.Accent)
			}),
		)
	})
}

func (a *App) layoutMain(gtx C, v view) D {
	switch {
	case len(v.projects) == 0 && len(v.opening) == 0:
		btn := a.click("open-center")
		if btn.Clicked(gtx) {
			a.pickFolder()
		}
		return a.centered(gtx, "Welcome to Deck", "Open a project folder and your agents work on it together.", btn, "Open folder…")
	case len(v.projects) == 0:
		return a.centered(gtx, "Starting Deck…", "The engine is loading your project.", nil, "")
	case v.session == nil:
		btn := a.click("new-center")
		if btn.Clicked(gtx) {
			a.newThread(v.proj)
		}
		return a.centered(gtx, "No thread open", "Pick one on the left, or start a new one.", btn, "New thread   Ctrl+N")
	}

	for {
		ev, ok := a.composer.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			a.send(v.proj, v.sess)
		}
	}
	if a.click("send").Clicked(gtx) {
		a.send(v.proj, v.sess)
	}
	if a.click("attach").Clicked(gtx) {
		a.pickImages()
	}
	if a.click("stop").Clicked(gtx) {
		sid := v.sess
		a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
			_, err := c.Cancel(ctx, &deckv1.SessionRef{SessionId: sid})
			return err
		})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return a.layoutHeader(gtx, v) }),
		layout.Rigid(a.hline),
		layout.Flexed(1, func(gtx C) D { return a.layoutChat(gtx, v) }),
		layout.Rigid(func(gtx C) D { return a.layoutApprovals(gtx, v) }),
		layout.Rigid(func(gtx C) D { return a.layoutComposer(gtx, v) }),
	)
}

func (a *App) layoutHeader(gtx C, v view) D {
	sid := v.sess
	renameBtn := a.click("rename")
	if renameBtn.Clicked(gtx) {
		a.renaming = !a.renaming
		a.renameEd.SetText(v.session.GetTitle())
		if a.renaming {
			gtx.Execute(key.FocusCmd{Tag: &a.renameEd})
		}
	}
	for {
		ev, ok := a.renameEd.Update(gtx)
		if !ok {
			break
		}
		if s, ok := ev.(widget.SubmitEvent); ok {
			a.renaming = false
			if t := strings.TrimSpace(s.Text); t != "" {
				a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
					_, err := c.RenameSession(ctx, &deckv1.RenameSessionRequest{SessionId: sid, Title: t})
					return err
				})
			}
		}
	}
	delBtn := a.click("delete")
	confirming := time.Since(a.deleteAt) < 4*time.Second
	if delBtn.Clicked(gtx) {
		if confirming {
			a.deleteAt = time.Time{}
			proj := v.proj
			a.do(proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.DeleteSession(ctx, &deckv1.SessionRef{SessionId: sid})
				return err
			}, func(p *backend.Project) {
				a.mu.Lock()
				if a.sess == sid {
					a.sess = ""
				}
				a.mu.Unlock()
				a.refetch(p)
			})
		} else {
			a.deleteAt = time.Now()
			confirming = true
			go func() { time.Sleep(4 * time.Second); a.w.Invalidate() }()
		}
	}
	panelBtn := a.click("panel")
	if panelBtn.Clicked(gtx) {
		a.saveSettings(func(s *settings.Settings) { s.PanelOpen = !s.PanelOpen })
	}

	return layout.Inset{Top: 12, Bottom: 12, Left: 20, Right: 16}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				if a.renaming {
					return a.editor(gtx, &a.renameEd, "Thread name, enter to save")
				}
				t := v.session.GetTitle()
				if t == "" {
					t = "New thread"
				}
				return material.Clickable(gtx, renameBtn, func(gtx C) D {
					l := a.bold(17, a.col.Fg, t)
					l.MaxLines = 1
					return l.Layout(gtx)
				})
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: 12}.Layout(gtx, func(gtx C) D {
					return a.segmented(gtx, "mode", [][2]string{{"ask", "Ask"}, {"plan", "Plan"}, {"yolo", "Yolo"}}, v.session.GetMode(), func(m string) {
						a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
							_, err := c.SetMode(ctx, &deckv1.SetModeRequest{SessionId: sid, Mode: m})
							return err
						})
					})
				})
			}),
			layout.Rigid(func(gtx C) D {
				label := "Delete"
				if confirming {
					label = "Delete?"
				}
				return layout.Inset{Left: 12}.Layout(gtx, func(gtx C) D { return a.link(gtx, delBtn, label, a.col.Danger) })
			}),
			layout.Rigid(func(gtx C) D {
				label := "Show panel"
				if v.set.PanelOpen {
					label = "Hide panel"
				}
				return layout.Inset{Left: 6}.Layout(gtx, func(gtx C) D { return a.link(gtx, panelBtn, label, a.col.Muted) })
			}),
		)
	})
}

// agentTint maps a message's agent (a roster id or name) to its colour.
func (a *App) agentTint(v view, agent string) (string, color.NRGBA) {
	for _, ag := range v.roster {
		if ag.GetId() == agent || ag.GetName() == agent {
			return ag.GetName(), a.col.tint(v.cur.theme, ag.GetKind())
		}
	}
	if agent == "" {
		return "Deck", a.col.Muted
	}
	return agent, a.col.tint(v.cur.theme, agent)
}

func (a *App) layoutChat(gtx C, v view) D {
	if len(v.msgs) == 0 {
		names := make([]string, 0, len(v.roster))
		for _, ag := range v.roster {
			names = append(names, "@"+ag.GetName())
		}
		sub := "Everyone in the thread sees each message."
		if len(names) > 0 {
			sub += " Address one with " + strings.Join(names, ", ") + "."
		}
		return a.centered(gtx, "Start the thread", sub, nil, "")
	}
	return layout.Inset{Left: 20, Right: 20, Top: 12}.Layout(gtx, func(gtx C) D {
		return material.List(a.th, &a.chat).Layout(gtx, len(v.msgs), func(gtx C, i int) D {
			return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx C) D { return a.layoutMessage(gtx, v, v.msgs[i]) })
		})
	})
}

func clipLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… %d more lines", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

func (a *App) layoutMessage(gtx C, v view, m *deckv1.Message) D {
	who, col := "Deck", a.col.Muted
	switch m.GetRole() {
	case "user":
		who, col = "You", a.col.Accent
	case "assistant":
		who, col = a.agentTint(v, m.GetAgent())
	}
	var kids []layout.FlexChild
	kids = append(kids, layout.Rigid(func(gtx C) D {
		return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(a.bold(12, col, who).Layout),
				layout.Rigid(func(gtx C) D {
					t := time.UnixMilli(m.GetCreatedAt()).Format("15:04")
					if !m.GetFinished() && m.GetRole() == "assistant" {
						t += "  writing…"
					}
					return layout.Inset{Left: 8}.Layout(gtx, a.line(11, a.col.Muted, t))
				}),
			)
		})
	}))
	for i, p := range m.GetParts() {
		key := fmt.Sprintf("%s/%d", m.GetId(), i)
		var w layout.Widget
		switch p.GetType() {
		case "text":
			w = func(gtx C) D { return a.layoutMarkdown(gtx, key, p.GetText()) }
		case "reasoning":
			w = func(gtx C) D {
				l := a.label(12, a.col.Muted, clipLines(p.GetText(), 8))
				l.State = a.sel(key, l.Text)
				return l.Layout(gtx)
			}
		case "tool_call":
			w = a.line(12, a.col.Muted, "▸ "+strings.TrimSpace(p.GetName()+"  "+p.GetSummary()))
		case "tool_result":
			if p.GetIsError() {
				w = a.label(12, a.col.Danger, clipLines(p.GetOutput(), 6)).Layout
			}
		case "image":
			w = func(gtx C) D { return card(gtx, a.col.Card, 6, a.line(12, a.col.Muted, "🖼 "+p.GetImage().GetName())) }
		}
		if w != nil {
			kids = append(kids, layout.Rigid(func(gtx C) D { return layout.Inset{Bottom: 2}.Layout(gtx, w) }))
		}
	}
	body := func(gtx C) D { return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...) }
	if m.GetRole() == "user" {
		return card(gtx, a.col.Card, 12, body)
	}
	return layout.Inset{Left: 12, Right: 12}.Layout(gtx, body)
}

func (a *App) layoutApprovals(gtx C, v view) D {
	if len(v.approvals) == 0 {
		return D{}
	}
	var kids []layout.FlexChild
	for _, ap := range v.approvals {
		kids = append(kids, layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: 8}.Layout(gtx, func(gtx C) D { return a.layoutApproval(gtx, v, ap) })
		}))
	}
	return layout.Inset{Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
	})
}

func (a *App) layoutApproval(gtx C, v view, ap *deckv1.Approval) D {
	decisions := []struct{ key, label string }{{"allow", "Allow"}, {"allow_session", "Allow for this thread"}, {"deny", "Deny"}}
	for _, d := range decisions {
		if a.click("ans:" + ap.GetId() + ":" + d.key).Clicked(gtx) {
			sid, id, dec := ap.GetSessionId(), ap.GetId(), d.key
			a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.Answer(ctx, &deckv1.AnswerRequest{SessionId: sid, Id: id, Decision: dec})
				return err
			})
		}
	}
	who, col := a.agentTint(v, ap.GetAgent())
	detail := ap.GetCommand()
	if detail == "" {
		detail = ap.GetPath()
	}
	if ap.GetDiff() != "" {
		detail = strings.TrimSpace(detail + "\n" + ap.GetDiff())
	}
	return layout.Background{}.Layout(gtx,
		func(gtx C) D { fill(gtx, a.col.Busy, 10); return D{Size: gtx.Constraints.Min} },
		func(gtx C) D {
			return layout.Inset{Left: 3}.Layout(gtx, func(gtx C) D {
				return card(gtx, a.col.Card, 12, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(a.bold(12, col, who+" needs your OK ("+ap.GetKind()+")").Layout),
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Top: 4, Bottom: 8}.Layout(gtx, a.label(13, a.col.Fg, ap.GetDescription()).Layout)
						}),
						layout.Rigid(func(gtx C) D {
							if detail == "" {
								return D{}
							}
							return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx C) D { return a.layoutDiff(gtx, clipLines(detail, 14)) })
						}),
						layout.Rigid(func(gtx C) D {
							var bs []layout.FlexChild
							for i, d := range decisions {
								bg := a.col.Accent
								switch d.key {
								case "deny":
									bg = a.col.Danger
								case "allow_session":
									bg = a.col.Line
								}
								btn := a.click("ans:" + ap.GetId() + ":" + d.key)
								bs = append(bs, layout.Rigid(func(gtx C) D {
									in := layout.Inset{}
									if i > 0 {
										in.Left = 8
									}
									return in.Layout(gtx, func(gtx C) D { return a.button(gtx, btn, d.label, bg) })
								}))
							}
							return layout.Flex{}.Layout(gtx, bs...)
						}),
					)
				})
			})
		})
}

func (a *App) layoutComposer(gtx C, v view) D {
	for i := range a.attach {
		if a.click(fmt.Sprintf("unattach:%d", i)).Clicked(gtx) {
			a.attach = slices.Delete(a.attach, i, i+1)
			break
		}
	}
	return layout.Inset{Top: 10, Bottom: 16, Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		return card(gtx, a.col.Card, 12, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					if len(a.attach) == 0 {
						return D{}
					}
					var kids []layout.FlexChild
					for i, at := range a.attach {
						btn := a.click(fmt.Sprintf("unattach:%d", i))
						kids = append(kids, layout.Rigid(func(gtx C) D {
							return layout.Inset{Right: 6}.Layout(gtx, func(gtx C) D { return a.chip(gtx, btn, "🖼 "+at.name+"  ✕", false) })
						}))
					}
					return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx C) D { return layout.Flex{}.Layout(gtx, kids...) })
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.End}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Right: 8}.Layout(gtx, func(gtx C) D { return a.button(gtx, a.click("attach"), "＋ Image", a.col.Line) })
						}),
						layout.Flexed(1, func(gtx C) D {
							hint := "Message the agents…  Enter sends, Shift+Enter for a new line"
							if n := len(v.queue); n > 0 {
								hint = fmt.Sprintf("%d waiting in the queue. %s", n, hint)
							}
							e := material.Editor(a.th, &a.composer, hint)
							e.HintColor = a.col.Muted
							e.TextSize = a.sp(14)
							gtx.Constraints.Max.Y = gtx.Dp(220)
							return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, e.Layout)
						}),
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Left: 8}.Layout(gtx, func(gtx C) D {
								if v.session.GetBusy() {
									return layout.Flex{}.Layout(gtx,
										layout.Rigid(func(gtx C) D { return a.button(gtx, a.click("stop"), "Stop", a.col.Danger) }),
										layout.Rigid(func(gtx C) D {
											return layout.Inset{Left: 8}.Layout(gtx, func(gtx C) D { return a.button(gtx, a.click("send"), "Queue", a.col.Accent) })
										}),
									)
								}
								return a.button(gtx, a.click("send"), "Send", a.col.Accent)
							})
						}),
					)
				}),
			)
		})
	})
}
