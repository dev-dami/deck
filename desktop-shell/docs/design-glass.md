# Deck desktop: Liquid Glass

Status: 2026-10-27. This replaces the look in `design-apple.md` for every
theme. The components and the motion below are the same in every look.
A look is made of aspects that mix (section 9): the colours (any theme of
the catalog, Riso's and Stitch's inks among them), the material, the
picture's process, the display face, the texture, and two knobs, corners
and translucency. Liquid Glass, the default, Riso and Stitch are presets
that set every aspect at once. The layout can mirror (section 4).

## 1. Direction

Deck runs a crew of agents in one thread. The old window was a chat app
with a sidebar. It showed what the agents said, but not what they were
doing, which agent did what, or how the work moved between them.

The new direction is **mission control for a crew**, calm and spatial:

- **See the crew.** Every agent has a hue, a status and a place on screen.
  You can tell who is working, who waits on you and who is done without
  reading.
- **Two ways to look at one thread, and its diff beside them.**
  *Conversation* (the timeline) and *Graph* (the work as an interactive
  map: agents, their sub-agents, tasks, the files they hold and edit) are
  peers that one control switches between (`ui/roomView.ts`). *Changes*
  (the diff) stays in the side panel, one click away.
- **Glass for chrome, sheets for reading.** Controls float on glass over
  the content. Text that is read at length sits on opaque sheets.
- **Intentional and quiet.** One accent, and the agents' hues as the only
  other colour. Motion only where something changed. Nothing decorative
  that does not also inform.

The reference points: macOS 26 (Tahoe) Liquid Glass for materials and
controls, Linear and Things for quiet density, Arc for a window made of
panels on a backdrop, and Figma or tldraw for a canvas you can pan and zoom.

## 2. Layers

Back to front:

1. **Backdrop.** The theme's picture printed through its process on a
   canvas behind the app (`ui/Backdrop.tsx`, section 9), over `body` in
   a deep tone of the theme's canvas. Until the picture has drawn, and
   with reduced transparency, `body` shows three soft pools of light
   instead: the accent (top left), then Claude's and Antigravity's hues.
   On a Mac with vibrancy, the desktop shows through a veil of it.
2. **Sheets** (`sheet`): opaque rounded planes for reading, such as the
   room, the side panel, Settings, Usage, Team and the inbox. They have a
   1px rim and a long soft shadow.
3. **Glass**:
   - `liquid`: regular glass, for the sidebar.
   - `liquid-clear`: barely tinted, for controls that float over a sheet
     (toolbar capsules, lane chips, the segmented track).
   - `liquid-thick`: for menus, popovers, the palette, dialogs and toasts.
     Every `.material` element gets it.
   - `liquid-tint`: the accent as glass, for the one prominent action on
     screen (send, a default button).

All four are Tailwind utilities in `src/renderer/styles.css`. Each writes
Tailwind's shadow variables, so `ring-*` and `shadow-*` still add on top.
Use them as plain classes, for example `className="liquid rounded-(--panel-radius)"`.
Do not hand-roll `backdrop-blur` or `bg-*/50` glass in a component.

With `prefers-reduced-transparency`, every glass is solid and the
backdrop is plain. The CSS does this, not the components.

## 3. Tokens

These are set in `:root`, derive from each theme's surfaces, and the
Liquid Glass theme tunes them by hand:

| Token | Use |
|---|---|
| `--backdrop`, `--ambient-1..3` | the window behind everything |
| `--glass-fill`, `-thick`, `-clear` | the glass tints |
| `--glass-blur`, `--glass-saturate` | 26px, 1.8 to 1.9 |
| `--glass-rim`, `--glass-spec`, `--glass-sheen`, `--glass-shadow` | edge, top highlight, sheen, lift |
| `--sheet-rim`, `--sheet-shadow` | sheets |
| `--panel-radius` 18 to 20px, `--sheet-radius` 14 to 16px | concentric corners |
| `--bubble` | the person's turn, the accent mixed into a surface |

Everything else (`--canvas`, `--surface-1/2`, `--text*`, `--hairline*`,
`--hover`, `--selected`, `--primary`, `--kind-*`, the syntax colours)
keeps its meaning from `design-apple.md` section 2. In Tailwind:
`bg-canvas`, `bg-surface-1`, `text-faint`, `border-hairline`,
`bg-selected` and so on.

