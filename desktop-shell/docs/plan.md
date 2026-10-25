# Deck desktop: the plan

Status: draft, 2026-10-25. The shell at `desktop-shell/` works: two
transports, a bundled engine, a strong security posture, 108 unit tests
and an Electron smoke test. This plan says what it takes to make it the
best way to run many agents and people on one codebase, and in what
order.

## 1. Diagnosis

What the shell has today, verified against the code:

- One state, two transports (`src/renderer/client.ts`), a React UI remade
  after T3 Code, side panels for diff, approvals, queue, side chat and
  agents, a Team page, a Usage page, a terminal drawer, git actions.
- The engine already does the hard multi-agent work: pools, subagent
  trees, leases, races, budgets, shared threads with roles (observer,
  participant, editor), contracts between Decks, memory pinned to code
  (`docs/multiplayer-design.md`).

What is wrong:

1. **No identity.** The look is T3 Code's structure without its
   discipline: untinted grey (`#0a0a0a`, white-alpha fills), the default
   blue, the system font, every agent reply in the same flat column, no
   motif. It reads as a template.
2. **Multi-agent work is hidden.** Agents, tasks, races, leases and the
   team live in side panels and separate pages. A thread looks like a
   chat with one bot. Nothing shows who is in the room, who is working on
   what, or what needs a person.
3. **No platform polish.** No app menu, no context menus, no native
   notifications, no dock badge, no window-state memory, no auto-update,
   no log file, no first-run screen, and no CI job for the shell.

## 2. Identity: Tabletop

Research (T3 Code source, Linear, Raycast, Warp, Zed, Claude, Cursor)
shows that tasteful dark tools share rules, not colours: near-black
canvas with a darker sidebar, separation by 6 to 8% white hairlines
instead of grey fills, one accent spent on state, an 11/10px metadata
scale, mono for everything a machine wrote, one radius family, one easing
curve, no container around agent prose, and one hero surface.

Deck's identity is **Tabletop**: a felt table at night. Warm ink, paper
text, one lamp. Nobody in this category is warm; Claude's clay hue
already belongs to this world.

### Tokens

```css
/* neutrals: hue 70, chroma 0.004-0.008 */
--canvas:          oklch(0.145 0.005 70);
--sidebar:         oklch(0.120 0.005 70);   /* darker than the canvas */
--surface-1:       oklch(0.175 0.006 70);   /* cards, composer */
--surface-2:       oklch(0.215 0.007 70);   /* popovers */
--hairline:        oklch(1 0 0 / 0.07);
--hairline-strong: oklch(1 0 0 / 0.12);
--hover:           oklch(1 0 0 / 0.04);
--selected:        oklch(1 0 0 / 0.08);
--text:            oklch(0.93 0.008 80);
--text-muted:      oklch(0.68 0.012 75);
--text-faint:      oklch(0.50 0.010 75);
--signal:          oklch(0.80 0.14 75);     /* amber: running, focus, send */
--agent-claude:    oklch(0.70 0.13 40);     /* clay */
--agent-codex:     oklch(0.80 0.03 90);     /* bone, near neutral */
--agent-opencode:  oklch(0.74 0.09 235);    /* ice */
--agent-antigravity: oklch(0.72 0.10 300);
--ease:            cubic-bezier(0.32, 0.72, 0, 1);
--radius:          8px;                      /* 6 / 8 / 12 / 16 */
```

Light mode keeps the same hue and the same deltas on a warm paper canvas
(`oklch(0.975 0.006 80)`).

### Rules

- **Type.** Geist Sans for UI and prose, Geist Mono for paths, branches,
  commands, ids, numbers and diffs. Both SIL OFL, bundled as woff2 under
  the renderer (the CSP already allows `font-src 'self'`). UI 13px, meta
  11px and 10px, prose 15px/1.6, code 12.5px. `tabular-nums` on every
  number. No weight above 500.
- **Surfaces.** No drop shadow except popovers. Raised controls get an
  inset 1px top highlight at 5% white. Hover and selection are white
  alpha, never amber.
- **Agent hue** appears only as a 2px rail, a glyph or a dot. Never as a
  fill. Amber is the app's own state colour, so the three agent hues
  never compete with it.
