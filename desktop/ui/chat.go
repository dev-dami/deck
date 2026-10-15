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

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/desktop/settings"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// chatWidth is the reading width of the chat and composer.
const chatWidth = unit.Dp(780)

func (a *App) layoutMain(gtx C, v view) D {
	switch {
	case len(v.projects) == 0:
		return a.layoutWelcome(gtx, v)
	case v.session == nil:
		if a.click("empty-new").Clicked(gtx) {
			a.newThread(v.proj)
		}
		return layout.Center.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return icon(gtx, icChat, 40, a.col.Muted) }),
				gap(12),
				layout.Rigid(a.bold(18, a.col.Fg, "No thread open").Layout),
				gap(4),
				layout.Rigid(a.label(13, a.col.Muted, "Pick one on the left, or start a new one.").Layout),
				gap(16),
				layout.Rigid(func(gtx C) D { return a.button(gtx, "empty-new", "New thread", icAdd, true) }),
			)
		})
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
		layout.Rigid(func(gtx C) D { return a.layoutConfirm(gtx, v) }),
		layout.Flexed(1, func(gtx C) D { return a.layoutChat(gtx, v) }),
		layout.Rigid(func(gtx C) D {
			return column(gtx, chatWidth, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return a.layoutApprovals(gtx, v) }),
					layout.Rigid(func(gtx C) D { return a.layoutComposer(gtx, v) }),
				)
			})
		}),
	)
}

func (a *App) layoutWelcome(gtx C, v view) D {
	if a.click("welcome-open").Clicked(gtx) {
		a.pickFolder()
	}
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
	for _, d := range v.recents {
		if a.click("welcome-recent:" + d).Clicked(gtx) {
			a.open(d)
		}
	}
	return layout.Center.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		kids := []layout.FlexChild{
			layout.Rigid(func(gtx C) D { return a.avatar(gtx, a.col.Accent, "D", 52) }),
			gap(16),
			layout.Rigid(a.bold(24, a.col.Fg, "Welcome to Deck").Layout),
			gap(6),
			layout.Rigid(func(gtx C) D {
				l := a.label(14, a.col.Muted, "Open a project and your agents work on it together, in one thread.")
				l.Alignment = text.Middle
				return l.Layout(gtx)
			}),
			gap(20),
			layout.Rigid(func(gtx C) D { return a.button(gtx, "welcome-open", "Open folder…", icFolder, true) }),
			gap(12),
			layout.Rigid(func(gtx C) D { return a.field(gtx, &a.pathEd, nil, "…or paste a folder path and press Enter") }),
		}
		if len(v.opening) > 0 {
			kids = append(kids, gap(14), layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(16), gtx.Dp(16)))
						return material.Loader(a.th).Layout(gtx)
					}),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Left: 8}.Layout(gtx, a.line(13, a.col.Muted, "Starting "+strings.Join(v.opening, ", ")+"…"))
					}),
				)
			}))
		}
		if len(v.recents) > 0 {
			kids = append(kids, gap(24), layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return a.bold(11, a.col.Muted, "RECENT").Layout(gtx)
			}), gap(6))
			for i, d := range v.recents {
				if i == 6 {
					break
				}
				kids = append(kids, layout.Rigid(func(gtx C) D {
					return a.rowBtn(gtx, "welcome-recent:"+d, false, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D { return icon(gtx, icFolder, 16, a.col.Accent) }),
							layout.Flexed(1, func(gtx C) D {
								return layout.Inset{Left: 10}.Layout(gtx, func(gtx C) D {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(a.line(13, a.col.Fg, filepath.Base(d))),
										layout.Rigid(a.line(11, a.col.Muted, d)),
									)
								})
							}),
						)
					})
				}))
			}
		}
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx, kids...)
	})
}

