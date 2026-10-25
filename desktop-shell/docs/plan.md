# Deck desktop: the plan

Status: 2026-10-25. The shell at `desktop-shell/` works: two transports,
a bundled engine, a strong security posture, 108 unit tests and an
Electron smoke test. This plan says what it takes to make it the best
way to run many agents and people on one codebase, and in what order.

## 1. Diagnosis

What the shell has today, verified against the code:

- One state, two transports (`src/renderer/client.ts`), a React UI remade
  after T3 Code, side panels for diff, approvals, queue, side chat and
  agents, a Team page, a Usage page, a terminal drawer, git actions.
- The engine already does the hard multi-agent work: pools, subagent
  trees, leases, races, budgets, shared threads with roles (observer,
  participant, editor), contracts between Decks, memory pinned to code
  (`docs/multiplayer-design.md`). It also has per-thread isolation,
  revert and archive (`internal/deck/session/isolate.go`, `changes.go`,
  `lifecycle.go`) that `deck.proto` does not expose.

What is wrong:

1. **No identity.** The look is T3 Code's structure without its
   discipline: untinted grey (`#0a0a0a`, white-alpha fills), the default
   blue, the system font, every agent reply in the same flat column, no
   motif. It reads as a template.
2. **Multi-agent work is hidden.** Agents, tasks, races, leases and the
   team live in the fifth side-panel tab and a page that replaces the
   chat. A thread looks like a chat with one bot. Nothing shows who is
   in the room, who is working on what, or what needs a person.
3. **The shell forgets.** `App.tsx:238` remounts `Shell` per project, so
   switching projects kills every terminal and dev server
   (`Terminal.tsx:182`), drops drafts (`Shell.tsx:195`) and loses unread
   state (`Shell.tsx:194`). Approvals and Done are per project. When the
   engine dies the project closes with no restart (`decks.ts:231`).
4. **No platform polish.** No app menu (Electron's default, with DevTools
   in production), no context menus, no native notifications (the
   permission handlers deny everything, `main.ts:186`), no dock badge,
   no window-state memory, no auto-update, no log file, no first-run
   screen, and no CI job for the shell.
5. **Performance.** `Shell` subscribes to the whole `AppState`
   (`Shell.tsx:192`) while the rpc stream sends every session's tokens,
   so a background thread re-renders everything. No virtualization. The
   agents, team and git views poll.

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
  commands, ids, numbers and diffs. Both SIL OFL, bundled as woff2 from
  the `geist` npm package (the CSP already allows `font-src 'self'`). UI
  13px, meta 11px and 10px, prose 15px/1.6, code 12.5px. `tabular-nums`
  on every number. No weight above 500.
- **Surfaces.** No drop shadow except popovers. Raised controls get an
  inset 1px top highlight at 5% white. Hover and selection are white
  alpha, never amber.
- **Agent hue** appears only as a 2px rail, a glyph or a dot. Never as a
  fill. Amber is the app's own state colour, so the agent hues never
  compete with it.
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

- **One status model** (after Devin's session states, Amoeba's board).
  Each room is in one state, derived in one pure function from the
  session, its approvals, the board, the agents and the notices:
  `working`, `needs-you`, `overlap`, `review` (idle with changes nobody
  looked at), `done`, `idle`. Each state carries a label that names the
  thing: "Approve command", "Answer claude-1", "Task blocked", "codex-2
  and claude-1 both changed auth.go", "PR #12 opened", and a timestamp.
  The sidebar, the inbox, the dock badge and the notifications all read
  this one model.
- **Room header: the lane strip.** One chip per participant: the person,
  each agent (glyph, index, name, status, with a thin amber pulse while
  working), each teammate following the room with their role. Click a
  chip to filter the timeline to that lane, or to open its agent (model,
  effort, stop, steer).
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
  Tasks (board, races, apply or discard a worktree result, with a diff
  preview first), Agents (the tree with context and cost per agent), Side
  chat.
- **Plans are artefacts.** A plan an agent posts in plan mode renders as
  a step list card with Approve, Edit and Ask buttons, not as prose. A
  preference auto-approves plans for people who trust their agents.
- **A line comment delegates.** In the diff, a comment on a line goes to
  the composer with the file, the range and the lines as context, aimed
  at the agent that wrote the change.
