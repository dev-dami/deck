package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/oligo/gioview/explorer"
	"github.com/oligo/gioview/navi"
	gvtheme "github.com/oligo/gioview/theme"

	"github.com/dev-dami/deck/desktop/backend"
	"github.com/dev-dami/deck/desktop/platform"
	"github.com/dev-dami/deck/desktop/settings"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// gvTheme adapts the app theme to gioview's, which its file tree draws
// with.
func (a *App) gvTheme() *gvtheme.Theme {
	return &gvtheme.Theme{Theme: a.th, HoverAlpha: 40, SelectedAlpha: 80, Bg2: a.col.Rail}
}

func (a *App) layoutPanel(gtx C, v view) D {
	paint.FillShape(gtx.Ops, a.col.Rail, clip.Rect{Max: gtx.Constraints.Max}.Op())
	tabs := []struct {
		t     tab
		label string
		icon  *widget.Icon
	}{
		{tabChanges, "Changes", icDiff},
		{tabFiles, "Files", icFile},
		{tabAgents, "Agents", icPeople},
		{tabQueue, "Queue", icQueue},
	}
	for _, tb := range tabs {
		if a.click(fmt.Sprint("tab:", int(tb.t))).Clicked(gtx) && a.tab != tb.t {
			a.tab = tb.t
			name := tabNames[tb.t]
			a.saveSettings(func(s *settings.Settings) { s.PanelTab = name })
		}
	}

	var items []layout.Widget
	switch a.tab {
	case tabChanges:
		items = a.changesItems(gtx, v)
	case tabFiles:
		items = a.fileItems(gtx, v)
	case tabAgents:
		items = a.agentItems(gtx, v)
	case tabQueue:
		items = a.queueItems(gtx, v)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: 10, Left: 10, Right: 10, Bottom: 8}.Layout(gtx, func(gtx C) D {
				var kids []layout.FlexChild
				for _, tb := range tabs {
					k := fmt.Sprint("tab:", int(tb.t))
					on := a.tab == tb.t
					kids = append(kids, layout.Flexed(1, func(gtx C) D {
						return material.Clickable(gtx, a.click(k), func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							bg, fg := a.col.Rail, a.col.Muted
							switch {
							case on:
								bg, fg = a.col.Selected, a.col.Fg
							case a.click(k).Hovered():
								bg = a.col.Hover
							}
							return surface(gtx, bg, 8, layout.Inset{Top: 6, Bottom: 6, Left: 4, Right: 4}, func(gtx C) D {
								label := tb.label
								return layout.Flex{Alignment: layout.Middle, Spacing: layout.SpaceSides}.Layout(gtx,
									layout.Rigid(func(gtx C) D { return icon(gtx, tb.icon, 15, fg) }),
									layout.Rigid(func(gtx C) D { return layout.Inset{Left: 6}.Layout(gtx, a.line(12, fg, label)) }),
								)
							})
						})
					}))
				}
				return layout.Flex{}.Layout(gtx, kids...)
			})
		}),
		layout.Rigid(a.hline),
		layout.Flexed(1, func(gtx C) D {
			return layout.UniformInset(10).Layout(gtx, func(gtx C) D {
				return material.List(a.th, &a.panel).Layout(gtx, len(items), func(gtx C, i int) D {
					return layout.Inset{Bottom: 6}.Layout(gtx, items[i])
				})
			})
		}),
	)
}

func (a *App) empty(text string) layout.Widget {
	return func(gtx C) D {
		return layout.Inset{Top: 24}.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.N.Layout(gtx, a.label(13, a.col.Muted, text).Layout)
		})
	}
}