func (a *App) layoutHeader(gtx C, v view) D {
	sid := v.sess
	if a.click("title").Clicked(gtx) {
		a.renaming = true
		a.renameEd.SetText(v.session.GetTitle())
	}
	if a.renaming {
		gtx.Execute(key.FocusCmd{Tag: &a.renameEd})
	}
	for {
		ev, ok := a.renameEd.Update(gtx)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case widget.SubmitEvent:
			a.renaming = false
			if t := strings.TrimSpace(e.Text); t != "" && t != v.session.GetTitle() {
				a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
					_, err := c.RenameSession(ctx, &deckv1.RenameSessionRequest{SessionId: sid, Title: t})
					return err
				})
			}
		}
	}
	if a.click("panel").Clicked(gtx) {
		a.saveSettings(func(s *settings.Settings) { s.PanelOpen = !s.PanelOpen })
	}
	menu := []menuItem{
		{"rename", "Rename", icEdit, func() { a.renaming = true; a.renameEd.SetText(v.session.GetTitle()) }},
		{"delete", "Delete…", icDelete, func() { a.confirm = sid }},
	}
	t := v.session.GetTitle()
	if t == "" {
		t = "New thread"
	}
	return layout.Inset{Top: 10, Bottom: 10, Left: 20, Right: 12}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				if a.renaming {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
					return a.field(gtx, &a.renameEd, icEdit, "Thread name, Enter to save")
				}
				return material.Clickable(gtx, a.click("title"), func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							l := a.bold(16, a.col.Fg, t)
							l.MaxLines = 1
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx C) D {
							if !v.session.GetBusy() {
								return D{}
							}
							return layout.Inset{Left: 10}.Layout(gtx, func(gtx C) D {
								gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(14), gtx.Dp(14)))
								return material.Loader(a.th).Layout(gtx)
							})
						}),
					)
				})
			}),
			layout.Rigid(func(gtx C) D {
				return a.segmented(gtx, "mode", [][2]string{{"ask", "Ask"}, {"plan", "Plan"}, {"yolo", "Yolo"}}, v.session.GetMode(), func(m string) {
					a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
						_, err := c.SetMode(ctx, &deckv1.SetModeRequest{SessionId: sid, Mode: m})
						return err
					})
				})
			}),
			gap(10),
			layout.Rigid(func(gtx C) D {
				tip := "Show the side panel  Ctrl+B"
				c := a.col.Muted
				if v.set.PanelOpen {
					tip, c = "Hide the side panel  Ctrl+B", a.col.Fg
				}
				return a.iconBtn(gtx, "panel", icPanel, tip, c)
			}),
			layout.Rigid(func(gtx C) D {
				return a.withMenu(gtx, "tmenu", true, menu, func(gtx C) D { return a.iconBtn(gtx, "tmore", icMore, "Thread", a.col.Muted) })
			}),
		)
	})
}

func (a *App) layoutConfirm(gtx C, v view) D {
	if a.confirm == "" || a.confirm != v.sess {
		return D{}
	}
	if a.click("confirm-cancel").Clicked(gtx) {
		a.confirm = ""
	}
	if a.click("confirm-delete").Clicked(gtx) {
		a.confirm = ""
		a.deleteThread(v.proj, v.sess)
	}
	return layout.Inset{Top: 10, Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		return outlined(gtx, a.col.Danger, a.col.Raised, 10, layout.UniformInset(12), func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return icon(gtx, icWarn, 20, a.col.Danger) }),
				layout.Flexed(1, func(gtx C) D {
					return layout.Inset{Left: 10, Right: 10}.Layout(gtx, a.label(13, a.col.Fg, "Delete this thread and its history? This cannot be undone.").Layout)
				}),
				layout.Rigid(func(gtx C) D { return a.button(gtx, "confirm-cancel", "Cancel", nil, false) }),
				gap(8),
				layout.Rigid(func(gtx C) D { return a.dangerButton(gtx, "confirm-delete", "Delete") }),
			)
		})
	})
}

// agentOf maps a message's agent (a roster id or name) to its name, tint
// and mark.
func (a *App) agentOf(v view, agent string) (name string, tint color.NRGBA, glyph string) {
	for _, ag := range v.roster {
		if ag.GetId() == agent || ag.GetName() == agent {
			return ag.GetName(), a.col.tint(v.cur.theme, ag.GetKind()), mark(v.cur.theme, ag.GetKind(), ag.GetName())
		}
	}
	return agent, a.col.tint(v.cur.theme, agent), mark(v.cur.theme, agent, agent)
}

func (a *App) layoutChat(gtx C, v view) D {
	if len(v.msgs) == 0 {
		names := make([]string, 0, len(v.roster))
		for _, ag := range v.roster {
			names = append(names, "@"+ag.GetName())
		}
		sub := "Every agent in the thread sees each message."
		if len(names) > 0 {
			sub += " Address one with " + strings.Join(names, ", ") + "."
		}
		return layout.Center.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(a.bold(18, a.col.Fg, "What should the agents do?").Layout),
				gap(6),
				layout.Rigid(a.label(13, a.col.Muted, sub).Layout),
			)
		})
	}
	return material.List(a.th, &a.chat).Layout(gtx, len(v.msgs), func(gtx C, i int) D {
		in := layout.Inset{Top: 8, Bottom: 10, Left: 20, Right: 20}
		if i == 0 {
			in.Top = 20
		}
		return column(gtx, chatWidth, func(gtx C) D {
			return in.Layout(gtx, func(gtx C) D { return a.layoutMessage(gtx, v, v.msgs[i]) })
		})
	})
}

