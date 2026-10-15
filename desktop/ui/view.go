package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"slices"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/desktop/state"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// view is one frame's copy of what is shown, taken under the locks so
// layout never races the event goroutines.
type view struct {
	projects []projView
	status   string
	proj     string
	sess     string
	recents  []string
	opening  int

	cur       *projView
	session   *deckv1.Session
	msgs      []*deckv1.Message
	approvals []*deckv1.Approval
	roster    []*deckv1.AgentLine
	queue     []*deckv1.QueuedItem
}

type projView struct {
	key, name string
	theme     *deckv1.Theme
	sessions  []*deckv1.Session
	pending   map[string]int
}

func (a *App) snapshot() view {
	a.mu.Lock()
	v := view{status: a.status, proj: a.proj, sess: a.sess, opening: len(a.opening)}
	for _, d := range a.recents {
		if a.projects[d] == nil {
			v.recents = append(v.recents, d)
		}
	}
	a.mu.Unlock()
	a.store.View(func(ps []*state.Project) {
		for _, p := range ps {
			pv := projView{key: p.Key, name: p.Name, theme: p.Theme, sessions: slices.Clone(p.Sessions), pending: map[string]int{}}
			for sid, as := range p.Approvals {
				pv.pending[sid] = len(as)
			}
			v.projects = append(v.projects, pv)
			if p.Key != v.proj {
				continue
			}
			v.msgs = slices.Clone(p.Messages[v.sess])
			v.approvals = slices.Clone(p.Approvals[v.sess])
			v.roster = slices.Clone(p.Roster[v.sess])
			v.queue = slices.Clone(p.Queue[v.sess])
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

func (a *App) layout(gtx C) D {
	paint.Fill(gtx.Ops, a.col.Bg)
	v := a.snapshot()
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return a.layoutRail(gtx, v) }),
		layout.Rigid(func(gtx C) D { return a.vline(gtx) }),
		layout.Flexed(1, func(gtx C) D { return a.layoutMain(gtx, v) }),
	)
}

func (a *App) vline(gtx C) D {
	size := image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)
	paint.FillShape(gtx.Ops, a.col.Line, clip.Rect{Max: size}.Op())
	return D{Size: size}
}

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
	l := material.Label(a.th, size, txt)
	l.Color = c
	return l
}

func (a *App) bold(size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	l := a.label(size, c, txt)
	l.Font.Weight = font.Bold
	return l
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

func (a *App) button(gtx C, btn *widget.Clickable, txt string, bg color.NRGBA) D {
	b := material.Button(a.th, btn, txt)
	b.Background = bg
	b.Color = a.col.Bg
	b.TextSize = 13
	b.Inset = layout.Inset{Top: 6, Bottom: 6, Left: 12, Right: 12}
	return b.Layout(gtx)
}

func (a *App) layoutRail(gtx C, v view) D {
	width := gtx.Dp(270)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
	paint.FillShape(gtx.Ops, a.col.Rail, clip.Rect{Max: image.Pt(width, gtx.Constraints.Max.Y)}.Op())

	for {
		ev, ok := a.pathEd.Update(gtx)
		if !ok {
			break
		}
		if s, ok := ev.(widget.SubmitEvent); ok && strings.TrimSpace(s.Text) != "" {
			a.open(strings.Trim(strings.TrimSpace(s.Text), `"`))
			a.pathEd.SetText("")
		}
	}

	var items []layout.Widget
	items = append(items, func(gtx C) D {
		return layout.Inset{Bottom: 10}.Layout(gtx, a.bold(20, a.col.Accent, "Deck").Layout)
	})
	items = append(items, func(gtx C) D {
		return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx C) D {
			return card(gtx, a.col.Card, 8, func(gtx C) D {
				e := material.Editor(a.th, &a.pathEd, "Open a project folder…")
				e.TextSize = 13
				e.HintColor = a.col.Muted
				return e.Layout(gtx)
			})
		})
	})
	if v.status != "" {
		col := a.col.Danger
		if v.opening > 0 {
			col = a.col.Muted
		}
		items = append(items, func(gtx C) D {
			return layout.Inset{Bottom: 10}.Layout(gtx, a.label(12, col, v.status).Layout)
		})
	}

	for _, p := range v.projects {
		newBtn := a.click("new:" + p.key)
		if newBtn.Clicked(gtx) {
			a.newThread(p.key)
		}
		items = append(items, func(gtx C) D {
			return layout.Inset{Top: 8, Bottom: 4}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, a.bold(13, a.col.Fg, p.name).Layout),
					layout.Rigid(func(gtx C) D {
						return material.Clickable(gtx, newBtn, func(gtx C) D {
							return layout.UniformInset(4).Layout(gtx, a.label(13, a.col.Accent, "+ New").Layout)
						})
					}),
				)
			})
		})
		if len(p.sessions) == 0 {
			items = append(items, a.label(12, a.col.Muted, "No threads yet").Layout)
		}
		for _, s := range p.sessions {
			btn := a.click("sess:" + p.key + "|" + s.GetId())
			if btn.Clicked(gtx) {
				a.selectSession(p.key, s.GetId())
			}
			on := p.key == v.proj && s.GetId() == v.sess
			pending := p.pending[s.GetId()]
			items = append(items, func(gtx C) D {
				return a.row(gtx, btn, on, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D {
							t := s.GetTitle()
							if t == "" {
								t = "New thread"
							}
							l := a.label(13, a.col.Fg, t)
							l.MaxLines = 1
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx C) D {
							switch {
							case pending > 0:
								return a.label(12, a.col.Busy, fmt.Sprintf(" %d!", pending)).Layout(gtx)
							case s.GetBusy():
								return a.label(12, a.col.Busy, " ●").Layout(gtx)
							}
							return D{}
						}),
					)
				})
			})
		}
	}

	if len(v.recents) > 0 {
		items = append(items, func(gtx C) D {
			return layout.Inset{Top: 16, Bottom: 4}.Layout(gtx, a.label(12, a.col.Muted, "RECENT").Layout)
		})
		for _, d := range v.recents {
			btn := a.click("recent:" + d)
			if btn.Clicked(gtx) {
				a.open(d)
			}
			items = append(items, func(gtx C) D {
				return a.row(gtx, btn, false, func(gtx C) D {
					l := a.label(13, a.col.Muted, filepath.Base(d))
					l.MaxLines = 1
					return l.Layout(gtx)
				})
			})
		}
	}

	return layout.UniformInset(14).Layout(gtx, func(gtx C) D {
		return material.List(a.th, &a.rail).Layout(gtx, len(items), func(gtx C, i int) D { return items[i](gtx) })
	})
}

