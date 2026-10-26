# Deck desktop: Liquid Glass

Status: 2026-10-26. This replaces the look in `design-apple.md` for every
theme. A theme now gives colours only. The materials, the layout, the
components and the motion below are the same in all of them. "Liquid
Glass" is the default theme, and Apple, Tabletop, T3 and the rest of the
catalog stay as colourways of it.

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

1. **Backdrop.** `body`: a deep tone of the theme's canvas, with three
   soft pools of light: the accent (top left), then Claude's and
   Antigravity's hues (bottom right and bottom left). It follows the
   accent. On a Mac with vibrancy, the desktop shows through a veil of it.
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
  result in dark and light.