func (a *App) layoutMessage(gtx C, v view, m *deckv1.Message) D {
	switch {
	case m.GetRole() == "user":
		return a.userMessage(gtx, m)
	case m.GetRole() == "assistant" && m.GetAgent() != "":
		return a.agentMessage(gtx, v, m)
	}
	// Deck's own lines: who joined, notices.
	var text []string
	for _, p := range m.GetParts() {
		if p.GetType() == "text" {
			text = append(text, inlineStrip(p.GetText()))
		}
	}
	return layout.Center.Layout(gtx, a.label(12, a.col.Muted, strings.Join(text, " ")).Layout)
}

// inlineStrip drops markdown emphasis from a one-line note.
func inlineStrip(s string) string {
	s = strings.TrimSpace(s)
	for _, m := range []string{"**", "__", "_", "*"} {
		if len(s) > 2*len(m) && strings.HasPrefix(s, m) && strings.HasSuffix(s, m) {
			s = s[len(m) : len(s)-len(m)]
		}
	}
	return s
}

func textOf(m *deckv1.Message) string {
	var parts []string
	for _, p := range m.GetParts() {
		if p.GetType() == "text" {
			parts = append(parts, p.GetText())
		}
	}
	return strings.Join(parts, "\n\n")
}

func (a *App) userMessage(gtx C, m *deckv1.Message) D {
	return layout.E.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Max.X = gtx.Constraints.Max.X * 85 / 100
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return surface(gtx, a.col.Raised, 14, layout.Inset{Top: 10, Bottom: 6, Left: 14, Right: 14}, func(gtx C) D {
					var kids []layout.FlexChild
					for i, p := range m.GetParts() {
						key := fmt.Sprintf("%s/%d", m.GetId(), i)
						switch p.GetType() {
						case "text":
							kids = append(kids, layout.Rigid(func(gtx C) D { return a.markdownView(gtx, key, p.GetText()) }))
						case "image":
							kids = append(kids, layout.Rigid(func(gtx C) D { return a.imageChip(gtx, p.GetImage().GetName()) }))
						}
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
				})
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 3, Right: 4}.Layout(gtx, a.line(11, a.col.Muted, clock(m.GetCreatedAt())))
			}),
		)
	})
}

func (a *App) imageChip(gtx C, name string) D {
	return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D {
		return outlined(gtx, a.col.Line, a.col.Field, 8, layout.Inset{Top: 5, Bottom: 5, Left: 8, Right: 10}, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return icon(gtx, icImage, 16, a.col.Accent) }),
				layout.Rigid(func(gtx C) D { return layout.Inset{Left: 6}.Layout(gtx, a.line(12, a.col.Fg, name)) }),
			)
		})
	})
}

func clock(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("15:04")
}

func (a *App) agentMessage(gtx C, v view, m *deckv1.Message) D {
	name, tint, glyph := a.agentOf(v, m.GetAgent())
	results := map[string]*deckv1.Part{}
	for _, p := range m.GetParts() {
		if p.GetType() == "tool_result" {
			results[p.GetToolCallId()] = p
		}
	}
	key := m.GetId()
	if a.click(key + "/copy").Clicked(gtx) {
		a.copyText(textOf(m))
	}

	var body []layout.FlexChild
	body = append(body, layout.Rigid(func(gtx C) D {
		return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(a.bold(13, tint, name).Layout),
				layout.Rigid(func(gtx C) D {
					t := clock(m.GetCreatedAt())
					if !m.GetFinished() {
						t += "  ·  working"
					}
					return layout.Inset{Left: 8}.Layout(gtx, a.line(11, a.col.Muted, t))
				}),
			)
		})
	}))
	for i, p := range m.GetParts() {
		pk := fmt.Sprintf("%s/%d", key, i)
		var w layout.Widget
		switch p.GetType() {
		case "text":
			w = func(gtx C) D { return a.markdownView(gtx, pk, p.GetText()) }
		case "reasoning":
			w = func(gtx C) D { return a.reasoning(gtx, pk, p.GetText()) }
		case "tool_call":
			res := results[p.GetId()]
			w = func(gtx C) D { return a.toolRow(gtx, pk, p, res, m.GetFinished()) }
		case "tool_result":
			if !slices.ContainsFunc(m.GetParts(), func(c *deckv1.Part) bool { return c.GetType() == "tool_call" && c.GetId() == p.GetToolCallId() }) && p.GetIsError() {
				w = func(gtx C) D { return a.toolRow(gtx, pk, &deckv1.Part{Name: "tool", Summary: "failed"}, p, true) }
			}
		case "image":
			w = func(gtx C) D { return a.imageChip(gtx, p.GetImage().GetName()) }
		}
		if w != nil {
			body = append(body, layout.Rigid(w))
		}
	}
	if m.GetFinished() && textOf(m) != "" {
		body = append(body, layout.Rigid(func(gtx C) D {
			return a.iconBtn(gtx, key+"/copy", icCopy, "Copy the reply", a.col.Muted)
		}))
	}

	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return a.avatar(gtx, tint, glyph, 26) }),
		gap(12),
		layout.Flexed(1, func(gtx C) D { return layout.Flex{Axis: layout.Vertical}.Layout(gtx, body...) }),
	)
}