func (a *App) centered(gtx C, txt string) D {
	return layout.Center.Layout(gtx, a.label(15, a.col.Muted, txt).Layout)
}

func (a *App) layoutMain(gtx C, v view) D {
	switch {
	case len(v.projects) == 0 && v.opening == 0:
		return a.centered(gtx, "Open a project folder to start (paste its path on the left).")
	case len(v.projects) == 0:
		return a.centered(gtx, "Starting Deck…")
	case v.session == nil:
		return a.centered(gtx, "Pick a thread, or press + New.")
	}

	for {
		ev, ok := a.composer.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			a.send(v)
		}
	}
	if a.sendBtn.Clicked(gtx) {
		a.send(v)
	}
	if a.stopBtn.Clicked(gtx) {
		sid := v.sess
		a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
			_, err := c.Cancel(ctx, &deckv1.SessionRef{SessionId: sid})
			return err
		})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return a.layoutHeader(gtx, v) }),
		layout.Flexed(1, func(gtx C) D { return a.layoutChat(gtx, v) }),
		layout.Rigid(func(gtx C) D { return a.layoutApprovals(gtx, v) }),
		layout.Rigid(func(gtx C) D { return a.layoutComposer(gtx, v) }),
	)
}

func (a *App) send(v view) {
	text := strings.TrimSpace(a.composer.Text())
	if text == "" {
		return
	}
	a.composer.SetText("")
	sid := v.sess
	a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
		_, err := c.Send(ctx, &deckv1.SendRequest{SessionId: sid, Text: text})
		return err
	})
}

func (a *App) layoutHeader(gtx C, v view) D {
	return layout.Inset{Top: 14, Bottom: 10, Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		kids := []layout.FlexChild{
			layout.Flexed(1, func(gtx C) D {
				t := v.session.GetTitle()
				if t == "" {
					t = "New thread"
				}
				l := a.bold(16, a.col.Fg, t)
				l.MaxLines = 1
				return l.Layout(gtx)
			}),
		}
		for _, ag := range v.roster {
			col := a.col.tint(v.cur.theme, ag.GetKind())
			name := ag.GetName()
			if ag.GetBusy() {
				name += " ●"
			}
			kids = append(kids, layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: 6}.Layout(gtx, func(gtx C) D {
					return card(gtx, a.col.Card, 4, a.label(12, col, name).Layout)
				})
			}))
		}
		kids = append(kids, layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: 10}.Layout(gtx, a.label(12, a.col.Muted, v.session.GetMode()).Layout)
		}))
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
	})
}