Radii nest: window gutter 8px → panel 18 to 20px → sheet 14 to 16px →
card 12px → control capsule (full) → menu row 7px. A child's radius is
its parent's radius minus the padding between them.

## 4. Layout

```
┌ backdrop ─────────────────────────────────────────────────────────────┐
│ ┌ sidebar (liquid) ┐ ┌ room (sheet) ──────────────────┐ ┌ panel ─────┐ │
│ │ ●●●  Deck        │ │ title / project   [⌥ capsules] │ │ (sheet)    │ │
│ │ Search      ⌘K   │ │ ◉ claude-1 ◉ codex-1  (chips)   │ │ Changes    │ │
│ │ Inbox        1   │ │ [Conversation | Graph | Changes]│ │ Tasks …    │ │
│ │ ── threads ──    │ │                                │ │            │ │
│ │ ▸ Fix lockout ●● │ │   timeline or graph            │ │            │ │
│ │                  │ │                                │ │            │ │
│ │                  │ │  ╭ composer (liquid) ────────╮ │ │            │ │
│ │ ⚙ ▥ ⚇   ● Live  │ │  ╰───────────────────────────╯ │ │            │ │
│ └──────────────────┘ └────────────────────────────────┘ └────────────┘ │
└───────────────────────────────────────────────────────────────────────┘
```

- The shell has an 8px gutter (`p-2 gap-2`) showing the backdrop.
- **Sidebar**: 256px of `liquid`, `rounded-(--panel-radius)`. On a Mac the
  traffic lights sit inside it, at (18, 19). Its first row is 36px high, so
  the lights centre on it. Elsewhere that row is 44px.
- **Room**: a `sheet` with `rounded-(--sheet-radius)` and
  `overflow-hidden`. The terminal drawer lives inside it, at the bottom.
- **Toolbar** (inside the room sheet, 48px, no background): the title over
  the subtitle at the leading edge. At the trailing edge, controls grouped
  in `liquid-clear` capsules (`rounded-full p-0.5`), at most three groups.
- **View switcher**: a segmented control (`kit/segmented.tsx`) with
  Conversation and Graph, centred in the toolbar. The room holds the view;
  `RoomHeader` takes `view` and `onView`.
- **Lane strip**: one line of agent chips. Each chip is a `liquid-clear`
  capsule with the agent's glyph in its hue, its name, and a status dot
  that pulses while it works.
- **Composer**: a `liquid` capsule (22px radius) that floats 12px above the
  sheet's bottom edge. The timeline runs under it and shows through,
  blurred. The room measures the composer's height into `--composer-h` on
  its wrapper (a `ResizeObserver`). The timeline pads its bottom by that
  amount plus 1.5rem, and the jump pill sits above it.
- **Side panel**: a `sheet` beside the room, 480px, rounded. Below 1100px
  it floats over the room as `liquid-thick`.
- **Pages** (Settings, Usage, Team, Inbox): a sheet in the room's place.
  Groups inside are `rounded-(--sheet-radius) bg-surface-1` with a rim.
- **Welcome and Connect**: no sheet. Glass cards float straight on the
  backdrop, which is the first impression and should feel like it.

**Moving things.** Settings > Appearance > Layout (`ui/LayoutPicker.tsx`)
draws each choice as the window it makes. The sidebar sits on the left or
the right (`sidebarSide`); the side panel docks on the side opposite it,
beside it (between it and the room), or floats over the room away from it
at any width (`panelDock`). The Shell writes `data-sidebar` and
`data-dock` on `.shell`, and styles.css orders the parts (`order`, then
`row-reverse` for a right sidebar), so the DOM and the Tab order stay
sidebar, room, panel. The sidebar's inner edge is a grip
(`role="separator"`): drag it to resize (208 to 420px, `--sidebar-w` on
`<html>`), the arrow keys step it by 16px, a double click resets it, and
dragged across most of the window it moves the sidebar to the other side.
Compact density (`data-density`) tightens Tailwind's `--spacing` by a
tenth; the type keeps its size and the Mac's title row stays 36px. On a
Mac whichever part sits at the top left clears the traffic lights:
the sidebar itself, or the room's toolbar or the panel's tabs through the
Shell's `data-lights`. A right sidebar sees the backdrop mirrored, so the
band held back for it follows it.

## 5. Components