func (a *App) changesItems(gtx C, v view) []layout.Widget {
	if a.click("refresh-changes").Clicked(gtx) {
		if p := a.project(v.proj); p != nil {
			go a.loadChanges(p, v.sess)
		}
	}
	added, removed := 0, 0
	for _, f := range v.changes {
		added += int(f.GetAdded())
		removed += int(f.GetRemoved())
	}
	items := []layout.Widget{func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				if len(v.changes) == 0 {
					return a.line(12, a.col.Muted, "No changes in this thread yet")(gtx)
				}
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(a.line(12, a.col.Muted, fmt.Sprintf("%d files  ", len(v.changes)))),
					layout.Rigid(a.line(12, a.col.Add, fmt.Sprintf("+%d ", added))),
					layout.Rigid(a.line(12, a.col.Del, fmt.Sprintf("−%d", removed))),
				)
			}),
			layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, "refresh-changes", icDiff, "Refresh", a.col.Muted) }),
		)
	}}
	for _, f := range v.changes {
		k := "file:" + v.sess + "|" + f.GetPath()
		if a.click(k).Clicked(gtx) {
			a.expanded[k] = !a.expanded[k]
		}
		open := a.expanded[k]
		items = append(items, func(gtx C) D {
			return a.rowBtn(gtx, k, open, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return icon(gtx, icFile, 15, a.col.Muted) }),
					layout.Flexed(1, func(gtx C) D {
						return layout.Inset{Left: 8}.Layout(gtx, func(gtx C) D {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(a.line(13, a.col.Fg, filepath.Base(f.GetPath()))),
								layout.Rigid(a.line(11, a.col.Muted, filepath.Dir(f.GetPath()))),
							)
						})
					}),
					layout.Rigid(a.line(12, a.col.Add, fmt.Sprintf(" +%d", f.GetAdded()))),
					layout.Rigid(a.line(12, a.col.Del, fmt.Sprintf(" −%d", f.GetRemoved()))),
				)
			})
		})
		if open && f.GetDiff() != "" {
			items = append(items, func(gtx C) D { return a.diffView(gtx, k+"/diff", f.GetDiff()) })
		}
	}
	return items
}

// fileItems is the project's file tree (gioview's explorer) or, once a
// file is picked, its highlighted contents.
func (a *App) fileItems(gtx C, v view) []layout.Widget {
	if a.preview != "" && strings.HasPrefix(a.preview, v.proj) {
		if a.click("preview-back").Clicked(gtx) {
			a.preview, a.previewTx = "", ""
		}
		path, text := a.preview, a.previewTx
		if a.click("preview-edit").Clicked(gtx) {
			a.fail(platform.OpenEditor(path))
		}
		rel, _ := filepath.Rel(v.proj, path)
		return []layout.Widget{
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, "preview-back", icClose, "Back to the files", a.col.Muted) }),
					layout.Flexed(1, func(gtx C) D { return layout.Inset{Left: 6}.Layout(gtx, a.line(13, a.col.Fg, rel)) }),
					layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, "preview-edit", icOpen, "Open in VS Code", a.col.Muted) }),
				)
			},
			func(gtx C) D { return a.codeView(gtx, "preview:"+path, text, "", filepath.Base(path)) },
		}
	}
	tree := a.trees[v.proj]
	if tree == nil {
		root, err := explorer.NewEntryNavItem(v.proj)
		if err != nil {
			return []layout.Widget{a.empty(err.Error())}
		}
		root.OnSelectFunc = func(n *explorer.EntryNode) { a.openPreview(n.Path) }
		root.SetExpanded(true)
		tree = navi.NewNavItem(root, nil)
		a.trees[v.proj] = tree
	}
	return []layout.Widget{func(gtx C) D { return tree.Layout(gtx, a.gvTheme()) }}
}