// agentTint maps a message's agent (a roster id or name) to its colour.
func (a *App) agentTint(v view, agent string) (string, color.NRGBA) {
	for _, ag := range v.roster {
		if ag.GetId() == agent || ag.GetName() == agent {
			return ag.GetName(), a.col.tint(v.cur.theme, ag.GetKind())
		}
	}
	return agent, a.col.tint(v.cur.theme, agent)
}

func (a *App) layoutChat(gtx C, v view) D {
	return layout.Inset{Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		return material.List(a.th, &a.chat).Layout(gtx, len(v.msgs), func(gtx C, i int) D {
			return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx C) D { return a.layoutMessage(gtx, v, v.msgs[i]) })
		})
	})
}

func clip6(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], fmt.Sprintf("… %d more lines", len(lines)-6))
	}
	return strings.Join(lines, "\n")
}

func (a *App) layoutMessage(gtx C, v view, m *deckv1.Message) D {
	who, col := "Deck", a.col.Muted
	switch m.GetRole() {
	case "user":
		who, col = "You", a.col.Fg
	case "assistant":
		who, col = a.agentTint(v, m.GetAgent())
	}
	var kids []layout.FlexChild
	kids = append(kids, layout.Rigid(func(gtx C) D {
		return layout.Inset{Bottom: 4}.Layout(gtx, a.bold(12, col, who).Layout)
	}))
	for _, p := range m.GetParts() {
		var w layout.Widget
		switch p.GetType() {
		case "text":
			w = a.label(14, a.col.Fg, p.GetText()).Layout
		case "reasoning":
			w = a.label(13, a.col.Muted, p.GetText()).Layout
		case "tool_call":
			w = a.label(12, a.col.Muted, "▸ "+strings.TrimSpace(p.GetName()+"  "+p.GetSummary())).Layout
		case "tool_result":
			if p.GetIsError() {
				w = a.label(12, a.col.Danger, clip6(p.GetOutput())).Layout
			}
		case "image":
			w = a.label(12, a.col.Muted, "[image "+p.GetImage().GetName()+"]").Layout
		}
		if w != nil {
			kids = append(kids, layout.Rigid(func(gtx C) D { return layout.Inset{Bottom: 2}.Layout(gtx, w) }))
		}
	}
	body := func(gtx C) D { return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...) }
	if m.GetRole() == "user" {
		return card(gtx, a.col.Card, 10, body)
	}
	return layout.Inset{Left: 10, Right: 10}.Layout(gtx, body)
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
	decisions := []struct{ key, label string }{{"allow", "Allow"}, {"allow_session", "Allow for thread"}, {"deny", "Deny"}}
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
	return card(gtx, a.col.Card, 12, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(a.bold(12, col, who+" wants to "+ap.GetKind()).Layout),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 4, Bottom: 8}.Layout(gtx, a.label(13, a.col.Fg, ap.GetDescription()).Layout)
			}),
			layout.Rigid(func(gtx C) D {
				if detail == "" {
					return D{}
				}
				l := a.label(12, a.col.Muted, clip6(detail))
				l.Font.Typeface = "monospace"
				return layout.Inset{Bottom: 8}.Layout(gtx, l.Layout)
			}),
			layout.Rigid(func(gtx C) D {
				var bs []layout.FlexChild
				for i, d := range decisions {
					bg := a.col.Accent
					if d.key == "deny" {
						bg = a.col.Danger
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
}

func (a *App) layoutComposer(gtx C, v view) D {
	return layout.Inset{Top: 10, Bottom: 16, Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		return card(gtx, a.col.Card, 12, func(gtx C) D {
			return layout.Flex{Alignment: layout.End}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					hint := "Message the agents… (@agent to address one)"
					if n := len(v.queue); n > 0 {
						hint = fmt.Sprintf("%d queued. %s", n, hint)
					}
					e := material.Editor(a.th, &a.composer, hint)
					e.HintColor = a.col.Muted
					gtx.Constraints.Max.Y = gtx.Dp(200)
					return e.Layout(gtx)
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Left: 10}.Layout(gtx, func(gtx C) D {
						if v.session.GetBusy() {
							return layout.Flex{}.Layout(gtx,
								layout.Rigid(func(gtx C) D { return a.button(gtx, &a.stopBtn, "Stop", a.col.Danger) }),
								layout.Rigid(func(gtx C) D {
									return layout.Inset{Left: 8}.Layout(gtx, func(gtx C) D { return a.button(gtx, &a.sendBtn, "Send", a.col.Accent) })
								}),
							)
						}
						return a.button(gtx, &a.sendBtn, "Send", a.col.Accent)
					})
				}),
			)
		})
	})
}
