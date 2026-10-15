package ui

import (
	"fmt"
	"image"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/dev-dami/deck/desktop/settings"
	"github.com/dev-dami/deck/desktop/state"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// view is one frame's copy of what is shown, taken under the locks so
// layout never races the event goroutines.
type view struct {
	set      settings.Settings
	projects []projView
	proj     string
	sess     string
	recents  []string
	opening  []string
	waiting  int
	toast    string
	toastErr bool

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
	v := view{set: a.set, proj: a.proj, sess: a.sess, models: map[string][]*deckv1.ModelChoice{}}
	if time.Since(a.toastAt) < toastFor {
		v.toast, v.toastErr = a.toast, a.toastErr
	}
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
			key.Filter{Name: "F", Required: key.ModShortcut},
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
		case "F":
			gtx.Execute(key.FocusCmd{Tag: &a.searchEd})
		}
	}
}

func (a *App) layout(gtx C) D {
	paint.Fill(gtx.Ops, a.col.Bg)
	a.shortcuts(gtx)
	a.flushClipboard(gtx)
	a.mu.Lock()
	a.attach = append(a.attach, a.pendingAttach...)
	a.pendingAttach = nil
	a.mu.Unlock()

	v := a.snapshot()
	a.windowTitle(v.waiting)
	showPanel := a.page == pageChat && v.set.PanelOpen && v.session != nil

	main := func(gtx C) D {
		if a.page == pageSettings {
			return a.layoutSettings(gtx, v)
		}
		return a.layoutMain(gtx, v)
	}
	// The main row is the Stacked child sized to the window, so the toast
	// (Expanded) and every Center inside get the full size as their minimum.
	return layout.Stack{}.Layout(gtx,
		layout.Stacked(func(gtx C) D {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return a.layoutRail(gtx, v) }),
				layout.Rigid(a.vline),
				layout.Flexed(1, func(gtx C) D {
					if !showPanel {
						return main(gtx)
					}
					return a.split.Layout(gtx, main,
						func(gtx C) D { return a.layoutPanel(gtx, v) },
						a.splitHandle)
				}),
			)
		}),
		layout.Expanded(func(gtx C) D { return a.layoutToast(gtx, v) }),
	)
}

// splitHandle is the drag bar between the chat and the panel.
func (a *App) splitHandle(gtx C) D {
	w := gtx.Dp(5)
	size := image.Pt(w, gtx.Constraints.Max.Y)
	line := image.Rect(w/2, 0, w/2+gtx.Dp(1), size.Y)
	paint.FillShape(gtx.Ops, a.col.Line, clip.Rect(line).Op())
	return D{Size: size}
}

func (a *App) layoutToast(gtx C, v view) D {
	if v.toast == "" {
		return D{}
	}
	if a.click("toast-close").Clicked(gtx) {
		a.mu.Lock()
		a.toastAt = time.Time{}
		a.mu.Unlock()
	}
	gtx.Constraints.Min = gtx.Constraints.Max
	return layout.S.Layout(gtx, func(gtx C) D {
		return layout.Inset{Bottom: 96}.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(560))
			gtx.Constraints.Min = image.Point{}
			ic, c := icInfo, a.col.Accent
			if v.toastErr {
				ic, c = icWarn, a.col.Danger
			}
			return elevated(gtx, a.col.Raised, 10, layout.Inset{Top: 8, Bottom: 8, Left: 12, Right: 6}, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return icon(gtx, ic, 18, c) }),
					layout.Flexed(1, func(gtx C) D {
						return layout.Inset{Left: 10, Right: 8}.Layout(gtx, a.label(13, a.col.Fg, v.toast).Layout)
					}),
					layout.Rigid(func(gtx C) D { return a.iconBtn(gtx, "toast-close", icClose, "Dismiss", a.col.Muted) }),
				)
			})
		})
	})
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 90 {
		s = string(r[:90])
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

func clipLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… %d more lines", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

// gap is fixed space in a flex.
func gap(dp unit.Dp) layout.FlexChild {
	return layout.Rigid(func(gtx C) D { return D{Size: image.Pt(gtx.Dp(dp), gtx.Dp(dp))} })
}
