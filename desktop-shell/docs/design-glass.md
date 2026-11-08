# Deck desktop: Liquid Glass

Status: 2026-11-07. This replaces the look in `design-apple.md` for every
theme. The components and the motion below are the same in every look.
A look is made of aspects that mix (section 9): the colours (any theme of
the catalog, Riso's and Stitch's inks among them), the material, the
picture's process, the display face, the texture, and two knobs, corners
and translucency. Horizon (section 10), the default, its sisters Aurora,
Dune and Nebula, Liquid Glass (section 11), Riso and Stitch are presets
that set every aspect at once. The layout can
mirror (section 4).

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
│ │ ●●●   ▭1 ⌕ ✎ ▯   │ │ title / project   [⌥ capsules] │ │ (sheet)    │ │
│ │ All projects ▾   │ │ ◉ claude-1 ◉ codex-1  (chips)   │ │ Changes  + │ │
│ │ deck 3           │ │ [Conversation | Graph | Changes]│ │ Tasks …    │ │
│ │ ● Docs pass  12h │ │                                │ │            │ │
│ │ ▢ Fix lockout    │ │   timeline or graph            │ │            │ │
│ │                  │ │                                │ │            │ │
│ │                  │ │  ╭ composer (liquid) ────────╮ │ │            │ │
│ │ ⚙ ▥ ⚇ ◔ ⊘     ● │ │  ╰───────────────────────────╯ │ │            │ │
│ └──────────────────┘ └────────────────────────────────┘ └────────────┘ │
└───────────────────────────────────────────────────────────────────────┘
```

- The shell has an 8px gutter (`p-2 gap-2`) showing the backdrop.
- **Sidebar**: 256px of `liquid`, `rounded-(--panel-radius)`. On a Mac the
  traffic lights sit inside it, at (18, 19). Its first row is 36px high
  everywhere, so the lights centre on it; it holds the few glyphs the
  sidebar keeps at hand (section 13). Or the threads run as a 40px bar
  across the top (`sidebarSide: "top"`, `ui/ThreadBar.tsx`).
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
- **Pages** (Settings, Usage, Team, Inbox): a sheet in the room's place,
  or panes at the side of the conversation (section 13). Groups inside are
  `rounded-(--sheet-radius) bg-surface-1` with a rim.
- **Welcome and Connect**: no sheet. Glass cards float straight on the
  picture, which is the first impression and should feel like it (section
  12, "The first screens").

**Moving things.** Settings > Appearance > Layout (`ui/LayoutPicker.tsx`)
draws each choice as the window it makes. The sidebar sits on the left or
the right, or the threads run in a bar across the top (`sidebarSide`:
`"left" | "right" | "top"`); the side panel docks on the side opposite it,
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
band held back for it follows it. With the top bar `.shell` is a column
(the bar, then the room), the panel docks as for a left sidebar (opposite
on the right, beside on the left), the width row and the grip go away
(the width waits for the column to come back), the bar clears the traffic
lights itself (78px at its start), and the picture's clip follows it:
behind the bar's glass alone, or in smoked glass the whole room under the
bar (`--topbar-h`). The Pages row (`pages`: `"full" | "side"`) says where
the sidebar's Settings, Usage, Team and Inbox buttons open them.

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
- **Actions** (`actions.ts`, the timeline): an agent's run of tool calls
  folds to one row, "◆ @ada · 14 actions — read 6 files, ran 3
  commands", in its hue. While a call runs, the glyph is a spinner and the
  row names the call; a failed call marks it. It opens to the calls, each
  of which opens to its output. Deck's result messages fold into their
  calls, and one agent's run of messages reads as one turn.

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

**History:** a "Now | History" switch (top right) turns the graph from
one thread now to every thread's work over time (`workHistory.ts`,
`ui/graph/History.tsx`). Each thread is a band of lanes (you, each agent,
its tasks), stacked in the order the threads began; along a lane, in time
order, the person's prompts, the agents' replies and their runs of tool
calls (one node per run, as the timeline groups them, opening in place to
the calls). Edges: a prompt asks the agents it names (a line down to the
reply); an agent hands off to one it names (dashed) or asks it for a
review (finer dashes); a task joins its owner and reviewer; a run reads
(dotted) or edits files, which sit in one band under all the threads.
Columns pack: work side by side in two lanes shares one. Filters over the
top left: threads, agents, a time range, and the kinds of node; "Load full
history" reads every thread's older pages. A click jumps to that message
in the conversation, opening its thread. It stays smooth when large: past
240 nodes only those near the view draw, below 35% zoom the cards drop
their text and read as blocks of their hue, and past 400 nodes an agent's
work between two prompts folds into one node that opens.

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
  backdrop's picture themselves (`ui/ArtFigure.tsx`), full bleed behind
  their glass, with its credit on a chip in the corner. Liquid Glass hangs
  its true duotone in either mode. The window's backdrop hides there. The
  type sits on a scrim, not on the bare picture (section 12, "The first
  screens").
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

## 10. Horizon

Horizon is the default look since 2026-11-07. Where Liquid Glass floats
panels on a backdrop, Horizon is a dark window with one picture in it: a
flush sidebar, and the room open onto a planet's lit limb, printed in red,
green and blue dots. The new-thread screen is its home: the composer and a
few cards of smoked glass over the picture.

| Aspect | Horizon |
|---|---|
| Colours (`horizon`) | near-black planes (`--canvas` `#111113`, `--sidebar` `#141415`), quiet greys, one soft blue (`#5b8def`); in light, white planes and a deeper blue |
| Material (`smoke`) | smoked glass: dark, 12px blur, a 9% rim, no sheen; 10 to 12px corners |
| Process (`spectrum`) | each of red, green and blue dithered on its own into four steps, at 1.5px, half Bayer and half noise, with a faint floor so the shadows speckle (`backdrop/spectrum.ts`) |
| Picture | *Horizon*, drawn for Deck by `scripts/horizon-art.py`: space and blue air above the limb, a white-gold line of sun on it, the night side lit orange and red below |
| Face | the UI's own (Geist) |