- **Taking over is a verb.** Stop, then edit, then "Continue; I changed
  X" sends the person's note with the files they touched since the stop.

## 4. Work packages

Each package names its files, what done means and the checks. All
packages run `npm run lint`, `npm test` and `npm run build`; those that
touch main also run `xvfb-run -a npm run smoke`. Prose in the UI and the
docs follows the repo's style: short sentences, no jargon.

### WP0 The shell remembers: app-level state

Scope: `src/renderer/ui/App.tsx`, `ui/Shell.tsx` (state only, not
classes), `ui/Terminal.tsx`, `src/renderer/ui.ts`, new
`src/renderer/appState.ts`, `src/renderer/drafts.ts`.

- Lift the UI reducer, drafts and unread state out of the per-project
  `Shell` into one store keyed by project, so a project switch keeps
  them. Drafts persist in localStorage per room.
- Terminals belong to the project, not to the mounted `Shell`: keep
  xterm instances and their subscriptions in a module store; the drawer
  attaches and detaches. Drawer height persists.
- Open saved projects in parallel at start, not one after another
  (`App.tsx:165`).
- Subscribe to slices: the sidebar reads sessions and statuses, the
  timeline reads one session's messages. `React.memo` on rows. A
  background session's tokens must not re-render the open timeline.
- Engine exit: the project stays open in an "engine stopped" state with
  the last stderr lines and a Restart button; a restart reconnects and
  resyncs.

Done when a test switches projects with a running fake terminal, a
draft and an unread room, and all three survive.

### WP1 Design system (Tabletop)

Scope: `src/renderer/styles.css`, `themes.ts`, `appearance.ts`,
`ui/kit/*`, `ui/Timeline.tsx`, `ui/Composer.tsx`, `ui/Sidebar.tsx`,
`ui/Shell.tsx` (classes only), `index.html`, `package.json` (`geist`).

- Add the Tabletop tokens as the default palette, light and dark, and
  keep the other palettes working on the same token names.
- Bundle Geist Sans and Geist Mono (variable woff2) from the `geist`
  package through Vite; `@font-face` in `styles.css`.
- Type scale, radius family, hairlines, inset highlights, one easing,
  reduced-motion rules.
- Timeline: agent prose without a container; the person's turn as a
  card with a rail; tool rows in mono; the corner index on each agent's
  first line.
- Composer as the hero surface; amber send; agent chips with hue rails.
- Sidebar: darker material, 36px rows, agent rail on a working row,
  status glyphs.
- Settings: Appearance shows Tabletop first; the swatches are right.
- Hover-only controls also show on `focus-within`; menus get arrow keys
  and focus return; dialogs trap focus.

Done when the screenshots in `docs/` are regenerated by the e2e test and
the README's "Use it" section names Tabletop.

### WP2 Attention: status model, inbox, notifications, badge

Scope: new `src/renderer/attention.ts` (pure), new `ui/Inbox.tsx`,
`ui/Sidebar.tsx` rows, `ui/Shell.tsx` (toasts), `src/main/main.ts` and
`host.ts` (host methods `notify`, `badge`, `focused`),
`src/shared/bridge.ts`, `src/preload/preload.ts`.