func (a *App) reasoning(gtx C, key, text string) D {
	k := key + "/think"
	if a.click(k).Clicked(gtx) {
		a.expanded[k] = !a.expanded[k]
	}
	open := a.expanded[k]
	return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return material.Clickable(gtx, a.click(k), func(gtx C) D {
					ic := icChevron
					if open {
						ic = icExpand
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return icon(gtx, ic, 16, a.col.Muted) }),
						layout.Rigid(func(gtx C) D { return layout.Inset{Left: 4}.Layout(gtx, a.line(12, a.col.Muted, "Thinking")) }),
					)
				})
			}),
			layout.Rigid(func(gtx C) D {
				if !open {
					return D{}
				}
				return layout.Inset{Left: 20, Top: 4}.Layout(gtx, a.label(12, a.col.Muted, text).Layout)
			}),
		)
	})
}

// toolRow is one tool call, collapsed to a line; open, it shows the diff
// or output of its result.
func (a *App) toolRow(gtx C, key string, call, res *deckv1.Part, finished bool) D {
	k := key + "/tool"
	if a.click(k).Clicked(gtx) {
		a.expanded[k] = !a.expanded[k]
	}
	open := a.expanded[k]
	detail := ""
	if res != nil {
		detail = res.GetDiff()
		if detail == "" {
			detail = res.GetOutput()
		}
	}
	return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.rowBtn(gtx, k, open, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return icon(gtx, icTool, 15, a.col.Muted) }),
						layout.Rigid(func(gtx C) D { return layout.Inset{Left: 8}.Layout(gtx, a.bold(12, a.col.Fg, call.GetName()).Layout) }),
						layout.Flexed(1, func(gtx C) D {
							return layout.Inset{Left: 8}.Layout(gtx, a.line(12, a.col.Muted, oneLine(call.GetSummary())))
						}),
						layout.Rigid(func(gtx C) D {
							switch {
							case res != nil && res.GetIsError():
								return icon(gtx, icWarn, 15, a.col.Danger)
							case res != nil:
								return icon(gtx, icDone, 15, a.col.Add)
							case !finished:
								gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(13), gtx.Dp(13)))
								return material.Loader(a.th).Layout(gtx)
							}
							return D{}
						}),
						layout.Rigid(func(gtx C) D {
							if detail == "" {
								return D{}
							}
							ic := icChevron
							if open {
								ic = icExpand
							}
							return layout.Inset{Left: 4}.Layout(gtx, func(gtx C) D { return icon(gtx, ic, 16, a.col.Muted) })
						}),
					)
				})
			}),
			layout.Rigid(func(gtx C) D {
				if !open || detail == "" {
					return D{}
				}
				return layout.Inset{Top: 4, Left: 4}.Layout(gtx, func(gtx C) D {
					if res.GetDiff() != "" {
						return a.diffView(gtx, k+"/diff", res.GetDiff())
					}
					return a.codeView(gtx, k+"/out", clipLines(res.GetOutput(), 60), "", "")
				})
			}),
		)
	})
}