**The window.** No gutter: the sidebar runs flush to the window's edge, an
opaque column of `--sidebar` with a hairline on its inner edge. The
picture fills the rest of the window (`.backdrop`'s clip follows
`--sidebar-w`), behind the room itself.

**Veils, not sheets.** The room's panes and the pages are clear, with a
veil under their content (`--room-veil`, the canvas at 86% and a 22px
blur, as a `::before` layer, so the composer's and the menus' own glass
still reach the picture). In a thread the picture is a soft glow behind
the words, never under them. The new-thread screen has no veil.

**Home.** Under the composer (`ui/Home.tsx`), five cards of `liquid`
glass on a four-column grid, two on a narrow pane:

| Card | Shows | From |
|---|---|---|
| Activity | 24 weeks of days, one blue from quiet to full | turns per day (the usage record's heat); a paired Deck counts threads by the day they were last touched |
| Recent threads | the three last touched, with a status dot and how long ago | the thread list |
| Usage | the plan window nearest its limit and when it resets, or the last 30 days' spend | the usage record |
| In progress | threads working (a pulsing dot) or waiting on you (the shield) | the thread list |
| This month | threads and turns so far, against last month | the thread list and the usage record |

A paired Deck keeps no usage record here, and the Usage card says so. The
question over the composer keeps its words, with the Deck mark before it
as a wordmark.

**Light.** The paper is the dots' peak, so bare paper prints clean and the
limb is a pastel arc of dots on it.

**Sisters.** Aurora (green and violet curtains over a ridge, a green
accent), Dune (sand ridges under a low sun, amber) and Nebula (lit gas
over deep stars, violet) are Horizon's window over a picture of their own,
drawn by `scripts/scene-art.py`. No two looks print the same picture
(`SPECTRUM_PICTURES` in `backdrop/inks.ts`, and its test).

**Choosing.** Each preset's card in Settings > Appearance is its picture
with the window in miniature over it, in its own colours (`MiniWindow` in
`ui/ThemeCard.tsx`): the flush sidebar, the composer and the home's cards
for smoked glass; the floating panels, made of the material, for the
others. The live preview over the cards draws the same home for a smoked
look (`SmokePreview` in `ui/LookEditor.tsx`).

**Agents' names.** The window shows an agent as people say it: "Claude",
and "Claude 2" for the second of its kind (`agentName.ts`), whatever its
id ("claude", "claude2", or "claude-1" from an older Deck). The id stays
what @mentions, lanes, tests and the engine use.

## 11. Liquid glass

The Liquid Glass material bends what is behind it, as a thick pane does at
its rim, and moves like a liquid.

**The lens** (`liquidGlass.ts`). Each glass panel (`.liquid`,
`.liquid-thick`, `.liquid-clear`, `.material`) gets an SVG filter drawn for
its own size and corner radius: a displacement map that is flat in the
middle and, across a rim up to 32px wide, reads the backdrop from further
inside, most at the edge (a circle's profile, 1 − √(1 − t²)). Red, green
and blue bend by 110%, 100% and 90%, so the rim splits the faintest
spectrum. CSS reads the filter from `--lens` (an unregistered-inherit
`@property`), before the blur: `backdrop-filter: var(--lens,) blur()
saturate()`. A panel the runtime has not reached, and every panel with
Reduce transparency, is plain frosted glass. Filters are cached per size,
96 at most; a panel that is resizing waits 90ms to settle first.

**The material.** Clearer than before (a 30% tint, a 4px blur, 18px for
menus and dialogs, 14px for the sidebar over the picture), with light on
the top-left edge, fainter on the bottom-right, a glow inside the upper
lip and a shade in the lower (`--glass-inset`).

**Motion.** Only scale and opacity move, on `--spring` (a damped spring as
`linear()`), and reduced motion stops all of it.

- The segmented controls' pill is one shape the track draws
  (`.seg-track::before`). Picking another segment stretches it across both,
  squashing it a little, and it settles on the new one with a spring. This
  is in every look.
- In Liquid Glass, a glass button gives under a press (0.9) and springs
  back; the send and tinted buttons swell a little under the pointer.
- In Liquid Glass, menus, popovers, dialogs and toasts bloom open from a
  drop (`liquid-bloom`): squashed, a small overshoot, then still.

## 12. The mode menu, the lead's control, the effort dial and the first screens

**The mode menu** (`ui/ModePicker.tsx`). The session's mode (Plan, Ask,
Yolo) is a word, not a dial: a pill in the composer's bar with the mode's
icon, its name and a chevron (`.mode-picker`, `data-mode`). A click opens
a `Popover` of `PickRow`s, the same rows and keys as the agent picker:
each mode's icon, name and what it does, the current one checked. Yolo
asks first; a room in yolo keeps the rainbow border on the pill, and the
amber flash (`[data-yolo-flash]`) stands in for the shockwave.

**The lead's control** (`ui/LeadPicker.tsx`, "The lead's control" in
styles.css), after T3 Code's single model-and-effort control
(`apps/web/src/components/chat/ComposerModelEffort.tsx` there). The agent
the person talks to and its effort are one capsule on a hairline
(`.lead-control`; clear glass in Liquid Glass): the agent's chip (its mark
in its hue, its name, the model when the bar has room, the status dot)
and, after a hairline, the effort dial. There is no chevron.