- **Buttons** (`kit/button.tsx`): capsules. `default` is `liquid-tint`,
  `outline` is `liquid-clear`, and `ghost` and `ghost-muted` show
  `bg-hover` on hover and `bg-selected` while pressed. Icon buttons are
  circles. Only one `default` button per view.
- **Segmented** (`kit/segmented.tsx`): `segmentTrack()` and
  `segmentItem({ on })`. Use it for every switcher: panel tabs, Settings
  and view modes.
- **Menus and popovers** (`kit/menu.tsx`): thick glass, 14px radius, 6px
  padding, 28px rows with a 7px radius. The row under the pointer is an
  accent capsule.
- **Dialogs** (`kit/dialog.tsx`): thick glass, 22px radius, over a light
  scrim.
- **Fields**: `fieldClass`, a recessed well with a 10px radius.
- **Empty states** (`kit/empty.tsx`): a duotone icon (no circle or badge
  around it), a title in the foreground colour, a hint in faint, and one
  action.
- **Cards inside a sheet**: `rounded-xl bg-surface-1` with
  `ring-1 ring-hairline`, and no shadow.
- **Status**: a dot in `bg-success`, `bg-warning`, `bg-destructive` or
  `bg-signal`. "Working" pulses with the stepped `animate-deck-pulse`.
  "Needs you" uses the warning colour and a `ShieldWarning` icon.
- **Agents**: the glyph from `kindMark()`, in `agentColour()`, always the
  same hue for the same agent, everywhere.

### Icons

Phosphor (`src/renderer/ui/icons.tsx`, the only place that imports
`@phosphor-icons/react`). The default weight is `regular`, from the
`IconContext` in `main.tsx`. A control that is on (an active nav row, a
pressed toggle) can pass `weight="fill"`. Empty states and large hero
icons use `weight="duotone"`. To add an icon, add it to `icons.tsx`.

### Type

The same as `design-apple.md`: 13px UI, 11px metadata, 14px prose, 12.5px
mono. Headings use `text-balance`, prose `text-pretty`, and data
`tabular-nums`.

### Not AI-template

The first version of the welcome screen read as AI-generated. These are
the tells, and none of them belongs in Deck:

- the centred stack: a logo tile, a headline, a grey subtitle and two
  equal cards, floating small in an empty window;
- an icon in a circle heading every card or row;
- a glow on a button, or an aurora in the corner;
- a pill badge for every state;
- a rim, a border and a shadow on every container, so nothing leads;
- generic copy that could sit on any product.

Instead: hierarchy through type, alignment and space. Glass only where
something floats. Agents shown as Deck shows them (a glyph in its hue,
live status). Copy in Deck's own plain voice.

## 6. Motion

- 160ms for hovers, 200 to 240ms for things that enter, on `--ease`. Only
  `transform` and `opacity`. Never animate blur or a glass surface's size.
- A thing that enters rises 4px and fades in (`animate-enter`).
- Live state (a working agent, data moving on a graph edge) uses stepped
  or dashed animations. They pause off screen, and under
  `[data-motion="reduced"]` they stop.

## 7. The Work Graph

The graph is an interactive map of one thread's work. It is new, and it
lives in `src/renderer/ui/graph/` with its model in
`src/renderer/graph.ts`.

**Nodes:**

| Node | From | Shows |
|---|---|---|
| You | always | the root |
| Agent | the roster (`AgentLine`) and crew nodes (`AgentNode`: `parent`, `depth`, `status`, `model`, `contextTokens/Window`, `costUsd`, `leases`) | glyph and hue, name, model, a status ring (pulses while working), a context ring, the running tool (`RunningTool`) |
| Task | the board (`BoardTask`: `owner`, `deps`, `status`, `files`) | title, status, owner |
| File | changed files (`ChangedFile`), leases (`leasePaths`), task files | path, +/−, who holds it |

**Edges:** you → top-level agents; parent → sub-agent (spawned); task →
task (`deps`); agent → task (owns); agent → file (edited or holds, the
latter dashed).

**Layout:** a deterministic layered DAG from left to right (you, agents by
depth, tasks, files), ordered within each layer by barycentre to cut
crossings, then by swapping neighbours while that undoes one. A crossing
among cards weighs more than one among lanes. An edge that spans more than
one layer runs through a lane in each layer it crosses, placed like a card,
so it passes between the cards and never behind one; edges of one kind from
one node share their lanes until they part. A tall, narrow canvas (the side
panel) lays the same layers out from top to bottom, with narrower cards. It
is a pure function in `graph.ts`, with vitest tests. Do not add a graph
library.