- `roomStatus(...)` returns the state and label of §3, with tests for
  each state. `needs-you` clears only when the person acts (answers,
  replies, or the agent moves on), never because the room was looked
  at; `review` and `done` clear when the room is opened (the lesson of
  Superset's issue 8634).
- Inbox page and sidebar entry with a count; keyboard navigation;
  shared approvals an editor may answer appear in it too.
- Ctrl/Cmd+Alt+L jumps to the next room that needs you, across
  projects; Ctrl/Cmd+1 to 9 open the first nine rooms of the sidebar,
  and holding the modifier shows the digits on the rows (Conductor).
- A room that finishes, fails, or needs an answer moves to the top of
  its group (T3 Code).
- Native notification when a room needs a person or finishes while the
  window is unfocused; it names the project and the room; click focuses
  the window and opens the room. No notification while the person has
  a message queued for that room (they are already on it).
  `app.setBadgeCount` on macOS and Linux; the overlay icon on Windows.
  Main sends notifications itself; the renderer's permission handlers
  stay as they are.
- Toasts get a dismiss button and stable timers; notices from
  background rooms go to the inbox, not to toasts.
- Settings: notifications all, needs-you only, or none; a sound toggle.

### WP3 Platform: menu, context menus, window state, logs, updates

Scope: `src/main/` (new `menu.ts`, `windowState.ts`, `log.ts`,
`update.ts`, `contextMenu.ts`), `main.ts`, `host.ts`, `bridge.ts`,
`preload.ts`, `ui/Settings.tsx` (About), `package.json`
(`electron-updater`, `electron-log`, `publish`, `protocols`).

- Native application menu with the standard roles and Deck's shortcuts
  (new thread, open folder, toggle sidebar and panel, terminal, settings,
  zoom; reload and DevTools only when unpackaged). Each item sends a
  `menu` event; the renderer runs the same command the palette runs.
- Context menus: edit actions on inputs, copy on selection, copy link and
  open in browser on links, copy image, rename and delete on a thread
  row. Ctrl/Cmd+Y stays Redo inside text fields; Yolo moves to
  Ctrl/Cmd+Shift+Y.
- Window state: size, position and maximized, saved in `userData`,
  restored on launch and clamped to a display. Zoom level persists.
- Logging: electron-log for main; renderer errors and unhandled
  rejections go to main over one IPC channel; engine stderr goes to the
  log; "Open logs folder" in Help and in Settings.
- `deck://` as a protocol handler (`open-url`, `second-instance` argv):
  `deck://pair?…` pairs, `deck://open?path=…` opens a folder. A folder
  dropped on the window opens it. `deck-desktop <dir>` opens it.
- Auto-update: electron-updater against the GitHub releases of
  `dev-dami/deck-releases`; check at start and every 6 hours; a quiet
  "Update ready, restart" row in the sidebar footer; "Check for
  updates" in Help. Off when unpackaged.
- About: app version, engine version (`Info.version`), Electron and
  Chromium.

### WP4 Room: lane strip, room tabs, sharing, plans, line comments

Scope: `ui/Shell.tsx` (split out `ui/RoomHeader.tsx`, `ui/Lanes.tsx`),
`ui/RightPanel.tsx` (becomes the room tabs), `ui/ApprovalCard.tsx`,
`ui/Team.tsx` (share control; followed threads render in the
`Timeline`), `ui/Timeline.tsx` (plan card, lane filter, message
actions), `ui/DiffView.tsx` (line comment), `ui/AgentsPanel.tsx`
(apply and discard preview), `src/renderer/attention.ts`.

- Lane strip from the roster, the agents tree and the thread's shares.
- Approval card names who may answer (you; editors who follow).
- Share control as an avatar stack in the header; role menu.
- Rooms shared with you in the sidebar, grouped by teammate, opening in
  the room view through `ReadShared`, `PostShared` and `AnswerShared`.
- Plan card: detect a plan-mode reply (a numbered step list) and render
  it with Approve (switches to ask or yolo and sends "go"), Edit (puts
  the steps in the composer) and Ask. Auto-approve preference.
- Message actions: copy, edit and resend (`EditMessage`), continue
  (`Continue`), retry. Both calls are already allow-listed in
  `bridge.ts`.
- Line comment in the diff: a hover button on a line; the comment goes to
  the composer with `path:line` and the lines quoted, addressed to the
  agent that changed the file.
- Apply and discard on the task board show the diff first and ask.
- Races: a structured launcher instead of composer syntax; entries
  compare side by side.

### WP5 Composer and commands

Scope: `ui/Composer.tsx`, `src/renderer/completion.ts`, `ui/Palette.tsx`,
`src/renderer/history.ts`.

- Slash commands with a picker: `/plan`, `/ask`, `/yolo`, `/new`,
  `/race <n>`, `/share`, `/stop`, `/clear`. Each maps to an existing
  client call.
- `@` picker for the changed files of the room (from `Changes`); a full
  file picker waits for the engine's `ListFiles` (WP-E2).
- Queue or steer on a modifier: Enter queues for a busy agent (as now),
  Ctrl/Cmd+Enter steers into its running turn; the send button says
  which. The engine has both (`Queue`, `Steer`). The queue panel lets the
  person edit and reorder what waits.
- Prompt history on `↑` in an empty composer. Image thumbnails on
  attachments.
- The composer strip shows the agent's context use as a small ring and
  its plan window when the engine reports one (`Usage`), so limits are
  visible before they stop a turn.
- The palette lists every menu command with its shortcut, searches
  threads of every open project by title and branch, and by message text
  for the rooms that are loaded; `>` filters to actions.

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
`ui/Shell.tsx`, `ui/Sidebar.tsx`, `src/renderer/rpcClient.ts`.

- CI job `desktop (ubuntu-latest)`: Node 22 with the npm cache on
  `desktop-shell/package-lock.json`, `npm ci`, `npm run lint`,
  `npm test`, `npm run build`, `npx playwright install --with-deps
  chromium`, `xvfb-run -a npm run e2e`, `xvfb-run -a npm run smoke`.
  Triggered by changes under `desktop-shell/`, `pkg/remote/web/src/`,
  `proto/` and `pkg/rpc/`.
- Split `Shell.tsx` (795 lines) and `Sidebar.tsx` (768 lines, 34
  props); keep behaviour and tests green.
- Virtualize the timeline and the thread list above 200 rows.
- Remove the duplicated helpers (`crew.ts`, `project.ts`), `splitSettled`
  and the second `base()`.
- E2E: project switch keeps terminals and drafts; inbox and notification
  path over the fake rpc Deck; the lane strip; the plan card.

### Engine packages (Go: `pkg/rpc`, `proto/deck/v1`; `buf generate`)

- **WP-E1 Room events.** The engine writes overlap and lease notes as
  notice entries with `Via: "overlap"` (`internal/deck/session/
  leases.go`), but `pkg/rpc/convert.go` drops the `Via`. `Notice` gains
  `via`; `Event` gains `lease_changed`, `board_changed`, `agents_changed`
  and `team_changed` so the desktop stops polling; `Session` gains
  `attention` (none, approval, question, blocked, error) and `branch`.
  Status: done. The room status reads `attention` and `via`, and the lane
  strip reads the live leases.
- **WP-E2 Files.** `ListFiles {session_id, query, limit}` over the tree
  with the repo's ignore rules, for the `@` picker; `ReadImage` for
  `ImageRef` bytes.
  Status: done for `ListFiles`, which the `@` picker uses. The engine has
  no `ReadImage` yet.
- **WP-E3 Expose what the engine has.** `CreateSessionRequest.isolate`
  and `ApplyIsolated`/`Unisolate` (`isolate.go`), `Revert` and
  `UndoRevert` (`changes.go`), `ArchiveSession` (`lifecycle.go`) with a
  five-second Undo toast in the shell and never for a working room,
  `StartRace` as a structured call, `Info` with the detected CLIs and
  their login state.
  Status: done. The new-thread toggle, the branch menu in the room header,
  Revert in Changes, Archive on the rows and the race form with a judge
  use these calls.
- **WP-E4 Ports and search.** A terminal or agent command that opens a
  listening port is reported on the room (`port_opened`), so the shell
  shows it as a chip and opens it in the browser; `SearchMessages
  {query, limit}` across the project's threads for the palette.

Each engine package comes with its `rpc.ts`/`rpcMap.ts` mapping and
guards on the TypeScript side, behind a capability check so the shell
works against an older engine.

## 5. Waves

Packages in one wave touch different files and run in parallel, each on
its own worktree. Each wave is merged, every check runs, and the next
wave starts.

| Wave | Packages | Why this order |
|---|---|---|
| 1 | WP0, WP1, WP3, WP7 (CI job only) | State, tokens and main are disjoint; CI catches everything after |
| 2 | WP2, WP4 | Both need WP0's store and WP1's tokens; WP2 owns `attention.ts`, `Inbox.tsx`, main; WP4 owns the room files |
| 3 | WP5, WP6, WP7 (split, virtualization, cleanup, e2e) | Composer, welcome and the split need the room in place |
| 4 | WP-E1, WP-E2, WP-E3 and their UI | Engine work last, each behind a capability check, so the shell ships without it |

## 6. What this does not do

- No new transport, no change to the security posture (the CSP, the
  sandbox and the token handling stay as they are).
- No Figma-style live cursors: Deck shares threads, not editors.
- No hosted service. Presence and overlap stay on the peer link.
- No multi-window yet. The single `win` and the sender check stay until
  WP0's store makes a second window cheap.
