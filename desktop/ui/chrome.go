package ui

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/component"
)

// The widget kit every screen draws with: labels scaled to the chosen text
// size, surfaces, icon buttons with tooltips (gio-x TipArea), chips and
// avatars.

func withAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

func (a *App) label(size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	l := material.Label(a.th, a.sp(size), txt)
	l.Color = c
	return l
}

func (a *App) bold(size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	l := a.label(size, c, txt)
	l.Font.Weight = font.SemiBold
	return l
}

func (a *App) line(size unit.Sp, c color.NRGBA, txt string) layout.Widget {
	l := a.label(size, c, txt)
	l.MaxLines = 1
	l.Truncator = "…"
	return l.Layout
}

func fillRR(gtx C, c color.NRGBA, radius unit.Dp) {
	defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(radius)).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

// surface draws w on a rounded background.
func surface(gtx C, bg color.NRGBA, radius unit.Dp, in layout.Inset, w layout.Widget) D {
	return layout.Background{}.Layout(gtx,
		func(gtx C) D { fillRR(gtx, bg, radius); return D{Size: gtx.Constraints.Min} },
		func(gtx C) D { return in.Layout(gtx, w) },
	)
}

// outlined draws w inside a 1dp border.
func outlined(gtx C, border, bg color.NRGBA, radius unit.Dp, in layout.Inset, w layout.Widget) D {
	return layout.Background{}.Layout(gtx,
		func(gtx C) D {
			fillRR(gtx, border, radius)
			inner := image.Rectangle{Min: image.Pt(gtx.Dp(1), gtx.Dp(1)), Max: gtx.Constraints.Min.Sub(image.Pt(gtx.Dp(1), gtx.Dp(1)))}
			defer clip.UniformRRect(inner, gtx.Dp(radius)-gtx.Dp(1)).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, bg)
			return D{Size: gtx.Constraints.Min}
		},
		func(gtx C) D { return in.Layout(gtx, w) },
	)
}

// elevated draws w on a surface with a soft shadow.
func elevated(gtx C, bg color.NRGBA, radius unit.Dp, in layout.Inset, w layout.Widget) D {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(component.Shadow(radius, 6).Layout),
		layout.Stacked(func(gtx C) D { return surface(gtx, bg, radius, in, w) }),
	)
}

func icon(gtx C, ic *widget.Icon, size unit.Dp, c color.NRGBA) D {
	px := gtx.Dp(size)
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	return ic.Layout(gtx, c)
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

func (a *App) tip(key string) *component.TipArea {
	t := a.tips[key]
	if t == nil {
		t = new(component.TipArea)
		a.tips[key] = t
	}
	return t
}

// iconBtn is a flat icon button with a tooltip.
func (a *App) iconBtn(gtx C, key string, ic *widget.Icon, tip string, c color.NRGBA) D {
	btn := a.click(key)
	return a.tip(key).Layout(gtx, component.DesktopTooltip(a.th, tip), func(gtx C) D {
		return material.Clickable(gtx, btn, func(gtx C) D {
			bg := color.NRGBA{}
			if btn.Hovered() {
				bg = a.col.Hover
			}
			return surface(gtx, bg, 6, layout.UniformInset(5), func(gtx C) D { return icon(gtx, ic, 18, c) })
		})
	})
}

// roundBtn is the filled circular button of the composer.
func (a *App) roundBtn(gtx C, key string, ic *widget.Icon, bg color.NRGBA) D {
	btn := a.click(key)
	return material.Clickable(gtx, btn, func(gtx C) D {
		if btn.Hovered() {
			bg = withAlpha(bg, 0xd8)
		}
		return surface(gtx, bg, 17, layout.UniformInset(8), func(gtx C) D { return icon(gtx, ic, 18, a.col.OnAccent) })
	})
}

// button is a text button, filled (primary) or quiet.
func (a *App) button(gtx C, key, txt string, ic *widget.Icon, primary bool) D {
	btn := a.click(key)
	return material.Clickable(gtx, btn, func(gtx C) D {
		bg, fg := a.col.Raised, a.col.Fg
		if primary {
			bg, fg = a.col.Accent, a.col.OnAccent
		}
		if btn.Hovered() {
			bg = withAlpha(bg, 0xd0)
			if !primary {
				bg = a.col.Hover
			}
		}
		return surface(gtx, bg, 8, layout.Inset{Top: 7, Bottom: 7, Left: 12, Right: 12}, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					if ic == nil {
						return D{}
					}
					return layout.Inset{Right: 6}.Layout(gtx, func(gtx C) D { return icon(gtx, ic, 16, fg) })
				}),
				layout.Rigid(a.bold(13, fg, txt).Layout),
			)
		})
	})
}