**Rendering:** one SVG for the edges (smooth curves, straight along a
lane, one pixel at any zoom). The nodes are flat HTML cards placed over it,
as a debugger's graph draws them: `bg-surface-1`, a 6px radius and a 1px
rim (`--graph-rim`), no glass, glow or shadow. Type carries the hierarchy:
an agent is its glyph in its hue, its name, its model and context, and the
tool it runs under a hairline; a task is a small mark (check, cross, dot)
and its title; a file is its name in mono and its +/−. Picked or focused,
a card takes the accent outline. The canvas has a dot grid that is barely
there (`--graph-dot`).

**Colour:** carries meaning only. At rest every edge is a neutral hairline
(`--graph-edge`). The edge to a file an agent edits now takes that agent's
hue and flows. While a node is hovered or picked, its edges take the hue of
the agent they belong to and everything else fades. A lease is dotted.

**Interaction:**

- Pan by dragging the background. Zoom with the wheel or a pinch, around
  the pointer, from 0.4× to 2×.
- Zoom −, + and Fit controls in a `liquid-clear` capsule in a corner. Fit
  is the default.
- Hover a node: its neighbourhood stays bright and the rest dims to 35%.
- Click an agent: select it and show a detail card (status, model,
  context, cost, running tool, leases) with actions:
  - **Show its messages**: filter the conversation to its lane.
  - **Steer**.
  - **Stop**.
- Click a file: open its diff in Changes. Click a task: open Tasks. A
  lease's pattern (a path with `*`) has no diff, so it picks its holder.
- Keyboard: Tab or the arrow keys move between nodes (along a layer, or
  to the nearest node in the next), Enter activates, `+` and `-` zoom, `0`
  fits, Escape closes the detail card.
- A wide canvas shows the counts and a key to the lines (top left) and,
  while part of the graph is out of sight, a minimap.
- Live: a working agent's ring pulses (stepped), and the edge to the file
  an agent is editing flows (an animated dash). Both stop under reduced
  motion.

**Where it shows:** in the room, as the Graph view beside Conversation,
where the composer still floats over it. It also shows in the side panel
as a "Graph" tab (a new `Panel`, `"graph"`), `compact`. The props are in
`ui/graph/WorkGraph.tsx`.

## 8. Working rules

- Tailwind utilities and the kit first. New global CSS only for things a
  utility cannot express. Then it goes in `styles.css`, in a section with
  a comment saying why.
- Keep every role, label, class hook and piece of text the e2e tests
  select by (`.title`, `.project`, `.panel-toggles`, `.composer`,
  `.session`, `.msg`, `[data-agent]`, the aria names), or update the test
  in the same change.
- Checks: `npm run lint`, `npx vitest run`, then
  `PW_CHROMIUM=/opt/pw-browsers/chromium PORT=<free> npx playwright test --grep-invert "electron|packaged"`,
  and `SHOTS=<dir> PORT=<free> PW_CHROMIUM=… npm run shots` to look at the
  result in dark and light. `PALETTE=<theme id>` shoots another theme.

## 9. Art directions

One picture, three processes. A theme that is an art direction prints a
public-domain picture (`src/renderer/art/`, credited in its
`ATTRIBUTION.md`) through its own printing process, and brings its own
material, display face and texture. The layout and the components stay
the ones above.

| | Liquid Glass (`glass`) | Riso (`riso`) | Stitch (`stitch`) |
|---|---|---|---|
| Process | Atkinson dither in 2px cells, a duotone of dusk navy `#1d2240` and peach `#f3c9a8` | a round-dot halftone at 15° in one fluorescent ink, `#ff48b0`, with black at 45° on paper only | cross-stitch, one stitch a 10px cell, on even-weave cloth |
| Picture | Adolphe Braun's Alps, about 1870 | Karl Blossfeldt's *Allium oreophilum*, 1928, with solid blocks of the spot colour: a disc behind its head and a bar at its foot | O'Sullivan's *Black Cañon*, 1871, in three tonal flosses, with a hand of cards and a pair of dice over it |
| Texture | the Deck mark as a faint watermark in the tone | black stock in dark, paper in light; fibre in the stock, specks where the drum ran dry | linen in light, denim (with its twill) in dark |
| Material | glass (section 2) | paper and vellum: no blur, square corners, a rule and a 3px misregistered shadow in the spot colour | felt: opaque, with a running stitch just inside each panel's edge |
| Display face | Inter Tight | Space Grotesk; Martian Mono for code | Pixelify Sans, headings only |

