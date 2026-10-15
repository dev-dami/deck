package ui

import (
	"fmt"
	"runtime"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/dev-dami/deck/desktop/backend"
	"github.com/dev-dami/deck/desktop/platform"
	"github.com/dev-dami/deck/desktop/settings"
)

func (a *App) section(title, sub string, w layout.Widget) layout.Widget {
	return func(gtx C) D {
		return layout.Inset{Bottom: 22}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(a.bold(15, a.col.Fg, title).Layout),
				layout.Rigid(func(gtx C) D {
					if sub == "" {
						return D{}
					}
					return layout.Inset{Top: 2}.Layout(gtx, a.label(12, a.col.Muted, sub).Layout)
				}),
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 10}.Layout(gtx, w) }),
			)
		})
	}
}

func (a *App) toggle(gtx C, b *widget.Bool, label string) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			s := material.Switch(a.th, b, label)
			s.Color.Enabled = a.col.Accent
			return s.Layout(gtx)
		}),
		layout.Rigid(func(gtx C) D { return layout.Inset{Left: 10}.Layout(gtx, a.label(13, a.col.Fg, label).Layout) }),
	)
}

func (a *App) layoutSettings(gtx C, v view) D {
	if a.reopen.Update(gtx) {
		on := a.reopen.Value
		a.saveSettings(func(s *settings.Settings) { s.ReopenProjects = on })
	}
	if a.atLogin.Update(gtx) {
		if err := platform.SetLaunchAtLogin(a.atLogin.Value); err != nil {
			a.atLogin.Value = !a.atLogin.Value
			a.fail(err)
		}
	}
	for {
		ev, ok := a.deckEd.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			a.setDeckPath(strings.Trim(strings.TrimSpace(a.deckEd.Text()), `"`))
		}
	}
	browse := a.click("deck-browse")
	if browse.Clicked(gtx) {
		go func() {
			ps, err := platform.PickFiles(a.hwnd, "Choose the deck program", platform.Filter{Name: "Programs", Pattern: "*.exe;deck*"})
			if err == nil && len(ps) > 0 {
				a.setDeckPath(ps[0])
			} else if err != nil && err != platform.ErrCancelled {
				a.fail(err)
			}
		}()
	}
	reset := a.click("deck-reset")
	if reset.Clicked(gtx) {
		a.setDeckPath("")
	}
	// A path chosen with Browse or reset arrives through the settings.
	if !gtx.Focused(&a.deckEd) && a.deckEd.Text() != v.set.DeckPath {
		a.deckEd.SetText(v.set.DeckPath)
	}
	back := a.click("settings-back")
	if back.Clicked(gtx) {
		a.page = pageChat
	}

	resolved, err := backend.FindDeck(v.set.DeckPath)
	if err != nil {
		resolved = err.Error()
	}
	engine := "No project open."
	if v.cur != nil {
		engine = fmt.Sprintf("%s runs deck %s.", v.cur.name, v.cur.version)
	}

	items := []layout.Widget{
		func(gtx C) D {
			return layout.Inset{Bottom: 20}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, a.bold(22, a.col.Fg, "Settings").Layout),
					layout.Rigid(func(gtx C) D { return a.link(gtx, back, "Back to threads", a.col.Accent) }),
				)
			})
		},
		a.section("Appearance", "", func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return a.segmented(gtx, "theme", [][2]string{{"dark", "Dark"}, {"light", "Light"}}, v.set.Theme, func(t string) {
						a.saveSettings(func(s *settings.Settings) { s.Theme = t })
						a.applyTheme()
					})
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 8}.Layout(gtx, func(gtx C) D {
						return a.segmented(gtx, "size", [][2]string{{"13", "Small text"}, {"14", "Normal"}, {"16", "Large"}}, fmt.Sprint(v.set.TextSize), func(sz string) {
							n := 14
							_, _ = fmt.Sscan(sz, &n)
							a.saveSettings(func(s *settings.Settings) { s.TextSize = n })
							a.applyTheme()
						})
					})
				}),
			)
		}),
		a.section("New threads", "The mode a thread starts in. Ask asks before edits and commands, Plan only plans, Yolo approves everything.", func(gtx C) D {
			return a.segmented(gtx, "defmode", [][2]string{{"", "Deck's default"}, {"ask", "Ask"}, {"plan", "Plan"}, {"yolo", "Yolo"}}, v.set.DefaultMode, func(m string) {
				a.saveSettings(func(s *settings.Settings) { s.DefaultMode = m })
			})
		}),
		a.section("Startup", "", func(gtx C) D {
			kids := []layout.FlexChild{
				layout.Rigid(func(gtx C) D { return a.toggle(gtx, &a.reopen, "Reopen the projects that were open when I quit") }),
			}
			if runtime.GOOS == "windows" {
				kids = append(kids, layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 10}.Layout(gtx, func(gtx C) D { return a.toggle(gtx, &a.atLogin, "Start Deck when I sign in") })
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		}),
		a.section("Engine", "The deck program that runs your projects. Empty uses the one installed with the app, then the one on PATH. Applies to projects opened next.", func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D { return a.editor(gtx, &a.deckEd, "Path to deck, enter to save") }),
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Left: 8}.Layout(gtx, func(gtx C) D { return a.button(gtx, browse, "Browse…", a.col.Line) })
						}),
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Left: 8}.Layout(gtx, func(gtx C) D { return a.button(gtx, reset, "Use default", a.col.Line) })
						}),
					)
				}),
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 8}.Layout(gtx, a.label(12, a.col.Muted, "Using: "+resolved).Layout) }),
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 2}.Layout(gtx, a.label(12, a.col.Muted, engine).Layout) }),
			)
		}),
		a.section("Shortcuts", "", func(gtx C) D {
			rows := [][2]string{{"Ctrl+O", "Open a project folder"}, {"Ctrl+N", "New thread"}, {"Ctrl+B", "Show or hide the side panel"}, {"Ctrl+,", "Settings"}, {"Enter / Shift+Enter", "Send / new line"}}
			kids := make([]layout.FlexChild, 0, len(rows))
			for _, r := range rows {
				kids = append(kids, layout.Rigid(func(gtx C) D {
					return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx C) D {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								gtx.Constraints.Min.X = gtx.Dp(unit.Dp(150))
								return a.line(13, a.col.Accent, r[0])(gtx)
							}),
							layout.Rigid(a.line(13, a.col.Fg, r[1])),
						)
					})
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
		}),
		a.section("About", "", a.label(13, a.col.Muted, "Deck desktop "+a.version+". The app is a window over the deck CLI; each project runs its own engine.").Layout),
	}
	return layout.Inset{Top: 24, Left: 36, Right: 36}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(760))
		return material.List(a.th, &a.settingsL).Layout(gtx, len(items), func(gtx C, i int) D { return items[i](gtx) })
	})
}

func (a *App) setDeckPath(p string) {
	if p != "" {
		if _, err := backend.FindDeck(p); err != nil {
			a.fail(err)
			return
		}
	}
	a.saveSettings(func(s *settings.Settings) { s.DeckPath = p })
}