- **Agent prose has no container.** The person's turn is a card on
  `--surface-1` with a 2px left rail in `--text-faint`: a played card.
  Tool calls are 12.5px mono rows with a muted `$`.
- **The corner index** is the motif. Every thread card, room header and
  agent chip carries a small mono index in the agent hue, like the rank
  in the corner of a playing card: `✳ a7f`, `⌥ k21`. The terminal-prompt
  icon is the app's own suit.
- **Motion.** One easing. A new thread slides in 180ms with a 4px
  translate. The active thread row is lifted one surface. Pulses step
  (`steps(4)`) so an idle window costs no frames. Reduced motion turns
  all of it off.
- **The hero surface** is the composer: `--surface-1`, hairline, inset
  highlight, 12px radius, amber focus ring at 40%. The send button is the
  only amber fill in the app.

The theme catalog stays. "Tabletop" becomes the default; "T3" stays as a
choice.

## 3. Information architecture: the room

A thread is a room. The engine already has the parts: the roster is who
is in it, shares are the membership and roles, leases are who holds
what, the board is the work, team presence is who is around. The desktop
shows the room, not a chat with panels.

```
┌ sidebar ──────┬ room ─────────────────────────────────────┬ side ─────┐
│ Inbox (3)     │ ✳ a7f  Fix the pairing lockout   main ↑1  │ Changes   │
│ ───────────── │ ◉ you  ✳ claude-1 working  ⌥ codex-2 idle │ Tasks     │
│ deck          │       ◇ vps/claude observer                │ Agents    │
│  ● Fix the…   │ ─────────────────────────────────────────  │ Side chat │
│  ◐ Docs pass  │ timeline: prose, cards, tool rows, plans   │           │
│  ○ Long hist… │                                            │           │
│ Shared w/ you │ [approval card: who may answer]            │           │
│  vps · Auth…  │ composer · mode · agents · branch          │           │
└───────────────┴────────────────────────────────────────────┴───────────┘
```

- **Room header: the lane strip.** One chip per participant: the person,
  each agent (glyph, index, name, status: working, tool, waiting, idle,
  error, with a thin amber pulse while working), each teammate following
  the room with their role. Click a chip to filter the timeline to that
  lane, or to open its agent (model, effort, stop, steer).
- **Status is the sidebar's job.** Each room row shows one of: working,
  needs you, overlap, done, idle. "Needs you" is an approval, a question,
  a blocked task or an error. "Overlap" is two agents on the same file,
  from the engine's lease and overlap notices.
- **Inbox.** One list, across all open projects, of what needs a person:
  approvals (with who may answer them), questions, finished rooms,
  errors, contract proposals. Keyboard first: `j`/`k`, `y`/`n`, `enter`
  opens the room. The dock badge is the inbox count.
- **Sharing is quiet.** The share control sits in the room header as a
  small avatar stack with "+N". Clicking it shows teammates and their
  role (observer, participant, editor) and lets you change it. Rooms a
  teammate shared with you appear in the sidebar under their name and
  open in the same room view, read-only or with a composer, by role.
- **Tasks and agents are tabs of the room**, not global panels: Changes,
  Tasks (board, races, apply or discard a worktree result), Agents (the
  tree with context and cost per agent), Side chat.
- **Plans are artefacts.** A plan an agent posts in plan mode renders as
  a step list card with Approve, Edit and Ask buttons, not as prose.
- **A line comment delegates.** In the diff, a comment on a line goes to
  the composer with the file, the range and the lines as context, aimed
  at the agent that wrote the change.

## 4. Work packages

Each package names its files, what done means and the checks. All
packages run `npm run lint`, `npm test` and `npm run build`; those that
touch main also run `xvfb-run -a npm run smoke`. Prose in the UI and the
docs follows the repo's style: short sentences, no jargon.

### WP1 Design system (Tabletop)

Scope: `src/renderer/styles.css`, `themes.ts`, `appearance.ts`,
`ui/kit/*`, `ui/Timeline.tsx`, `ui/Composer.tsx`, `ui/Sidebar.tsx`,
`ui/Shell.tsx` (classes only), `index.html`, new `src/renderer/fonts/`.