- A new agent or model slides up into the chip (`.lead-swap`, 460ms on
  `--spring`, from 60% below, faded and a little blurred), and the
  capsule eases from its old width to its new one on the same spring (a
  Web Animation, clipped while it runs).
- The chip opens the agent picker, which rises out of the bar from the
  chip's corner (`.lead-pop`: from 94% and 6px lower, on the spring; Liquid
  Glass keeps its bloom).
- In the agent picker the model's efforts dock beside the models, as T3
  Code docks its reasoning column: a 40px dial that turns to the level
  picked (drag it or scroll over it), then each level by name (Default,
  None, Minimal, Low, Medium, High, Extra high, Max). With a docked column
  the models are one column; without one, two.

**The effort dial** (`ui/EffortDial.tsx`, the geometry in `effort.ts`, "The
effort dial" in styles.css). The lead's reasoning effort shows with no
word, as a dial attached to the end of the chip:

- A 20px ring of segments round a flat cap. The ring sweeps 270°, its gap
  at the bottom; one segment per level the model takes (the catalog's
  list, lightest first; an off level such as `none` is no segment; seven
  at most), 14° apart, 1.75 wide on an 8.25 radius.
- Segments are lit up to the level in the agent's hue (`--hue`, else
  `--primary`); the rest are the text's colour at 20% (16% in light). The
  cap is the text at 9% with a `--hairline-strong` rim; its pointer is the
  text's colour and stands at the middle of the level's segment. The
  agent's own level (`""`, `data-auto`) rests the pointer in the gap, faint,
  over a ring at 34%: a dial at rest, not "off". An off level rests it just
  before the first segment.
