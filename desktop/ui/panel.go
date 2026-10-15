package ui

import (
	"context"
	"fmt"
	"strings"

	"gioui.org/layout"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/desktop/backend"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

const maxDiffLines = 400

// layoutDiff colours a unified diff by line.
func (a *App) layoutDiff(gtx C, diff string) D {
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	if len(lines) > maxDiffLines {
		lines = append(lines[:maxDiffLines], fmt.Sprintf("… %d more lines", len(lines)-maxDiffLines))
	}
	kids := make([]layout.FlexChild, 0, len(lines))
	for _, ln := range lines {
		c := a.col.Fg
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
			c = a.col.Muted
		case strings.HasPrefix(ln, "+"):
			c = a.col.Add
		case strings.HasPrefix(ln, "-"):
			c = a.col.Del
		case strings.HasPrefix(ln, "@@"):
			c = a.col.Accent
		}
		kids = append(kids, layout.Rigid(func(gtx C) D {
			l := a.label(12, c, ln)
			l.Font.Typeface = "Go Mono"
			l.MaxLines = 1
			return l.Layout(gtx)
		}))
	}
	return card(gtx, a.col.Code, 8, func(gtx C) D { return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...) })
}

func (a *App) layoutPanel(gtx C, v view) D {
	width := gtx.Dp(380)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
	sid := v.sess

	refresh := a.click("refresh-changes")
	if refresh.Clicked(gtx) {
		if p := a.project(v.proj); p != nil {
			go a.loadChanges(p, sid)
		}
	}

	tabs := [][2]string{{"0", fmt.Sprintf("Changes %d", len(v.changes))}, {"1", "Agents"}, {"2", fmt.Sprintf("Queue %d", len(v.queue))}}
	var items []layout.Widget
	items = append(items, func(gtx C) D {
		return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx C) D {
			return a.segmented(gtx, "tab", tabs, fmt.Sprint(int(a.tab)), func(s string) {
				a.tab = tab(s[0] - '0')
			})
		})
	})

	switch a.tab {
	case tabChanges:
		items = append(items, a.changesItems(gtx, v)...)
	case tabAgents:
		items = append(items, a.agentItems(gtx, v)...)
	case tabQueue:
		items = append(items, a.queueItems(gtx, v)...)
	}
	return layout.UniformInset(14).Layout(gtx, func(gtx C) D {
		return material.List(a.th, &a.panel).Layout(gtx, len(items), func(gtx C, i int) D {
			return layout.Inset{Bottom: 6}.Layout(gtx, items[i])
		})
	})
}

func (a *App) changesItems(gtx C, v view) []layout.Widget {
	var items []layout.Widget
	added, removed := 0, 0
	for _, f := range v.changes {
		added += int(f.GetAdded())
		removed += int(f.GetRemoved())
	}
	items = append(items, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				if len(v.changes) == 0 {
					return a.line(12, a.col.Muted, "Nothing changed in this thread yet.")(gtx)
				}
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(a.line(12, a.col.Muted, fmt.Sprintf("%d files  ", len(v.changes)))),
					layout.Rigid(a.line(12, a.col.Add, fmt.Sprintf("+%d ", added))),
					layout.Rigid(a.line(12, a.col.Del, fmt.Sprintf("−%d", removed))),
				)
			}),
			layout.Rigid(func(gtx C) D { return a.link(gtx, a.click("refresh-changes"), "Refresh", a.col.Accent) }),
		)
	})
	for _, f := range v.changes {
		k := v.sess + "|" + f.GetPath()
		btn := a.click("file:" + k)
		if btn.Clicked(gtx) {
			a.openDiff[k] = !a.openDiff[k]
		}
		items = append(items, func(gtx C) D {
			return a.row(gtx, btn, a.openDiff[k], func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, a.line(12, a.col.Fg, f.GetPath())),
					layout.Rigid(a.line(11, a.col.Add, fmt.Sprintf(" +%d", f.GetAdded()))),
					layout.Rigid(a.line(11, a.col.Del, fmt.Sprintf(" −%d", f.GetRemoved()))),
				)
			})
		})
		if a.openDiff[k] && f.GetDiff() != "" {
			items = append(items, func(gtx C) D { return a.layoutDiff(gtx, f.GetDiff()) })
		}
	}
	return items
}