func (a *App) layoutApprovals(gtx C, v view) D {
	if len(v.approvals) == 0 {
		return D{}
	}
	var kids []layout.FlexChild
	for _, ap := range v.approvals {
		kids = append(kids, layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: 8, Left: 20, Right: 20}.Layout(gtx, func(gtx C) D { return a.layoutApproval(gtx, v, ap) })
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

func (a *App) layoutApproval(gtx C, v view, ap *deckv1.Approval) D {
	decisions := []struct {
		key, label string
		primary    bool
	}{{"allow", "Allow", true}, {"allow_session", "Allow for this thread", false}, {"deny", "Deny", false}}
	for _, d := range decisions {
		if a.click("ans:" + ap.GetId() + ":" + d.key).Clicked(gtx) {
			sid, id, dec := ap.GetSessionId(), ap.GetId(), d.key
			a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.Answer(ctx, &deckv1.AnswerRequest{SessionId: sid, Id: id, Decision: dec})
				return err
			})
		}
	}
	name, tint, _ := a.agentOf(v, ap.GetAgent())
	return elevated(gtx, a.col.Raised, 12, layout.UniformInset(14), func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return icon(gtx, icWarn, 18, a.col.Busy) }),
					layout.Rigid(func(gtx C) D { return layout.Inset{Left: 8}.Layout(gtx, a.bold(13, tint, name).Layout) }),
					layout.Flexed(1, func(gtx C) D {
						return layout.Inset{Left: 6}.Layout(gtx, a.line(13, a.col.Fg, "wants to "+approvalVerb(ap.GetKind())))
					}),
				)
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 6, Bottom: 10}.Layout(gtx, a.label(13, a.col.Fg, ap.GetDescription()).Layout)
			}),
			layout.Rigid(func(gtx C) D {
				switch {
				case ap.GetDiff() != "":
					return layout.Inset{Bottom: 10}.Layout(gtx, func(gtx C) D { return a.diffView(gtx, "ap:"+ap.GetId(), clipLines(ap.GetDiff(), 40)) })
				case ap.GetCommand() != "":
					return layout.Inset{Bottom: 10}.Layout(gtx, func(gtx C) D { return a.codeView(gtx, "ap:"+ap.GetId(), ap.GetCommand(), "sh", "") })
				case ap.GetPath() != "":
					return layout.Inset{Bottom: 10}.Layout(gtx, a.line(12, a.col.Muted, ap.GetPath()))
				}
				return D{}
			}),
			layout.Rigid(func(gtx C) D {
				var bs []layout.FlexChild
				for i, d := range decisions {
					if i > 0 {
						bs = append(bs, gap(8))
					}
					bs = append(bs, layout.Rigid(func(gtx C) D {
						return a.button(gtx, "ans:"+ap.GetId()+":"+d.key, d.label, nil, d.primary)
					}))
				}
				return layout.Flex{}.Layout(gtx, bs...)
			}),
		)
	})
}

func approvalVerb(kind string) string {
	switch kind {
	case "command":
		return "run a command"
	case "edit":
		return "edit a file"
	}
	return "use a tool"
}

func (a *App) layoutComposer(gtx C, v view) D {
	for i := range a.attach {
		if a.click(fmt.Sprintf("unattach:%d", i)).Clicked(gtx) {
			a.attach = slices.Delete(a.attach, i, i+1)
			break
		}
	}
	busy := v.session.GetBusy()
	return layout.Inset{Top: 10, Bottom: 16, Left: 20, Right: 20}.Layout(gtx, func(gtx C) D {
		return elevated(gtx, a.col.Field, 16, layout.Inset{Top: 12, Bottom: 8, Left: 14, Right: 8}, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					if len(a.attach) == 0 {
						return D{}
					}
					var kids []layout.FlexChild
					for i, at := range a.attach {
						k := fmt.Sprintf("unattach:%d", i)
						kids = append(kids, layout.Rigid(func(gtx C) D {
							return layout.Inset{Right: 6, Bottom: 8}.Layout(gtx, func(gtx C) D { return a.chip(gtx, k, at.name+"  ✕", false) })
						}))
					}
					return layout.Flex{}.Layout(gtx, kids...)
				}),
				layout.Rigid(func(gtx C) D {
					e := material.Editor(a.th, &a.composer, "Message the agents…")
					e.HintColor = a.col.Muted
					e.TextSize = a.sp(14)
					gtx.Constraints.Min.Y = gtx.Dp(24)
					gtx.Constraints.Max.Y = gtx.Dp(240)
					return layout.Inset{Right: 6, Bottom: 8}.Layout(gtx, e.Layout)
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, "attach", icAttach, "Attach images", a.col.Muted) }),
						layout.Flexed(1, func(gtx C) D {
							hint := "Enter to send · Shift+Enter for a new line"
							if n := len(v.queue); n > 0 {
								hint = fmt.Sprintf("%d waiting in the queue · %s", n, hint)
							}
							return layout.Inset{Left: 8}.Layout(gtx, a.line(11, a.col.Muted, hint))
						}),
						layout.Rigid(func(gtx C) D {
							if !busy {
								return D{}
							}
							return layout.Inset{Right: 6}.Layout(gtx, func(gtx C) D { return a.roundBtn(gtx, "stop", icStop, a.col.Danger) })
						}),
						layout.Rigid(func(gtx C) D { return a.roundBtn(gtx, "send", icSend, a.col.Accent) }),
					)
				}),
			)
		})
	})
}
