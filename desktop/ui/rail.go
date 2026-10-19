package ui

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"

	"github.com/dev-dami/deck/desktop/platform"
)

type menuItem struct {
	key, label string
	icon       *widget.Icon
	run        func()
}

// withMenu lays w out with a gio-x context menu over it: right-click opens
// it, or a primary click when primary is set (for ••• buttons).
func (a *App) withMenu(gtx C, key string, primary bool, items []menuItem, w layout.Widget) D {
	p := a.popup(key)
	p.area.Activation = pointer.ButtonSecondary
	if primary {
		p.area.Activation = pointer.ButtonPrimary
	}
	p.state.Options = p.state.Options[:0]
	for _, it := range items {
		k := key + "/" + it.key
		if a.click(k).Clicked(gtx) {
			it.run()
		}
		mi := component.MenuItem(a.th, a.click(k), it.label)
		mi.Icon = it.icon
		mi.Label.TextSize = a.sp(13)
		p.state.Options = append(p.state.Options, mi.Layout)
	}
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(w),
		layout.Expanded(func(gtx C) D {
			return p.area.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min = image.Point{}
				gtx.Constraints.Max.X = gtx.Dp(220)
				m := component.Menu(a.th, &p.state)
				m.SurfaceStyle.Fill = a.col.Raised
				return m.Layout(gtx)
			})
		}),
	)
}

func (a *App) layoutRail(gtx C, v view) D {
	width := gtx.Dp(272)
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
	paint.FillShape(gtx.Ops, a.col.Rail, clip.Rect{Max: image.Pt(width, gtx.Constraints.Max.Y)}.Op())

	if a.click("settings").Clicked(gtx) {
		if a.page == pageSettings {
			a.page = pageChat
		} else {
			a.page = pageSettings
		}
	}
	if a.click("rail-new").Clicked(gtx) {
		a.newThread(v.proj)
	}
	if a.click("rail-open").Clicked(gtx) {
		a.pickFolder()
	}
	search := strings.ToLower(strings.TrimSpace(a.searchEd.Text()))

	var items []layout.Widget
	for _, p := range v.projects {
		items = append(items, a.projectItems(gtx, v, p, search)...)
	}
	if len(v.opening) > 0 {
		items = append(items, func(gtx C) D {
			return layout.Inset{Top: 10, Left: 10}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(14), gtx.Dp(14)))
						return material.Loader(a.th).Layout(gtx)
					}),
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Left: 8}.Layout(gtx, a.line(12, a.col.Muted, "Starting "+strings.Join(v.opening, ", ")+"…"))
					}),
				)
			})
		})
	}
	if len(v.recents) > 0 && len(v.projects) > 0 {
		items = append(items, func(gtx C) D {
			return layout.Inset{Top: 18, Bottom: 4, Left: 10}.Layout(gtx, a.bold(11, a.col.Muted, "RECENT").Layout)
		})
		for _, d := range v.recents {
			k := "recent:" + d
			if a.click(k).Clicked(gtx) {
				a.open(d)
			}
			items = append(items, func(gtx C) D {
				return a.rowBtn(gtx, k, false, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return icon(gtx, icFolder, 15, a.col.Muted) }),
						layout.Flexed(1, func(gtx C) D { return layout.Inset{Left: 8}.Layout(gtx, a.line(13, a.col.Muted, filepath.Base(d))) }),
					)
				})
			})
		}
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: 14, Left: 14, Right: 10, Bottom: 10}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return a.avatar(gtx, a.col.Accent, "D", 24) }),
					layout.Flexed(1, func(gtx C) D { return layout.Inset{Left: 10}.Layout(gtx, a.bold(17, a.col.Fg, "Deck").Layout) }),
					layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, "settings", icSettings, "Settings  Ctrl+,", a.col.Muted) }),
				)
			})
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: 12, Right: 12, Bottom: 8}.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return a.rowBtn(gtx, "rail-new", false, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return icon(gtx, icEdit, 16, a.col.Fg) }),
						layout.Flexed(1, func(gtx C) D { return layout.Inset{Left: 10}.Layout(gtx, a.bold(13, a.col.Fg, "New thread").Layout) }),
						layout.Rigid(a.line(11, a.col.Muted, "Ctrl+N")),
					)
				})
			})
		}),
		layout.Rigid(func(gtx C) D {
			if len(v.projects) == 0 {
				return D{}
			}
			return layout.Inset{Left: 12, Right: 12, Bottom: 6}.Layout(gtx, func(gtx C) D { return a.field(gtx, &a.searchEd, icSearch, "Search threads") })
		}),
		layout.Flexed(1, func(gtx C) D {
			return layout.Inset{Left: 8, Right: 6}.Layout(gtx, func(gtx C) D {
				return material.List(a.th, &a.rail).Layout(gtx, len(items), func(gtx C, i int) D { return items[i](gtx) })
			})
		}),
		layout.Rigid(a.hline),
		layout.Rigid(func(gtx C) D {
			return layout.UniformInset(8).Layout(gtx, func(gtx C) D {
				return a.rowBtn(gtx, "rail-open", false, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return icon(gtx, icFolder, 16, a.col.Fg) }),
						layout.Flexed(1, func(gtx C) D {
							return layout.Inset{Left: 10}.Layout(gtx, a.label(13, a.col.Fg, "Open folder…").Layout)
						}),
						layout.Rigid(a.line(11, a.col.Muted, "Ctrl+O")),
					)
				})
			})
		}),
	)
}