func (a *App) agentItems(gtx C, v view) []layout.Widget {
	sid := v.sess
	reload := func(p *backend.Project) { a.loadSession(p, sid) }
	var items []layout.Widget
	if len(v.roster) == 0 {
		return append(items, a.line(12, a.col.Muted, "No agents in this thread yet."))
	}
	for _, ag := range v.roster {
		kind, id := ag.GetKind(), ag.GetId()
		col := a.col.tint(v.cur.theme, kind)
		stop := a.click("interrupt:" + id)
		if stop.Clicked(gtx) {
			a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.Interrupt(ctx, &deckv1.InterruptRequest{SessionId: sid, Agent: id})
				return err
			})
		}
		models := v.models[kind]
		var current *deckv1.ModelChoice
		for _, m := range models {
			if m.GetId() == ag.GetModel() || (ag.GetModel() == "" && m.GetSelected()) {
				current = m
			}
		}
		for _, m := range models {
			if a.click("model:"+kind+":"+m.GetId()).Clicked(gtx) && m != current {
				model := m.GetId()
				a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
					_, err := c.SetModel(ctx, &deckv1.SetModelRequest{SessionId: sid, Kind: kind, Model: model})
					return err
				}, reload)
			}
		}
		if current != nil {
			for _, e := range current.GetEfforts() {
				if a.click("effort:"+kind+":"+e).Clicked(gtx) && e != ag.GetEffort() {
					model, effort := current.GetId(), e
					a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
						_, err := c.SetModel(ctx, &deckv1.SetModelRequest{SessionId: sid, Kind: kind, Model: model, Effort: effort})
						return err
					}, reload)
				}
			}
		}

		items = append(items, func(gtx C) D {
			return card(gtx, a.col.Card, 12, func(gtx C) D {
				kids := []layout.FlexChild{
					layout.Rigid(func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, a.bold(14, col, ag.GetName()).Layout),
							layout.Rigid(func(gtx C) D {
								if !ag.GetBusy() {
									return a.line(11, a.col.Muted, "idle")(gtx)
								}
								return a.link(gtx, stop, "Interrupt", a.col.Danger)
							}),
						)
					}),
					layout.Rigid(a.line(11, a.col.Muted, strings.TrimSpace(kind+"  "+ag.GetModel()+"  "+ag.GetEffort()))),
				}
				if len(models) > 0 {
					kids = append(kids, layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 8}.Layout(gtx, func(gtx C) D { return a.wrapChips(gtx, "model:"+kind+":", models, current) })
					}))
				}
				if current != nil && len(current.GetEfforts()) > 0 {
					opts := make([][2]string, 0, len(current.GetEfforts()))
					for _, e := range current.GetEfforts() {
						opts = append(opts, [2]string{e, e})
					}
					kids = append(kids, layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 6}.Layout(gtx, func(gtx C) D {
							return a.segmented(gtx, "effort:"+kind, opts, ag.GetEffort(), func(string) {})
						})
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
			})
		})
	}
	return items
}

// wrapChips lays model chips out in rows that fit the width.
func (a *App) wrapChips(gtx C, prefix string, models []*deckv1.ModelChoice, current *deckv1.ModelChoice) D {
	const perRow = 2
	var rows []layout.FlexChild
	for i := 0; i < len(models); i += perRow {
		end := min(i+perRow, len(models))
		row := models[i:end]
		rows = append(rows, layout.Rigid(func(gtx C) D {
			var kids []layout.FlexChild
			for j, m := range row {
				btn := a.click(prefix + m.GetId())
				label := m.GetLabel()
				if label == "" {
					label = m.GetId()
				}
				kids = append(kids, layout.Rigid(func(gtx C) D {
					in := layout.Inset{Bottom: 6}
					if j > 0 {
						in.Left = 6
					}
					return in.Layout(gtx, func(gtx C) D { return a.chip(gtx, btn, label, m == current) })
				}))
			}
			return layout.Flex{}.Layout(gtx, kids...)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (a *App) queueItems(gtx C, v view) []layout.Widget {
	sid := v.sess
	var items []layout.Widget
	if len(v.queue) == 0 {
		return append(items, a.line(12, a.col.Muted, "Nothing waiting. Messages you send while an agent works line up here."))
	}
	for _, q := range v.queue {
		id := q.GetId()
		steer, cancel := a.click("steer:"+id), a.click("cancelq:"+id)
		if steer.Clicked(gtx) {
			a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.Steer(ctx, &deckv1.QueueItemRef{SessionId: sid, ItemId: id})
				return err
			})
		}
		if cancel.Clicked(gtx) {
			a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.CancelQueued(ctx, &deckv1.QueueItemRef{SessionId: sid, ItemId: id})
				return err
			})
		}
		who, col := a.agentTint(v, q.GetAgent())
		items = append(items, func(gtx C) D {
			return card(gtx, a.col.Card, 10, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(a.bold(12, col, "for "+who).Layout),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 4, Bottom: 8}.Layout(gtx, a.label(13, a.col.Fg, clipLines(q.GetText(), 6)).Layout)
					}),
					layout.Rigid(func(gtx C) D {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx C) D { return a.button(gtx, steer, "Send now", a.col.Accent) }),
							layout.Rigid(func(gtx C) D {
								return layout.Inset{Left: 8}.Layout(gtx, func(gtx C) D { return a.button(gtx, cancel, "Cancel", a.col.Line) })
							}),
						)
					}),
				)
			})
		})
	}
	return items
}
