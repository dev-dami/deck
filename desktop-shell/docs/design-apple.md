# Deck desktop: the Apple look

Status: 2026-10-26. Superseded for layout, materials and components by
`design-glass.md`, which every theme now follows; the colours, type and
HIG notes below still hold. "Apple" was the default theme. It makes the window
look and behave like a native Mac app. Tabletop (`docs/plan.md`, section
2), T3 and the rest of the catalog stay as choices. Apple uses Tabletop's
token names, so each component reads both themes with no map.

## 1. Sources

- Apple Human Interface Guidelines (macOS): Typography, Color, Materials,
  Sidebars, Toolbars, Segmented controls, Menus, Popovers, Sheets,
  Accessibility (developer.apple.com/design/human-interface-guidelines).
- The system's dynamic colours in AppKit: `labelColor`,
  `secondaryLabelColor`, `separatorColor`, `controlAccentColor`,
  `windowBackgroundColor`, `selectedContentBackgroundColor`.
- Mac apps with a native feel: Xcode (toolbar title and subtitle, syntax
  colours), Mail and Finder (sidebar material, semibold section
  headings, grey selection), Messages (the person's bubble), Notes,
  System Settings (grouped rows, segmented controls), Spotlight and
  Raycast (the command palette), Things 3, Linear and Arc (quiet
  chrome, one accent).

What the HIG says, in short:

- The macOS body text is 13pt, the minimum is 10pt. The built-in styles:
  Large Title 26/32, Title 1 22/26, Title 2 17/22, Title 3 15/20,
  Headline 13/16 bold, Body 13/16, Callout 12/15, Subheadline 11/14,
  Footnote and Captions 10/13. macOS has no Dynamic Type.
- A sidebar has at most two levels, uses the accent on its icons, can
  hide (a toolbar button and a View menu command), and keeps critical
  actions away from its bottom edge.
- A toolbar puts the sidebar toggle and the title at the leading edge
  and the inspector toggles at the trailing edge. It prefers symbols to
  text, uses no borders around symbols, and has at most three groups.
- A segmented control switches views in a toolbar or an inspector. Its
  segments have equal widths and carry text or symbols, not both.
- Use semantic colours, not fixed values. The person's accent colour
  applies to buttons, selections and sidebar icons.
- Materials: a sidebar blends "behind window" with the desktop. Use
  vibrant colours on materials. Reduce transparency makes them solid.

## 2. Tokens

The values are in `src/renderer/styles.css` under
`[data-palette="apple"]`. The swatches in `src/renderer/themes.ts`
repeat four of them, and `themes.test.ts` checks that they agree.

### Type

| Use | Size / line | Weight | Font |
|---|---|---|---|
| UI, rows, buttons | 13px / 1.5 | 400, 500 | `-apple-system`, SF Pro; Geist elsewhere |
| Toolbar title | 13px | 600 | same |
| Toolbar subtitle, metadata | 11px | 400 | same |
| Section headings | 11 to 12px | 600, secondary label | same |
| Messages (prose) | 14px / 1.55 | 400 | same |
| Code, paths, ids | 12.5px / 1.6 | 400 | `ui-monospace`, SF Mono; Geist Mono elsewhere |

`--font-weight-semibold` is 600 in Apple (Tabletop keeps 500).

### Spacing and size

- A 4px grid. Toolbars are 52px high (`h-13`), as a Mac unified toolbar.
- Controls: 24px (`xs`), 28px (`sm`), 32px (default). Menu rows: 28px.
- Sidebar: 256px wide, rows of 36px, an 8px inset.
- Timeline: turns 20px apart, a 48rem column.

### Radii (continuous corners in spirit; CSS draws them round)

`--control-radius` 6px for buttons and fields, 5px for menu rows,
`--radius` 10px for cards and menus, 14px for the composer and
settings groups, 18px for the palette, sheets and the person's bubble.

### Separators and elevation

- Hairlines: `separatorColor`, 10% white on dark, 10% black on light.
  The strong hairline is 16%.
- `--elevation-card`: a 0.5px outline and a 1 to 3px soft shadow, for
  the composer, settings groups and push buttons.
- `--elevation-popover`: a 0.5px outline and a 32px soft shadow, for
  menus, the palette, sheets and toasts.

### Materials

- On a Mac, `BrowserWindow` has `vibrancy: "sidebar"`, a clear
  background and `visualEffectState: "followWindow"` (`src/main/main.ts`).
  The renderer marks `<html data-vibrancy>` on a Mac
  (`appearance.ts`). Apple then makes the sidebar and `body`
  transparent, so the system material shows. Every other surface keeps
  an opaque background. On other systems the sidebar is opaque.
- `.material` (menus, popovers, the palette, sheets, toasts, the
  composer's pickers, the jump pill): 78% of the surface with
  `backdrop-filter: blur(24px) saturate(1.8)`. With
  `prefers-reduced-transparency` it is opaque.
- `.scrim` (behind the palette and sheets): 22% black, no blur.

### Colour

| Token | Dark | Light | From |
|---|---|---|---|
| `--canvas` | `#1e1e1e` | `#ffffff` | window and text background |
| `--sidebar` (opaque) | `#29292b` | `#efeff1` | sidebar material without vibrancy |
| `--surface-1` | `#262628` | `#ffffff` | cards, composer |
| `--surface-2` | `#2c2c2e` | `#ffffff` | menus, popovers |
| `--grouped` | `#1e1e1e` | `#f5f5f7` | behind settings groups |
| `--bubble` | `#2f2f31` | `#ededf0` | the person's turn |
| `--text` | `#e3e3e5` | `#1d1d1f` | labelColor |
| `--text-muted` | `#a1a1a6` | `#636366` | secondaryLabelColor |
| `--text-faint` | `#8e8e93` | `#78787d` | tertiary, kept at 4.4:1 or more |
| `--hairline` | 10% white | 10% black | separatorColor |
| `--hover` / `--selected` | 6% / 10% white | 4.5% / 8% black | unemphasized selection |
| `--signal` (accent) | `#0a84ff` | `#007aff` | systemBlue |
| `--success` | `#30d158` | `#28cd41` (text `#248a3d`) | systemGreen |
| `--warning` | `#ff9f0a` | `#ff9500` (text `#c93400`) | systemOrange |
| `--destructive` | `#ff453a` | `#ff3b30` (text `#d70015`) | systemRed |
| Agents | claude `#ff9f5a`, codex `#a1a1a6`, opencode `#64d2ff`, antigravity `#bf5af2` | `#c4581b`, `#6e6e73`, `#0077a8`, `#8944ab` | system hues |

The accent picker keeps its colours and adds Graphite. "Theme" is the
many-coloured dot, as macOS shows its Multicolor accent. Syntax colours
in diffs and code follow Xcode's Default (Dark) and Default (Light).

### Motion

One curve per theme (`--ease`; Apple uses the standard ease,
`cubic-bezier(0.25, 0.1, 0.25, 1)`), 160ms for hovers, 180ms for a row
or popover that enters with a 4px rise. `data-motion="reduced"` (the
person's choice, or `prefers-reduced-motion`) turns off every animation
and transition.

## 3. Component rules

- **Toolbar.** 52px. Leading: the sidebar toggle (when the sidebar is
  hidden), then the title (13px semibold) over the subtitle (the room's
  corner index and the project, 11px). Trailing: the git split button,
  sharing, then borderless glyph toggles (terminal, diff, side panel)
  with a bezel on hover and while on.
- **Lane strip.** One 36px line under the toolbar with a hairline under
  it. Lanes that do not fit fold into "+N". It wraps only for lanes
  that never fold.
- **Sidebar.** The system material on a Mac. Semibold 11px section
  headings in the secondary colour. A grey, rounded selection with no
  accent fill. Hover actions replace the row's agent glyphs; they do not
  sit on top of them. The footer shows the connection as a dot and a
  word.
- **Composer.** One box: the text, then one bar with the menu, the mode,
  the agents, the branch, attach and send. The send button is the
  accent. The focus ring is the accent at 15%, 3px. The send hint shows
  only when the bar is wider than 46rem.
- **Timeline.** Agent prose has no container. The person's turn is a
  bubble on the right. A reply's copy, retry and continue show on hover
  at the end of its header line. The timeline fades over its last 16px.
- **Segmented controls.** A recessed track (`--secondary`), equal
  segments, the chosen one raised (`--segment`, a card shadow). Settings
  and the side panel's view switcher use them.
- **Menus and popovers.** Frosted material, a 10px radius, 28px rows.
  In Apple the row under the pointer or the keys takes the accent with
  white text.
- **Palette.** A Spotlight sheet: frosted, 18px radius, the picked row
  in the accent.
- **Sheets (dialogs).** Frosted, over a light scrim, with the focus held
  inside.
- **Settings.** Grouped rows on `--grouped`, semibold section titles.
  The theme picker shows a theme's light and dark halves.
- **Focus.** Every control has a visible `focus-visible` ring in the
  accent. Hover-only controls also show on `focus-within`.
- **Scrollbars.** In Apple a thin overlay thumb that shows while the
  pointer is over the list.

## 4. Rough edges

Found in `docs/screenshot.png` and in screenshots of every screen in
light and dark, at 1360×860 and 960×600. All are fixed.

1. The lane strip wrapped to two lines with no top padding, so its
   second line sat on the timeline's first line. Now one padded line,
   "+N" for the rest.
2. The room header was crowded and cut the project to "ain
   deck-folder-W…". Now a title over a subtitle.
3. The composer's strip hung under the box as a narrower tab. Now part
   of the box's bottom bar.
4. About 50px of blank space between turns: each reply kept an empty
   row for its hover actions. Now the actions sit on the header line.
5. A sidebar row's hover actions sat on top of its agent glyphs.
6. The sidebar footer showed "• • 1": its breakpoint was wider than the
   sidebar. Now "Connected"; the duplicate Commands button is gone.
7. Toolbars were 48px and the traffic lights were not centred in them.
   Now 52px everywhere, and the lights sit at (18, 19).
8. The theme picker showed only the dark half of a theme that follows
   light or dark, and the "Theme" accent was a letter "T".
9. The side panel's tabs, the settings toggles and the color-mode
   control each used a different pill style. Now one segmented control.
10. Every bold weight was 500, so headings did not stand out from body
    text with the system font. Apple uses 600.
11. Popovers, the palette and sheets were flat surfaces on a heavy blurred
    scrim. Now frosted material on a light scrim.
12. The timeline cut a message hard at the composer's top edge. Now a
    short fade.
13. The light theme's faint text was under 4.5:1 in places; the Apple
    faint and muted greys are 4.4:1 and 6:1 or more on their canvas.
14. The Electron screenshot showed a temporary folder name
    (`deck-folder-WcahPl`). The test now opens a folder named `deck`.

## 5. Not done

- The engine's agent glyphs (✳, >_) stay as they are; they are Deck's
  own marks, not SF Symbols.
- The renderer does not read the person's macOS accent colour
  (`systemPreferences.getAccentColor`); that needs a new host method.
  The accent picker covers it.
- Windows 11 Mica (`backgroundMaterial`) is not set.