func (a *App) projectItems(gtx C, v view, p projView, search string) []layout.Widget {
	if a.click("new:" + p.key).Clicked(gtx) {
		a.newThread(p.key)
	}
	menu := []menuItem{
		{"new", "New thread", icAdd, func() { a.newThread(p.key) }},
		{"reveal", "Show in folder", icFolder, func() { a.fail(platform.Reveal(p.key)) }},
		{"editor", "Open in VS Code", icCode, func() { a.fail(platform.OpenEditor(p.key)) }},
		{"terminal", "Open terminal", icOpen, func() { a.fail(platform.OpenTerminal(p.key)) }},
		{"close", "Close project", icClose, func() { a.closeProject(p.key) }},
	}

	var items []layout.Widget
	items = append(items, func(gtx C) D {
		return layout.Inset{Top: 12, Bottom: 4, Left: 10}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					l := a.bold(11, a.col.Muted, strings.ToUpper(p.name))
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, "new:"+p.key, icAdd, "New thread", a.col.Muted) }),
				layout.Rigid(func(gtx C) D {
					return a.withMenu(gtx, "pmenu:"+p.key, true, menu, func(gtx C) D {
						return a.iconBtn(gtx, "pmore:"+p.key, icMore, "Project", a.col.Muted)
					})
				}),
			)
		})
	})
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
		sid := s.GetId()
		k := "sess:" + p.key + "|" + sid
		if a.click(k).Clicked(gtx) {
			a.selectSession(p.key, sid)
		}
		on := p.key == v.proj && sid == v.sess && a.page == pageChat
		pending := p.pending[sid]
		rowMenu := []menuItem{
			{"rename", "Rename", icEdit, func() {
				a.selectSession(p.key, sid)
				a.renaming = true
				a.renameEd.SetText(s.GetTitle())
			}},
			{"delete", "Delete…", icDelete, func() {
				a.selectSession(p.key, sid)
				a.confirm = sid
			}},
		}
		items = append(items, func(gtx C) D {
			return a.withMenu(gtx, "smenu:"+k, false, rowMenu, func(gtx C) D {
				return a.rowBtn(gtx, k, on, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, a.line(13, a.col.Fg, t)),
								layout.Rigid(func(gtx C) D {
									switch {
									case pending > 0:
										return layout.Inset{Left: 6}.Layout(gtx, func(gtx C) D {
											return surface(gtx, a.col.Busy, 8, layout.Inset{Left: 6, Right: 6, Top: 1, Bottom: 1}, a.bold(11, a.col.OnAccent, fmt.Sprint(pending)).Layout)
										})
									case s.GetBusy():
										return layout.Inset{Left: 6}.Layout(gtx, func(gtx C) D {
											gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(12), gtx.Dp(12)))
											return material.Loader(a.th).Layout(gtx)
										})
									}
									return layout.Inset{Left: 6}.Layout(gtx, a.line(11, a.col.Muted, ago(s.GetUpdatedAt())))
								}),
							)
						}),
						layout.Rigid(func(gtx C) D {
							if s.GetLastText() == "" {
								return D{}
							}
							return layout.Inset{Top: 2}.Layout(gtx, a.line(12, a.col.Muted, oneLine(s.GetLastText())))
						}),
					)
				})
			})
		})
	}
	if shown == 0 {
		msg := "No threads yet"
		if search != "" {
			msg = "No thread matches"
		}
		items = append(items, func(gtx C) D { return layout.Inset{Left: 10, Top: 4}.Layout(gtx, a.line(12, a.col.Muted, msg)) })
	}
	return items
}