- Add the Tabletop tokens as the default palette, light and dark, and
  keep the other palettes working on the same token names.
- Bundle Geist Sans and Geist Mono (woff2, variable) from the `geist`
  npm package; `@font-face` in `styles.css`; the CSP needs no change.
- Type scale, radius family, hairlines, inset highlights, one easing,
  reduced-motion rules.
- Timeline: agent prose without a container; the person's turn as a
  card with a rail; tool rows in mono; the corner index on each agent's
  first line.
- Composer as the hero surface; amber send; agent chips with hue rails.
- Sidebar: darker material, 36px rows, agent rail on a working row,
  status glyphs.
- Settings: Appearance shows Tabletop first; the swatches are right.

Done when the screenshots in `docs/` are regenerated by the e2e test and
the README's "Use it" section names Tabletop.

### WP2 Attention: statuses, inbox, notifications, badge

Scope: `src/renderer/ui.ts` (reducer), new `src/renderer/attention.ts`
(pure: derive each room's status and the inbox from `AppState`, the
board and the agents), new `ui/Inbox.tsx`, `ui/Sidebar.tsx` rows,
`src/main/main.ts` and `host.ts` (two host methods: `notify`, `badge`),
`src/shared/bridge.ts`, `src/preload/preload.ts`.

- `roomStatus(session, approvals, board, agents)` returns working, needs
  you, overlap, done or idle, with tests.
- Inbox page and sidebar entry with a count; keyboard navigation.
- Native notification when a room needs a person or finishes while the
  window is unfocused; click focuses the window and opens the room.
  `app.setBadgeCount` on macOS and Linux; the overlay icon on Windows.
- A notification preference in Settings: all, needs-you only, none; and
  a sound toggle.

### WP3 Platform: menu, context menus, window state, logs, updates

Scope: `src/main/` (new `menu.ts`, `windowState.ts`, `log.ts`,
`update.ts`), `main.ts`, `host.ts`, `bridge.ts`, `preload.ts`,
`ui/Settings.tsx` (About section), `package.json` (electron-updater,
electron-log, publish config).

- Native application menu with the standard roles and Deck's shortcuts
  (new thread, open folder, toggle sidebar and panel, terminal, settings,
  zoom, reload in dev only). Each item calls the same command the
  palette calls, through a `menu` event to the renderer.
- Context menus: edit actions on inputs, copy on selection, copy link and
  open in browser on links, copy image, rename and delete on a thread
  row.
- Window state: size, position and maximized, saved in `userData`,
  restored on launch and clamped to a display.
- Logging: electron-log for main; renderer errors and unhandled
  rejections go to main over one IPC channel; "Open logs folder" in
  Help and in Settings.
- Engine lifecycle: when `deck serve --rpc` exits unexpectedly, show a
  toast with the last stderr lines and a Restart button.
- Auto-update: electron-updater against the GitHub releases of
  `dev-dami/deck-releases`; check at start and every 6 hours; a quiet
  "Update ready, restart" row in the sidebar footer; a Check for updates
  item in Help. Off in dev and when unpackaged.
- About: version, engine version (`Info.version`), Electron and Chromium.

### WP4 Room: lane strip, room tabs, sharing, plans, line comments

Scope: `ui/Shell.tsx` (split out `ui/RoomHeader.tsx`, `ui/Lanes.tsx`),
`ui/RightPanel.tsx` (becomes the room tabs), `ui/ApprovalCard.tsx`,
`ui/Team.tsx` (share control), `ui/Timeline.tsx` (plan card, lane
filter), `ui/DiffView.tsx` (line comment), `src/renderer/attention.ts`.

- Lane strip from the roster, the agents tree and the thread's shares.
- Approval card names who may answer (you; editors who follow).
- Share control as an avatar stack in the header; role menu.
- Rooms shared with you in the sidebar, grouped by teammate, opening in
  the room view with the `ReadShared`/`PostShared`/`AnswerShared` calls.
- Plan card: detect a plan-mode reply (a numbered step list under a
  heading) and render it with Approve (switches to ask or yolo and sends
  "go"), Edit (puts the steps in the composer) and Ask.