- Turning spins the cap (`rotate`, 680ms on `--spring`, so it overshoots a
  little and settles) and lights or darkens the segments one after another
  in the direction it turned (`--d`, 55ms apart), so the light follows the
  pointer. Default to Max is a long clockwise turn round the gap. Reduced
  motion holds it still.
- Drag it (right or up turns it up, 14px a level), scroll over it (a
  wheel's notch a level) or use the arrows while it has the focus (Home
  rests it at the default, End turns it all the way). It shows the level at
  once and applies it (`setModel` with the lead's model) when the drag ends
  or once the wheel and keys have rested 450ms. A click opens the levels as
  a short column ("Effort" over the model's name): Default, then each level
  with a 16px dial and its name, the current one checked, applied at once.
- The name lives in the `title`, the `aria-label` and an `sr-only` span
  ("Reasoning effort: High"). A model that takes no effort shows no dial;
  one the catalog lists without levels but that runs at one is drawn on
  Claude Code's scale (low to max).

**The first screens.** The Welcome ("One deck for all your agents") and
the connect screen ("What should we work on?") are the home's family, in
every look (`FirstFrame` in `ui/Welcome.tsx`):

- The theme's picture fills the window, the same on both screens, so Pair
  on the Welcome changes the cards and not the room.
- A scrim (`.first-scrim`) pools the canvas behind the column, about 84%
  at its middle and 22% at the edges, so the picture shows whole round
  the column. Under the column the dots soften through a 5px blur that
  fades out by a mask, so a hard dither never fights the type. Bands of
  the canvas sit under the top strip and the credit.
- One centred column, 44rem at most: the Deck mark, the question in two
  tones (its quiet half in the muted colour, with a soft halo of the
  canvas), a line on what Deck is, and on the Welcome a sample message as
  a capsule of glass.
- Under it, two `ActionCard`s side by side (one column below 640px): Open
  a folder or Add project, and Pair with a Deck. The primary one wears
  the accent on its rim and in its icon.
- Then cards of `liquid` glass: the agents Deck found, the pairing form,
  the recent folders, and any notice or start error.
- Liquid Glass frosts these cards more here (16px, a 55% tint), as it
  does the sidebar. The picture's credit is a chip of the canvas in the
  bottom right.
- The column scrolls when the window is short. Under 760px tall, the mark
  and the gaps shrink.

## 13. The sidebar, the top bar and pages at the side

After T3 Code's Obsidian theme, the sidebar is calm: the threads and little
else (`ui/Sidebar.tsx`, the "sidebar" section of styles.css).

- **Header** (36px, the title bar's row): four faint glyphs at its end:
  the inbox with its count in the warning's colour (no badge), Search, New
  thread and Hide the sidebar. Nothing else floats there.
- **Projects**: a quiet heading (`All projects ▾`, the filter, with Add
  project on hover), then each project's name and count in 11px faint type
  (no tile; its New thread and Close on hover).
- **Threads**: plain grey rows of 13px type, about 30px tall (`h-7.5`), 1px
  apart, groups 8px apart. The open thread is a card of soft grey (8% of
  the text colour) with a faint rim (`--row-rim`), radius `--row-radius`
  (12px in smoked glass, 10px elsewhere). A row's state is a dot at its
  leading edge; its age, faint, at its end.
- **Foot** (40px, no hairline): Settings, Usage, Team, the bell and Close
  project as faint glyphs, and the connection as a dot alone.
- In smoked glass the column is darker than the room, nearly black in the
  dark mode, and one hairline parts it from the room.

**The top bar** (`ui/ThreadBar.tsx`). The same `nav` ("Threads") as a 40px
bar across the window: the project switcher, the threads as chips (a
state's dot, the title, the open one the soft card), a "+" for a new
thread and a menu of every thread (the archived too), then Search, the
inbox and the foot's controls, and Hide. The chips scroll sideways (the
wheel scrolls them too) and fade at an edge with more behind it. With
Settings open the chips give way to its sections.

**Pages at the side.** Settings, Usage, Team and the inbox are panes the
room's layout can hold (`page:<name>` in `layout/tree.ts`, `openPage`):
the first opens in a stack of its own along the room's edge on the panel's
side, the next joins it as a tab. The pages' tabs, and the side panel's,
end in a "+" that opens another page; the pages' stack has a button that
pops the page to full size, and a full page has one in its top right
corner that puts it at the side. At the side a page keeps its controls but
leaves its name to its tab, Settings picks its section from a strip of its
own, and an empty page stands its plate over its words. Full size stays
the default from the sidebar; Settings > Appearance > Layout > Pages makes
the sidebar's buttons open them at the side instead (while a room shows).

## 14. The starter tour

A skippable walk through the window for a new person (`ui/Tour.tsx`, the
steps and their logic in `tour.ts`), as coach marks in the window's own
material.

**Steps.** The Welcome has four: what Deck is, Open a folder, Pair with a
Deck, and the agent CLIs. A project's window has ten: a look around, the
threads (`nav[aria-label="Threads"]`, the sidebar or the top bar), the
composer and its `$agent`, `@` and `#` mentions, the mode menu (Plan / Ask
/ Yolo), the effort dial (`.effort-picker`), the home cards (`section.home`), Conversation / Graph
(`.room-views`), the diff and panels (`.panel-toggles`), the palette
(⌘/Ctrl+K) and Settings › Appearance (the Settings button in the sidebar's
foot, or in the top bar's tools). Each finds its target by selector when
it shows (a selector can name it in each layout, and the first match on
screen wins); a step whose target is not on screen is passed over (the
home cards in an open thread, the views on a new one, the effort dial for
a model that takes none). The mode menu lives only in a thread, so its step
shows in the middle without it.

**Look.** A dim veil with a hole cut round the target (one element whose
spread shadow is the veil), the hole's edge a ring of the accent that
breathes, and a card of `liquid-thick` glass on `--radius`: the count,
the title, a line or two, the dots (the current one a capsule of the
accent), Skip, Back and Next. The card sits below the target, else above,
else beside it, else in the middle, never past the window's edges, and
follows the target as the window resizes. A step with no target shrinks
the hole to a point under the card. Hole and card glide between steps on
`--ease`; reduced motion holds them still.

**Input.** The veil takes no pointer, only the card does: the window stays
usable, and Open a folder mid-tour opens the folder (the tour then goes on
with the project's steps). ← and → step, Esc leaves, Next holds the focus;
while the person types in a field the keys are theirs.

**When.** The pref `tour` is `"new"` in a new window and `"done"` once the
tour is finished or skipped (prefs saved before it existed load as done).
It starts by itself only on a real first run: never when
`navigator.webdriver` is set or the app was launched with a remote
debugging port (`AppInfo.automated`), so the end-to-end tests click through
an untouched window. The Welcome's part ends in Got it and hands over to the
project's when a project opens. The palette's Show the tour and Settings ›
General › Getting started start it again; starting it closes the page in
the room's place.