func (a *App) agentItems(gtx C, v view) []layout.Widget {
	sid := v.sess
	reload := func(p *backend.Project) { a.loadSession(p, sid) }
	if len(v.roster) == 0 {
		return []layout.Widget{a.empty("No agents have joined yet.")}
	}
	var items []layout.Widget
	for _, ag := range v.roster {
		kind, id := ag.GetKind(), ag.GetId()
		tint := a.col.tint(v.cur.theme, kind)
		if a.click("interrupt:" + id).Clicked(gtx) {
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
		modelOpts := make([][2]string, 0, len(models))
		for _, m := range models {
			label := m.GetLabel()
			if label == "" {
				label = m.GetId()
			}
			modelOpts = append(modelOpts, [2]string{m.GetId(), label})
		}
		var effortOpts [][2]string
		if current != nil {
			for _, e := range current.GetEfforts() {
				effortOpts = append(effortOpts, [2]string{e, e})
			}
		}
		items = append(items, func(gtx C) D {
			return outlined(gtx, a.col.Line, a.col.Bg, 12, layout.UniformInset(12), func(gtx C) D {
				kids := []layout.FlexChild{
					layout.Rigid(func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D { return a.avatar(gtx, tint, mark(v.cur.theme, kind, ag.GetName()), 28) }),
							layout.Flexed(1, func(gtx C) D {
								return layout.Inset{Left: 10}.Layout(gtx, func(gtx C) D {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(a.bold(14, a.col.Fg, ag.GetName()).Layout),
										layout.Rigid(a.line(11, a.col.Muted, strings.TrimSpace(kind+"  ·  "+ag.GetModel()+"  "+ag.GetEffort()))),
									)
								})
							}),
							layout.Rigid(func(gtx C) D {
								if !ag.GetBusy() {
									return a.line(11, a.col.Muted, "idle")(gtx)
								}
								return a.button(gtx, "interrupt:"+id, "Interrupt", icStop, false)
							}),
						)
					}),
				}
				if len(modelOpts) > 0 {
					kids = append(kids, gap(10), layout.Rigid(a.line(11, a.col.Muted, "MODEL")), gap(4), layout.Rigid(func(gtx C) D {
						return a.wrapSegmented(gtx, "model:"+kind, modelOpts, current.GetId(), func(model string) {
							a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
								_, err := c.SetModel(ctx, &deckv1.SetModelRequest{SessionId: sid, Kind: kind, Model: model})
								return err
							}, reload)
						})
					}))
				}
				if len(effortOpts) > 0 {
					kids = append(kids, gap(8), layout.Rigid(a.line(11, a.col.Muted, "EFFORT")), gap(4), layout.Rigid(func(gtx C) D {
						return a.segmented(gtx, "effort:"+kind, effortOpts, ag.GetEffort(), func(effort string) {
							model := current.GetId()
							a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
								_, err := c.SetModel(ctx, &deckv1.SetModelRequest{SessionId: sid, Kind: kind, Model: model, Effort: effort})
								return err
							}, reload)
						})
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
			})
		})
	}
	return items
}

// wrapSegmented is segmented chips in rows of two, for long model names.
func (a *App) wrapSegmented(gtx C, key string, opts [][2]string, cur string, pick func(string)) D {
	var rows []layout.FlexChild
	for i := 0; i < len(opts); i += 2 {
		row := opts[i:min(i+2, len(opts))]
		rows = append(rows, layout.Rigid(func(gtx C) D {
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D { return a.segmented(gtx, key, row, cur, pick) })
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (a *App) queueItems(gtx C, v view) []layout.Widget {
	sid := v.sess
	if len(v.queue) == 0 {
		return []layout.Widget{a.empty("Nothing waiting. Messages sent while an agent works line up here.")}
	}
	var items []layout.Widget
	for _, q := range v.queue {
		id := q.GetId()
		if a.click("steer:" + id).Clicked(gtx) {
			a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.Steer(ctx, &deckv1.QueueItemRef{SessionId: sid, ItemId: id})
				return err
			})
		}
		if a.click("cancelq:" + id).Clicked(gtx) {
			a.do(v.proj, func(ctx context.Context, c deckv1.DeckClient) error {
				_, err := c.CancelQueued(ctx, &deckv1.QueueItemRef{SessionId: sid, ItemId: id})
				return err
			})
		}
		name, tint, _ := a.agentOf(v, q.GetAgent())
		items = append(items, func(gtx C) D {
			return outlined(gtx, a.col.Line, a.col.Bg, 12, layout.UniformInset(12), func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(a.bold(12, tint, "for "+name).Layout),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 4, Bottom: 10}.Layout(gtx, a.label(13, a.col.Fg, clipLines(q.GetText(), 6)).Layout)
					}),
					layout.Rigid(func(gtx C) D {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx C) D { return a.button(gtx, "steer:"+id, "Send now", icSend, true) }),
							gap(8),
							layout.Rigid(func(gtx C) D { return a.button(gtx, "cancelq:"+id, "Cancel", nil, false) }),
						)
					}),
				)
			})
		})
	}
	return items
}