- Line comment in the diff: a hover button on a line; the comment goes to
  the composer with `path:line` and the lines quoted, addressed to the
  agent that changed the file.

### WP5 Composer and commands

Scope: `ui/Composer.tsx`, `src/renderer/completion.ts`, `ui/Palette.tsx`,
new `src/renderer/drafts.ts`, `src/renderer/history.ts`.

- Slash commands with a picker: `/plan`, `/ask`, `/yolo`, `/new`,
  `/race <n>`, `/share`, `/stop`, `/clear`. Each maps to an existing
  client call.
- `@` picker for the changed files of the room (from `Changes`); a full
  file picker waits for an engine `ListFiles` call (WP-E2).
- Drafts per room, kept in localStorage; prompt history on `↑` in an
  empty composer.
- The palette lists every menu command with its shortcut.

### WP6 First run and empty states

Scope: new `ui/Welcome.tsx`, `ui/ConnectScreen.tsx`, `ui/Shell.tsx`
(new-thread screen), `src/main/decks.ts` (surface the engine's
"no agent CLI" message as structured data).

- First launch: a Tabletop welcome with Open a folder, Pair with a Deck,
  and the detected agent CLIs (from `Info.kinds`) with install links for
  the missing ones.
- The new-thread screen suggests three starts: one agent, two agents
  that split the work, a race of three.
- Empty states for Inbox, Changes, Tasks and Agents say what will appear
  there and how.

### WP7 Engineering health

Scope: `.github/workflows/ci.yml`, `desktop-shell/test/e2e/*`,
`ui/Shell.tsx`, `ui/Sidebar.tsx`.

- CI job `desktop (ubuntu-latest)`: Node 22, `npm ci`, `npm run lint`,
  `npm test`, `npm run build`, `xvfb-run -a npm run e2e`.
- Split `Shell.tsx` (795 lines) into `RoomHeader`, `Lanes`, `NewThread`
  and the shell; keep behaviour, keep tests green.
- Virtualize the timeline and the thread list above 200 rows.
- E2E: inbox and notification path over the fake rpc Deck; the lane
  strip; the plan card.

### Engine packages (Go, `pkg/rpc`, `proto/deck/v1`)

- **WP-E1 Room events.** The engine already writes overlap and lease
  notes as notice entries with `Via: "overlap"`
  (`internal/deck/session/leases.go`), but `pkg/rpc/convert.go` drops
  the `Via`. `Notice` gains `via`, and `Event` gains `lease_changed`
  (session, agent, paths, exclusive), so the desktop derives the overlap
  status without parsing text; `Session` gains `attention` (none,
  approval, question, blocked, error) computed by the engine.
- **WP-E2 Files.** `ListFiles {session_id, query, limit}` over the
  tree with the repo's ignore rules, for the `@` picker.
- **WP-E3 Isolated threads.** `CreateSessionRequest.isolate` starts the
  thread on a worktree and branch of its own, with apply and discard, as
  tasks already do. The branch shows on the room row.

Each engine package comes with its `rpc.ts`/`rpcMap.ts` mapping and
guards on the TypeScript side.

## 5. Waves

Packages in one wave touch different files and run in parallel, each on
its own worktree. I merge each wave, run every check, and start the next.

| Wave | Packages | Why this order |
|---|---|---|
| 1 | WP1, WP3, WP7 (CI job only) | Tokens and platform touch disjoint files; CI catches everything after |
| 2 | WP2, WP4 | Both need WP1's tokens; WP2 owns `ui.ts`, `attention.ts`, `Inbox.tsx`, main; WP4 owns the room files |
| 3 | WP5, WP6, WP7 (split, virtualization, e2e) | Composer, welcome and the split need the room in place |
| 4 | WP-E1, WP-E2, WP-E3 and their UI | Engine work last, each behind a guard, so the shell ships without it |

## 6. What this does not do

- No new transport, no change to the security posture (the CSP, the
  sandbox and the token handling stay as they are).
- No Figma-style live cursors: Deck shares threads, not editors.
- No hosted service. Presence and overlap stay on the peer link.