Apple, Tabletop, T3 and the catalog themes are colours: through the
dither, the same picture in two of their own colours. Tokyo Night prints
the Hubble Deep Field in its palette.

**Aspects that mix.** The columns above are presets. Each row is an
aspect of its own (`themes.ts`), an attribute on `<html>` that
`appearance.ts` writes and `styles.css` reads, and any one goes with any
other:

| Aspect | Attribute | Choices |
|---|---|---|
| Colours | `data-palette` | every theme of `THEMES`; its block in styles.css sets colours only |
| Material | `data-material` | glass (the default), paper, felt, plain; each derives its tokens from the theme's surfaces, text and accent (paper's shadow is `--spot`, or the accent) |
| Picture | `data-process` | dither, halftone, stitch, none; a theme through a process not its own prints in its colours (`inks.ts`, `risoIn`, `stitchIn`); none keeps the plain ground |
| Display face | `data-face` | Inter Tight, Space Grotesk (with Martian Mono), Pixelify Sans, the UI's own |
| Texture | `data-texture` | none, paper fibre, felt |
| Corners | `data-corners` | auto (the material's), square, soft, round |
| Translucency | `data-translucency` | auto, clear, frosted, solid; glass only, and Reduce transparency still wins |

A look that matches no preset is Custom, and says which preset it was
mixed from (`presetOf`, `nearestPreset`). Prefs saved before the aspects
named one theme; it loads as the aspects it stood for (`lookFromPalette`):
Riso and Stitch as their presets, any other theme as Liquid Glass in its
colours.

**Where the picture shows.**

- **Behind the glass chrome.** In the shell, the backdrop shows behind the
  sidebar and nowhere else: the gutters between the panels are the plain
  ground (the stitch's cloth weave, in CSS), so no slice of a block, a
  card or a dither runs down them. The tone behind the sidebar is held
  back, as a photographer holds back a sky, so the sidebar stays calm. The
  riso keeps a 20px press margin and its blocks follow the subject, right
  of the sidebar; the stitch's cards and dice keep off the sidebar's band.
- **On the first screens.** The Welcome and the connect screen print the
  picture at the size of their plate (`ui/ArtFigure.tsx`), as a print on
  the page, with its credit under it. Liquid Glass hangs its true duotone
  in either mode. The window's backdrop hides there, so type never sits on
  the picture.
- **Never in the room.** The room's sheet, the chat, the composer and the
  diff stay opaque and plain. The pages' small plates (empty Inbox, Team,
  Usage) take each direction's inks (`--plate-lo`, `--plate-hi`).

**How it is drawn.** `backdrop/` holds the processes: `core.ts` is the
maths with no DOM (dither, screen, blocks, chart, layout; `core.test.ts`),
`scene.ts`, `halftone.ts` and `stitch.ts` draw, `inks.ts` gives each
theme's art and `usePrint.ts` prints it in a worker (`worker.ts`), once
per theme, mode, picture or size, never per frame. A print takes 40 to
240ms at 1440×900 at 2x, off the page's thread.

**Choosing.** Settings > Appearance (`ui/LookEditor.tsx`) opens on the
window in miniature, live, then the three presets as prints of their own
process (`ui/ThemeCard.tsx`), then a row for each aspect, drawn as what
it is: colours as chips of their swatches, each material and texture as
a swatch made of it (a `.look` element carries the attributes, so the
CSS blocks apply to it as to `<html>`), each process as a print of the
picture through it, each face in itself. Under them, the picture
(`ui/WallpaperPicker.tsx`): the look's own, the rest of its process's set
(`backdrop/pictures.ts`, `SETS`), or the person's own, kept in this
window's IndexedDB and never sent anywhere. Any picture prints through
any process.

**Adding a picture.** Put a public-domain file in `art/`, credit it in
`ATTRIBUTION.md`, add it to `PICTURES` with its crop and subject, and to a
set. Ship JPG or PNG: app:// names them (`src/main/mime.ts`), and it sends
`nosniff`.