func (a *App) dangerButton(gtx C, key, txt string) D {
	btn := a.click(key)
	return material.Clickable(gtx, btn, func(gtx C) D {
		bg := a.col.Danger
		if btn.Hovered() {
			bg = withAlpha(bg, 0xd0)
		}
		return surface(gtx, bg, 8, layout.Inset{Top: 7, Bottom: 7, Left: 12, Right: 12}, a.bold(13, a.col.OnAccent, txt).Layout)
	})
}

// chip is a small pill; on fills it with the accent.
func (a *App) chip(gtx C, key, txt string, on bool) D {
	btn := a.click(key)
	return material.Clickable(gtx, btn, func(gtx C) D {
		bg, fg := color.NRGBA{}, a.col.Muted
		switch {
		case on:
			bg, fg = withAlpha(a.col.Accent, 0x30), a.col.Accent
		case btn.Hovered():
			bg, fg = a.col.Hover, a.col.Fg
		}
		return outlined(gtx, a.col.Line, bg, 12, layout.Inset{Top: 4, Bottom: 4, Left: 10, Right: 10}, a.line(12, fg, txt))
	})
}

// segmented is a row of chips of which one is on.
func (a *App) segmented(gtx C, key string, opts [][2]string, cur string, pick func(string)) D {
	kids := make([]layout.FlexChild, 0, len(opts))
	for i, o := range opts {
		k := key + ":" + o[0]
		if a.click(k).Clicked(gtx) && o[0] != cur {
			pick(o[0])
		}
		kids = append(kids, layout.Rigid(func(gtx C) D {
			in := layout.Inset{}
			if i > 0 {
				in.Left = 6
			}
			return in.Layout(gtx, func(gtx C) D { return a.chip(gtx, k, o[1], o[0] == cur) })
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, kids...)
}

// rowBtn is a full-width list row, highlighted when on or hovered.
func (a *App) rowBtn(gtx C, key string, on bool, w layout.Widget) D {
	btn := a.click(key)
	return material.Clickable(gtx, btn, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		bg := color.NRGBA{}
		switch {
		case on:
			bg = a.col.Selected
		case btn.Hovered():
			bg = a.col.Hover
		}
		return surface(gtx, bg, 8, layout.Inset{Top: 7, Bottom: 7, Left: 10, Right: 8}, w)
	})
}

func (a *App) field(gtx C, ed *widget.Editor, ic *widget.Icon, hint string) D {
	return outlined(gtx, a.col.Line, a.col.Field, 8, layout.Inset{Top: 8, Bottom: 8, Left: 10, Right: 10}, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				if ic == nil {
					return D{}
				}
				return layout.Inset{Right: 8}.Layout(gtx, func(gtx C) D { return icon(gtx, ic, 16, a.col.Muted) })
			}),
			layout.Flexed(1, func(gtx C) D {
				e := material.Editor(a.th, ed, hint)
				e.TextSize = a.sp(13)
				e.HintColor = a.col.Muted
				return e.Layout(gtx)
			}),
		)
	})
}

// avatar is an agent's round badge: its kind's tint with its mark.
func (a *App) avatar(gtx C, tint color.NRGBA, mark string, size unit.Dp) D {
	px := gtx.Dp(size)
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	fillRR(gtx, tint, size/2)
	return layout.Center.Layout(gtx, a.bold(unit.Sp(float32(size)*0.5), a.col.OnAccent, mark).Layout)
}

// column centres w at most width wide, the reading width of the chat.
func column(gtx C, width unit.Dp, w layout.Widget) D {
	mx := gtx.Dp(width)
	if gtx.Constraints.Max.X <= mx {
		return w(gtx)
	}
	pad := gtx.Metric.PxToDp((gtx.Constraints.Max.X - mx) / 2)
	return layout.Inset{Left: pad, Right: pad}.Layout(gtx, w)
}
