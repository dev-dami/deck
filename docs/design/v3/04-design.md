# 04 · The v3 experience and look

Status: v3 plan, chapter 4. Date: 2026-11-05.

This chapter decides how Deck v3 feels and looks on the desktop (GPUI), in the terminal (Bubble Tea) and on the phone (Expo). It covers the principles, the layout direction we picked, wireframes for every main screen, the design tokens, the status table all clients share, the copy for empty, error, offline and loading states, and what we deliberately do not copy. It builds on D6, D7 and D18, and touches D5, D9, D15, D16, D17 and D19. It does not reopen them.

Related chapters:
- Agents, tasks, the picker rules, the status vocabulary and attention rules: [03-agents-and-delegation.md](03-agents-and-delegation.md). This chapter owns the look of those things and the **final keymap** (03 §4.8 defers keys to here).
- Hosts, route badges, Add a host, moving a thread, offline queueing: [05-fleet-and-connect.md](05-fleet-and-connect.md).
- Protocol, revisions, summary streams: [07-protocol-and-performance.md](07-protocol-and-performance.md).

Sources and confidence:
- **T3 Code** was read from source (`github.com/pingdotgg/t3code` at `25b53299`, 2026-11-05). File references below point into that repo. These claims are verified.
- **Antigravity 2** and **Grok Bot** come from web search summaries; the research session could not open x.ai, docs.x.ai or most blogs. Every claim about them is marked **(snippet)**: it has not been checked against the full page, and no screenshots were viewed. Antigravity's official palette, type and motion could not be found at all, so nothing here copies them.
- GPUI facts are marked "(Zed source, not re-checked)" unless they come from the one verified source (no element backdrop blur, zed discussion #47429).
- Wireframes use `‹model›` where a model name would go. This plan names no models.

---

## 0. The idea in one paragraph

Deck v3 is a **dispatch desk for a crew**. The crew is your agents and the people you work with, spread across your hosts. Home is not a chat; it is what came back to you (T3 Code's inbox), with your crew across the top (Grok Bot's roster, as reported (snippet)) and everything running underneath as live trees you can follow (Antigravity 2's agent overview, as reported (snippet)). Every hand-off is a card you can see. Every row says which host it runs on. The chrome is grey, there is one green-teal accent, status has fixed colours, and nothing moves unless something changed.

"Beyond the 2D box" means three things here, and none of them is a 3D effect:
1. **The window is not the boundary.** Threads run on hosts with no client attached (D3). The UI says where each thing runs, keeps running when you close it, and lets you pick it up from any client.
2. **The unit on screen is the crew, not the pane.** Agents and people are first-class rows with identity and status. Threads are where they meet.
3. **Depth is shown as structure.** Delegation is a tree with three levels of attention (indicator → Follow → Open), not a stack of tabs.

---

## 1. Design principles

1. **Home is what came back to you.** The thread list is ordered by when each thread last came back to you (finished, failed, asked, needs approval), not by recency. Running work folds away; anything that needs you rises. (T3 Code `docs/user/thread-sidebar.md`.)
2. **Colour says whose move it is.** Amber: your move. Red: it broke. Green: a result is back. Working is the agent's move and stays neutral, with a stepped pulse. Chrome is grey. The green-teal accent belongs to Deck's own controls (focus, the primary button, links) and never means a status.
3. **Agents are teammates; threads are context bubbles.** An agent has a name, a description, a spec and a face, and it can be in many threads. Each piece of work gets its own thread. There is never one unbounded thread per agent (the Grok Bot complaint, (snippet)).
4. **Every hand-off is on the page.** A delegation is a card in the thread where it was asked, a node in the tree and, when it ends, a result card in the same place. Nothing is delegated where you can't see it. This is the opposite of Grok Bot's bot-to-bot messages in private chats (snippet).
5. **Say where.** Every thread, agent, approval and result names its host as soon as you have more than one ("on vps-1"), and names the person when it isn't you. A host that can't be reached says so in words, with a time ("vps-1 offline 3m").
6. **Model and effort are always visible, with their source.** On the composer chip, on every hand-off card and in every reply footer. Picking never writes global defaults (D5).
7. **Indicator, then Follow, then Open.** Overviews show one current-action line, never streaming text. A live view is something you choose (Follow), and taking over is a second, separate step (Open). This is Grok Bot's monitor → preview → take-over ladder (snippet) applied to every node.
8. **Artifacts are the unit of review.** Plans, walkthroughs, diffs, screenshots and command logs open as artifacts. Comments on them batch into one message to the agent (Antigravity 2's pattern, (snippet)).
9. **Squares are agents, circles are people.** Identity colour belongs to who someone is and is never used for status. Status never relies on colour alone: every status has a glyph and a word.
10. **Nothing repaints when nothing changes.** No continuous animation. Panels snap open. Pulses are stepped, a few frames a second, on visible rows only. An idle window draws zero frames. (T3 Code `AGENTS.md`: "No continuously repainting animations.")
11. **One key to the next thing that needs you.** Every action has a key, every key shows in the palette, and Escape closes only the top layer.
12. **Same words, same glyphs, on every client.** One status table (§5.1), one vocabulary, one set of agent marks. The engine computes status and attention (D4, 03 §4.2); clients render it and never infer it.

---

## 2. Three directions, and the pick

We sketched three directions from the research (`research-external.md` §7). All three use the same engine data. They differ in what the home screen is about.

### A. Crew Room

- **Metaphor:** a team chat. Agents are teammates on a roster, threads are topics, delegation is an @mention that opens a reply thread.
- **Layout:** left rail = roster (avatar, status, spec) above channels and threads; centre = timeline; right = agent profile and live preview.

```
┌ CREW ───────────────┬ #auth-refactor ──────────────────────────┬ claude ─────────────┐
│ [✻] claude    ● 3   │ (you) move auth to v2 tokens             │ General agent       │
│ [>_] codex    ! 1   │ [✻] claude  I'll map call sites first…   │ ‹model› ▂▄▆ high    │
│ [>_] reviewer ○     │   ↳ [>_] codex  "tests"  ● 2m  (thread)  │ in 3 threads        │
│ (A) ana       here  │ (A) ana  keep v1 for the phone?          │ ┌ live preview ───┐ │
│ THREADS             │                                          │ │ $ go test ./... │ │
│ # auth-refactor  !  │ ┌──────────────────────────────────────┐ │ └─────────────────┘ │
│ # billing        ✓  │ │ Message #auth-refactor…              │ │                     │
└─────────────────────┴──────────────────────────────────────────┴─────────────────────┘
```

- **Good:** closest to Grok Bot's feel. Strong identity. Profiles naturally carry spec and tools. Easy to explain.
- **Bad:** "what is running" is scattered across threads, so it needs an inbox anyway. A roster-first home stops scaling past about ten agents. A live preview on screen by default invites babysitting (xAI's own reason for hiding it, (snippet)). Heaviest to render when many threads are live.

### B. Mission Board

- **Metaphor:** an air-traffic board. Every run is a row, grouped by state.
- **Layout:** home = a dense keyboard list (Needs you / Review / Running / Done today) with children nested under parents; Space peeks, Enter opens the thread in a split. Regroup by project, host or agent.

```
 ST  AGENT         TASK                 THREAD          HOST    SPEC            TIME  LAST  DIFF
 !   >_ codex      db migration         db migration    vps-1   ‹model› ▂▄      12m   2m   +80 −4
 ?   ✻ claude      auth refactor        auth refactor   laptop  ‹model› ▂▄▆     18m   5m   +120 −31
 ●   >_ codex      └ tests              auth refactor   laptop  ‹model› ▂▄       1m   4s   +36
 ●   ✻ charles     docs sweep           docs            vps-1   ‹model› ▂▄▆      9m   12s
 ✓   ✻ claude      flaky retry test     flaky test      laptop  ‹model› ▂▄▆     21m   1h   +42 −10
```

- **Good:** the best answer to "what is running right now". Maps one-to-one onto the engine's summary stream, so it is cheap and easy to virtualise. The natural TUI form. Excellent for 5–30 parallel runs.
- **Bad:** feels like ops tooling, not teammates. The conversation is one hop away. It becomes the babysitting screen xAI avoided.

### C. Dispatch (Inbox + Threads) — **picked**

- **Metaphor:** a dispatcher's desk. A tray of what came back (the inbox), the crew on the wall (the roster), the board one key away, and each job in its own folder (a thread) with every hand-off clipped to it.
- **Layout:** a sidebar inbox ordered by what came back; a Home that shows the crew, what needs you, what is ready to review and live trees of what is running across hosts; a thread view with a centred timeline, hand-off cards, a child strip and a docked composer; right-panel tabs; the board as a flip of Home.
- **Good:** keeps Grok Bot's best ideas (identity, roster, the watch ladder) and fixes its worst (context bubbles per task, visible hand-offs, explicit model and effort). One data model for three clients. Borrows proven patterns (T3's inbox and status model, Antigravity's overview and artifacts, Codex's subagent strip).
- **Bad:** less novel than A; three panes on desktop need careful collapse; inbox semantics (read vs cleared, settle, snooze) must be exact or it turns into another notification pile.

**The pick is C, with the roster from A and the board key from B.**

Why:
1. The owner's top complaint is bugginess, and C maps directly onto D4 and D7: the inbox is the engine's attention list, Running is the summary stream, a thread is one subscription. No client-side guessing.
2. D7 fixes the home as Inbox + Now. A cannot provide that without bolting an inbox on; B provides it but loses the teammates.
3. The roster from A gives Grok Bot's "your team at home" without making it the primary navigation: a band at the top of Home, the participants in each thread header, and a full Agents page (03 §2.3).
4. The board key from B gives the dense view when you have twenty things running, without making it the default. One key flips Home to the board and back; it is the same data with a different layout.
5. C has the cleanest phone and TUI translations: phone = inbox root; TUI = inbox pane plus thread, with the board on one key.

**Why not "Deck Room":** "room" was v2's word for a thread (desktop-shell's `Room.tsx`, the lane strip). Reusing it for the whole workspace would confuse everyone who used v2. In v3 the words are **thread**, **agent**, **task**, **host**, **person**, and the home is just **Home**.

---

## 3. Dispatch in full

### 3.0 Window anatomy (desktop)

```
┌────────────┬──────────────────────────────────────────────┬──────────────────┐
│ sidebar    │ title bar 40                                  │ right panel      │
│ 256 (48)   ├──────────────────────────────────────────────┤ 380 (300–560)    │
│ inbox      │ main: Home, a thread, Review, Hosts, Agents   │ tabs + Follow    │
│ threads    │ timeline max 736, centred                     │                  │
│ hosts      │                                              │                  │
└────────────┴──────────────────────────────────────────────┴──────────────────┘
```

- **Sidebar** (256px, 48px collapsed): search and New, then **Needs you**, **Review**, **Threads** (came back), **Snoozed**, **Settled**, and a compact **Hosts** rail at the bottom (05 §3d). It is navigation: every thread appears once, as one row.
- **Main:** Home, a thread, Review, Hosts, Agents or Settings. No modal pages.
- **Right panel** (380px default): tabs **Work · Changes · Files · Terminal · Browser**, and a **Follow** pane pinned above the tab content when you follow something. Five tabs, down from nine side-panel views in desktop-shell (`research-desktop.md` §4). Graph, Tasks and Agents merge into **Work**; Preview merges into **Browser** (the host-side browser, D10).
- **Responsive:** below 1180px the right panel overlays the main area instead of pushing it. Below 900px the sidebar collapses to its 48px rail (icons plus status counts). The sidebar and an open right panel are never both expanded below 1024px, as Antigravity 2 reportedly does (snippet).
- **No status bar.** Host and link state live in the sidebar's Hosts rail, so there is one place for them.

### 3.1 Home: crew, Needs you, Review, Running across hosts

```
┌──────────────────────────────┬─────────────────────────────────────────────────────────────────────────────┐
│ ▌deck  ⌕ Search  ⌘K  [+ New] │ Home    3 need you · 2 to review · 5 running · 3 hosts       [Inbox│Board] │
├──────────────────────────────┼─────────────────────────────────────────────────────────────────────────────┤
│ NEEDS YOU                  3 │ CREW                                                          All agents →  │
│ ! db migration   vps-1    2m │ [>_]! codex   [✻]● claude   [>_]● reviewer   [✻]● charles   [▯]○ opencode   │
│ ? auth refactor  laptop   5m │ (A) ana · here on studio    (M) milo · away                                 │
│ ◆ billing export vps-1   12m │                                                                             │
│ REVIEW                     2 │ NEEDS YOU                                                                   │
│ ✓ flaky test     laptop   1h │ ┌─────────────────────────────────────────────────────────────────────────┐ │
│ ✓ auth: tests    laptop   4m │ │ ! [>_] codex wants to run   sqlc generate && go test ./db/...           │ │
│ THREADS          came back ▾ │ │   db migration · deck/api · on vps-1 · waiting 2m     [Deny d] [Approve y]│ │
│ ● auth refactor  laptop      │ ├─────────────────────────────────────────────────────────────────────────┤ │
│ ● docs sweep     vps-1       │ │ ? [✻] claude asks: keep v1 tokens for the phone app?                    │ │
│ ‖ nightly deps   vps-2       │ │   auth refactor · deck/api · on laptop · 5m                             │ │
│ ✗ release notes  laptop   3h │ │   ( Yes, until Friday )  ( No, cut over now )  [Reply r]                │ │
│ ▸ Snoozed 1                  │ ├─────────────────────────────────────────────────────────────────────────┤ │
│ ▸ Settled                    │ │ ◆ [>_] reviewer has a plan for billing export · 6 steps, 2 hand-offs    │ │
│                              │ │   billing export · deck/web · on vps-1 · 12m           [Review plan ⏎] │ │
├──────────────────────────────┤ └─────────────────────────────────────────────────────────────────────────┘ │
│ HOSTS                        │ REVIEW                                                                      │
│ ● laptop   local       2 run │ ✓ [✻] claude → you   flaky retry test · +42 −10 · 3 files       [Review]  │
│ ● vps-1    tailnet 18ms 3 run│ ✓ [>_] codex → claude tests for auth v2 · 12 passing · on laptop [Open]    │
│ ◌ vps-2    offline 3m        │ RUNNING · grouped by host ▾                                                 │
│ + Add a host                 │ laptop  ● [✻] claude    auth refactor   editing handlers/auth.go   4m · 3s │
│                              │           └ ● [>_] codex  tests        go test ./auth/...          1m · 1s │
│                              │ vps-1   ● [✻] charles   docs sweep      reading 14 files           9m · 12s│
│                              │           └ ● [✻] map links (subagent) checking 212 URLs           2m · 2s │
│                              │ vps-2   ◌ offline 3m · nightly deps was running · last seen 10:39  [Wake]  │
└──────────────────────────────┴─────────────────────────────────────────────────────────────────────────────┘
```

- **Crew band:** agents (rounded squares with their CLI mark) then people (circles with initials). The badge on each avatar is the agent's rolled-up status (03 §4.2). Hover shows the current action and threads; click filters Running to that agent; "All agents →" opens the Agents page (03 §2.3). It wraps to at most two rows; beyond that it shows the most recently active and "+6". People show presence in words ("here on studio", "away"), not a status colour.
- **Needs you:** one card per attention item (03 §4.3), answerable in place. Each card names the agent, the thread, the project, the host and how long it has waited. Destructive requests a member cannot answer say "Needs an admin" instead of showing buttons (D15).
- **Review:** results you own, one line each, with the route (`codex → claude`, `claude → you`) and a diff stat.
- **Running:** live trees, one line per node, showing the current action and "elapsed · last progress". Grouped by host by default (`g` cycles host / project / agent / none). A node with no progress for 5 minutes shows "no progress 6m" in amber (03 §4.2).
- **Board flip:** `⌘⇧B` or the `[Inbox│Board]` switch replaces the three bands with direction B's table (same rows, one per run, grouped by state, regroup with `g`). The choice is remembered per window.
- **Empty bands disappear.** If nothing needs you, the band is replaced by one line: "Nothing needs you. 5 running on 3 hosts." (§5.3).

### 3.2 A thread

```
┌──────────────┬────────────────────────────────────────────────────────┬──────────────────────────────────┐
│ NEEDS YOU  3 │ auth refactor ✻a7f  deck/api · ⎇ auth-v2 · on laptop ▾  │ Work  Changes  Files  Term  Browser│
│ ! db migr…   │ [✻][>_][>_] (A)(M)                       Hand to ▾   ⋯  │──────────────────────────────────│
│ ? auth ref…  │────────────────────────────────────────────────────────│ ▾ auth refactor   18m · 62% budget│
│ REVIEW     2 │                                                        │ ● [✻] claude    lead              │
│ ✓ flaky…     │  you                                                   │   editing handlers/auth.go        │
│ THREADS      │  Move auth to v2 tokens. Keep the phone app working.   │ ├ ✓ [✻] map call sites  3m        │
│ ● auth ref…  │                                                        │ ├ ● [>_] codex  tests   1m        │
│ ● docs swe…  │  [✻] claude                                            │ │   [Follow f] [Open ⏎]           │
│              │  I'll map the call sites, rewrite the handlers and     │ └ ○ [>_] reviewer  queued         │
│              │  hand the tests to codex.                              │      after "tests"                │
│              │  ▸ Read 6 files, ran 2 commands                  0:41  │                                  │
│              │  ✻ claude · ‹model› · high · thread          4m · $0.22│ BOARD  1 of 4 done   [Tree│Board] │
│              │                                                        │ ✓ map call sites      claude      │
│              │  ┌ hand-off ─────────────────────────────── running ┐  │ ● rewrite handlers    claude      │
│              │  │ [✻] claude → [>_] codex    task t3 · sub-thread  │  │ ● tests               codex       │
│              │  │ Write tests for v2 token refresh                 │  │ ○ review              reviewer    │
│              │  │ Done when: go test ./auth/... passes             │  │                                  │
│              │  │ ‹model› · medium (inherited from claude)         │  │                                  │
│              │  │ ● go test ./auth/...  1m · last progress 1s      │  │                                  │
│              │  │                      [Peek ␣] [Follow f] [Open ⏎]│  │                                  │
│              │  └──────────────────────────────────────────────────┘  │                                  │
│              │  (A) ana   can we keep v1 for the phone until Friday?  │                                  │
│              │  ┌ result ───────────────────────────────── 3m ─────┐  │                                  │
│              │  │ ✓ [✻] map call sites → claude    subagent        │  │                                  │
│              │  │ 23 call sites in 9 files; 4 need care.           │  │                                  │
│              │  │                                   [Show report]  │  │                                  │
│              │  └──────────────────────────────────────────────────┘  │                                  │
│              │ ┌────────────────────────────────────────────────────┐ │                                  │
│              │ │ ✓ map  ● [>_] tests 1m  ○ [>_] reviewer   Stop all │ │ ← child strip                    │
│              │ ├────────────────────────────────────────────────────┤ │                                  │
│              │ │ Message claude…                                    │ │                                  │
│              │ │                                                    │ │                                  │
│              │ │ [✻ claude · ‹model› · ▂▄▆] ask ▾  ⎇ auth-v2   ⏎ send│ │                                  │
│              │ └────────────────────────────────────────────────────┘ │                                  │
└──────────────┴────────────────────────────────────────────────────────┴──────────────────────────────────┘
```

- **Header:** title, the corner index (`✻a7f`: the lead's mark plus a short thread id in mono, faint; it is also what `deck open a7f` and the palette accept), project, branch or worktree, and **"on laptop ▾"**, which opens Move to host… and "Keep running on vps-1 when the lid closes" (D17, 05 §3e). The second line is the participants: the lead, then agents with live runs, then people present, then "+N". **Hand to ▾** delegates from here as well as from the composer.
- **Timeline:** centred, at most 736px (T3's 46rem). Agent prose has no container (a desktop-shell rule worth keeping). Your turn is a block on `surface` with a 2px rail in `text_faint`. A teammate's turn shows their circle and name; their messages go to the thread's agent directly (D8). Runs of tool calls fold to one row ("Read 6 files, ran 2 commands"), as T3's activity log does; it opens to each command, its exit code and its output.
- **Reply footer:** every agent reply ends with `mark · agent · ‹model› · effort · source` and `elapsed · cost` in faint (03 §3.7). Hover expands the sources.
- **Hand-off card:** one card per task, updated in place from typed events (03 §2.6). It goes through three states without moving: *proposed* (when the delegation policy is `ask`: the per-child agent and spec can be edited, with Approve), *running* (current action, elapsed, last progress, budget), and *result* (summary, check, diff stat, Accept / Send back / View diff). Visually: `elevated` fill, 8px radius, a 2px left rail in the delegate's identity colour, the route `from → to` in the header. No shadow inside the timeline.
- **Result card:** the same card resolved. A subagent's result shows its report; a sub-thread's result also has Open.
- **Child strip** (03 §4.6): one 28px row docked on top of the composer, shown only when the thread has live or unreviewed children. One chip per direct child (mark, name, status, elapsed); deeper descendants show as "+2". Click = Peek popover; `f` = Follow; Enter = Open. Finished children stay dimmed until reviewed, then leave the strip.
- **Composer:** docked at the bottom of the timeline column, `elevated` fill, 8px radius, `border_strong` edge, focus ring in the accent. On a new thread it starts as a large centred hero composer that docks after the first send (T3's pattern). It has exactly three controls: the **agent · model · effort** chip, the **mode** chip (ask / plan / yolo, capped by role and profile), and the **workspace** chip (branch or worktree). `⏎` sends, `⇧⏎` new line, `⌘⇧⏎` **Hand to…**, `/hand reviewer …` does the same.
- **Agent · model · effort chip:** `[✻ claude · ‹model› · ▂▄▆]`. Segments are separately clickable (agent, model, effort bars). Clicking an effort bar sets that level in one click; `⌥E` cycles effort; `⌘L` opens the picker (§3.6). Below 560px of composer width the model name hides first, then the agent name; the mark and the bars always stay.
- **Right tabs:** **Work** (the delegation tree, §3.3, with a Tree│Board toggle for the task board), **Changes** (the thread's diff, with Review), **Files**, **Terminal** (on the thread's host), **Browser** (frames from the host-side Chromium, D10).

### 3.3 The delegation tree, with Follow and Open

The Work tab, or full width with `⌘⇧T`.

```
┌ Work · auth refactor ─────────────── 4 agents · on laptop · 18m · budget 62% ──── [Tree│Board] ┐
│ ● [✻] claude          lead         editing handlers/auth.go        ‹model› high     claims auth/* │
│ ├ ✓ [✻] map call sites subagent    done 3m · 23 call sites                       [Report]        │
│ ├ ● [>_] codex "tests" sub-thread  go test ./auth/...  1m · 1s     ‹model› medium  inherited      │
│ │ └ ✓ [>_] fixtures    subagent    done                                                           │
│ └ ○ [>_] reviewer     sub-thread   queued · starts after "tests"   ‹model› high    profile        │
├───────────────────────────────────────────────────────────────────────────────────────────────────┤
│ FOLLOWING  [>_] codex "tests" · on laptop · live                         [Open ⏎]  [Unfollow f]  │
│ $ go test ./auth/...                                                                              │
│ --- FAIL: TestRefreshRotates (0.02s)                                                              │
│     refresh_test.go:48: got token v1, want v2                                                     │
│ editing auth/refresh_test.go                                                                      │
└───────────────────────────────────────────────────────────────────────────────────────────────────┘
```

| Level | What you see | How |
|---|---|---|
| **Indicator** | Status glyph, one current-action line, elapsed and last progress. Hover shows the last three actions. | Always, on every row |
| **Peek** | The row expands in place: last 6 lines of output, the current tool, a Steer box, Stop | `Space`, or click the chevron |
| **Follow** | A live preview pinned above the right panel's tab content: terminal output, the diff so far, or browser frames. It stays as you move between threads. One follow at a time on desktop; following another replaces it. | `f`, or Follow on a card, chip or row |
| **Open** | Go to that node. A **sub-thread** opens as a thread you can talk to. A **subagent** opens read-only (its transcript, Steer and Stop): subagents report to their parent and are not chatted with (03 §4.6). | `⏎` |

- Follow is never on by default. When you follow something on another host, the pane header names the host.
- The tree uses fixed 28px rows so GPUI's `uniform_list` can render it (Zed source, not re-checked). `j/k` move, `h/l` fold, `x` stops (with a confirm row naming what stops, children included).
- The **Board** toggle shows the same tasks as columns (queued, running, review, done) with owners. It is the old Tasks tab, now one view of the Work tab.
- Each node shows its spec and the source of it, so an inherited spec is never silent (D5).

### 3.4 Review: artifacts, per-hunk accept or reject, batched comments

Opened from a result card, a Review row, or the Changes tab.

```
┌ Review · flaky retry test · [✻] claude → you · on laptop · +42 −10 · 3 files ── [Send back] [Accept all ⌘⏎] ┐
│ ARTIFACTS              │ internal/runner/retry_test.go                       hunk 2 of 5    [Unified│Split] │
│ ◆ Plan          4/4    │ @@ -38,7 +38,9 @@ func TestRetry(t *testing.T)           [Reject x] [Accept y]   │
│ ▤ Walkthrough          │  38      r := New(cfg)                                                           │
│ ▦ Changes   3 files    │  40    - time.Sleep(10 * time.Millisecond)                                       │
│   ✓ retry.go    +12    │  40    + clock.Advance(10 * time.Millisecond)                                    │
│   ● retry_test  +28 −8 │        ┌ you · draft ─────────────────────────────────────────────┐              │
│   ○ clock.go    +2 −2  │        │ Inject the clock through New() instead of a package var. │              │
│ ▣ Screens       2      │        └──────────────────────────────────────────── [Delete] ────┘              │
│ ▸ Commands      7      │  41    + if !r.Done() { t.Fatal("not done after advance") }                      │
│ ▸ Check  go test ✓     │                                                                                  │
├────────────────────────┴──────────────────────────────────────────────────────────────────────────────────┤
│ 2 comments · 1 rejected hunk → one message to claude              [Discard drafts]   [Send back to claude ⏎]│
└────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

- **Artifacts** on the left: Plan, Walkthrough, Changes (by file, with each file's review state), Screens, Commands (the run's command log with exit codes), Check (the done-when check). This is Antigravity 2's artifact list (snippet) with Deck's own items.
- **Per hunk:** `y` accepts, `x` rejects, `j/k` move between hunks, `c` comments on the selected line. File rows show ✓ all accepted, ● partly reviewed, ○ untouched. Rejecting a hunk reverts it in the task's worktree (engine op) and adds a line to the outgoing message ("Rejected hunk 2 in retry_test.go").
- **Comments are drafts** until sent. They persist across restarts, attach to the next message, clear when it is sent, and come back if the send fails. Antigravity 2 reportedly behaves the same way (snippet). The footer always says what will be sent and to whom.
- **Accept all** applies the task's worktree to the thread's tree, with undo, and marks the task done (03 §2.6). **Send back** sends the batched message and returns the task to running.
- On the phone Review is read and comment only (§3.9); accepting needs desktop or TUI, or an explicit confirm on the phone.

### 3.5 Hosts: the fleet, and Add a host

```
┌ Hosts ──────────────────────────────────────────────────────────────────────────────── [+ Add a host] ┐
│ HOST       ROUTE                    CLIs                    THREADS          PEOPLE        VERSION    │
│ ● laptop   local                    ✻ ✓   >_ ✓   ▯ —        4 · 2 running    you           3.0.2      │
│ ● vps-1    tailnet · direct 18 ms   ✻ ✓   >_ ! sign in      3 · 2 running    ana viewing   3.0.2      │
│ ◌ vps-2    offline 3m               ✻ ✓   >_ ✓              1 · was running  —             3.0.1  ↑   │
│ ● studio   relay vps-1 62 ms        ✻ ✓                     2 · 1 running    ana's · member 3.0.2     │
├───────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ vps-1 · deck 3.0.2 · systemd user unit · up 6 days                          [Terminal] [Update] [⋯]  │
│ Route   tailnet · direct 18 ms     (tried: socket — · tailnet ✓)                                      │
│ CLIs    ✻ claude   signed in                                                                          │
│         >_ codex   not signed in                                  [Sign in on vps-1]                  │
│ Agents  [>_] reviewer · always on      [✻] charles                                                    │
│ People  you · admin     (A) ana · member     (M) milo · viewer                         [Invite…]     │
│ Threads ● docs sweep   ! db migration   ◆ billing export                                              │
└───────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

- One row per host: state glyph, name (teammates' hosts read "ana's studio"), route badge in 05's exact words (05 §2.3), CLI sign-in per kind (mark plus ✓ / ! sign in / — not installed), threads, people present, Deck version with `↑` when it differs from this client's.
- Selecting a host shows its detail below the list. **Sign in on vps-1** opens a terminal on that host running the CLI's own login; Deck never reads credentials (D16, 05 §3g).
- An offline host is greyed, says how long, what was running, and offers **Wake via SSH** when it has an SSH alias (05 §3d).

**Add a host** is a sheet (05 §3a owns the steps; this is the look):

```
┌ Add a host ──────────────────────────────────────────────────────────────────┐
│ 1 Box       From ~/.ssh/config   [ vps-3   deploy@203.0.113.7         ▾ ]    │
│             or paste a link / scan the QR from `deck host up`                │
│ 2 Check     ✓ ssh   ✓ linux/amd64   ✓ ~/.local/bin   ✓ systemd --user        │
│ 3 Install   deck 3.0.2 → ~/.local/bin/deck  (no root)              ● 12s     │
│ 4 Keys      host key pinned · this device pinned on vps-3 over SSH           │
│ 5 Network   ● Tailscale on vps-3 (recommended)   ○ embedded   ○ public       │
│             ○ SSH only                ☐ also run a relay for phones          │
│ 6 CLIs      ✻ claude  not signed in  [Sign in]    >_ codex  not installed    │
│                                                      [Cancel]  [Add vps-3]   │
└──────────────────────────────────────────────────────────────────────────────┘
```

- Steps fill in as they finish; a failed step shows the command, its stderr, Retry and "Open a terminal on vps-3". Nothing to type except the choice of box and network.

### 3.6 The picker popover

Opened from the composer chip (`⌘L`, or clicking a segment), a child chip, a tree node or a hand-off card. Columns are the chip's segments; focus starts on the segment you clicked. The rules are 03 §3.2 and §3.5; this is the look.

```
┌ claude · this thread ─────────────────────────────────────────────────────────────┐
│ ⌕ Search agents and models                                                        │
├────────────────────────┬────────────────────────────────────┬─────────────────────┤
│ AGENTS                 │ MODELS · claude on laptop          │ EFFORT              │
│ ★ [>_] reviewer   ●    │ ● Default · ‹model›   (profile)    │   Default · medium  │
│   [✻] charles     ●    │   ‹model›   fast, cheaper          │ ▂       low         │
│ ─────────────────────  │   ‹model›   strongest reasoning    │ ▂▄      medium      │
│ › [✻] claude      ●    │   ‹model›   long context           │ ▂▄▆     high    ●   │
│   [>_] codex   82% ▮▮▯ │                                    │ max                 │
│   [▯] opencode  ! sign │                                    │                     │
│   [π] pi          —    │                                    │                     │
├────────────────────────┴────────────────────────────────────┴─────────────────────┤
│ catalog 2h ago · refreshing…                           Save to profile "charles"  │
│ ←/→ column · ↑/↓ move · ⏎ apply · esc close            Defaults live in Settings  │
└───────────────────────────────────────────────────────────────────────────────────┘
```

- **Title** names the target and scope: "claude · this thread", "codex "tests" · this run", "reviewer · profile".
- **Agents rail:** named agents (★ favourites first), then built-in kinds. Each shows readiness **on the thread's host**: ● ready, `82% ▮▮▯` plan window used, `! sign in`, `— not installed`.
- **Models:** "Default · ‹resolved›" is always the first row and can always be chosen again (03 §3.2). Each row has a one-phrase hint, never a raw slug alone.
- **Effort:** the EffortGlyph, one rising bar per level the model supports, filled to the level, unfilled bars in `text_faint` (T3 `EffortGlyph.tsx`). Levels without bars ("max") use their name.
- Clicking or `⏎` applies at once; there is no Apply button and Escape never loses a change you applied. Choosing a model moves focus to Effort and keeps the popover open (T3's behaviour). Choosing a different agent for the lead shows an inline confirm row: "Hand the thread to codex · it gets a brief of the history · [Hand off] [Cancel]".
- The footer shows catalog freshness (it never blocks; D5) and "Save to profile" when the target is a named agent's run. Global defaults are only in Settings.
- 140ms enter, 12px radius, `elevated` fill, shadow level 3 (§4.8).

### 3.7 The command palette

`⌘K`. One list, sections in this order: Commands, Threads, Agents, Hosts, Files (current thread). Each command shows its key.

```
┌ ⌘K ──────────────────────────────────────────────────────────────────────┐
│ hand rev                                                                 │
├──────────────────────────────────────────────────────────────────────────┤
│ COMMANDS                                                                 │
│ › Hand to reviewer                                          ⌘⇧⏎ then r   │
│   Hand to an agent…                                         ⌘⇧⏎          │
│ THREADS                                                                  │
│   ‖ handlers review   ✻b21 · deck/api · on vps-1 · 2d                    │
│ AGENTS                                                                   │
│   [>_] reviewer  Reviews diffs for correctness; read-only · vps-1        │
│ HOSTS                                                                    │
│   ● vps-1  tailnet · direct 18 ms                                        │
├──────────────────────────────────────────────────────────────────────────┤
│ ↑↓ move · ⏎ run · ⇥ next section · > commands  @ agents  : hosts  # files│
└──────────────────────────────────────────────────────────────────────────┘
```

- Prefixes narrow the list: `>` commands, `@` agents and people, `:` hosts, `#` files. A corner index (`b21`) jumps straight to that thread.
- Fuzzy match on words; recent items first when the query is empty.
- Every action in the app is a palette command, so the palette is also the key reference (Zed's pattern).

### 3.8 Desktop keymap (final)

`⌘` is Ctrl on Linux and Windows. "Nav focus" means the composer or another text field does not have focus (Escape leaves the composer).

| Key | Action |
|---|---|
| `⌘K` | Palette |
| `⌘N` | New thread (hero composer, "Runs on" defaults per 05 §3e) |
| `⌘0` / `⌘1…9` | Home / the Nth thread in the sidebar |
| `⌃⌥L`, or `n` in nav focus | Next item that needs you, across hosts (`N` previous) |
| `⌘⇧B` | Flip Home between Inbox and Board |
| `⌘⇧T` | Work tree full width |
| `⌘\` | Toggle the right panel |
| `⌘L` / `⌥E` | Picker / cycle effort of the focused target |
| `⌘⇧⏎` | Hand to… |
| `Space` / `f` / `⏎` (nav focus) | Peek / Follow / Open |
| `y` / `d` / `r` (nav focus, on a request) | Approve / Deny (with an optional reason) / Reply |
| `x` (nav focus) | Stop the focused run (confirm row) |
| `s` / `z` (nav focus, on a thread row) | Settle / Snooze, each with a 5 s Undo (`⌘Z`) as T3 does |
| `Esc` | Close the top layer only |

### 3.9 TUI translation

The TUI uses the same regions, the same glyphs (§5.1), the same marks and the same words. It never paints a full background: it uses the terminal's own and paints only selected rows, cards and the composer.

**(a) Home** (`alt+n` toggles it from anywhere; 120×32 shown)

```
 ▌deck  Home · 3 need you · 2 review · 5 running · 3 hosts                     b board  n next  ? keys
 CREW  >_! codex  ✻● claude  >_● reviewer  ✻● charles  ▯ opencode   @ana here  @milo away
 ───────────────────────────────┬───────────────────────────────────────────────────────────────────────
 NEEDS YOU                    3 │ NEEDS YOU
 ! db migration    vps-1    2m  │›! >_ codex     db migration   vps-1   2m  run: sqlc generate && go te…
 ? auth refactor   laptop   5m  │ ? ✻ claude     auth refactor  laptop  5m  keep v1 tokens for the phone?
 ◆ billing export  vps-1   12m  │ ◆ >_ reviewer  billing export vps-1  12m  plan: 6 steps, 2 hand-offs
 REVIEW                       2 │ REVIEW
 ✓ flaky test      laptop   1h  │ ✓ ✻ claude → you    flaky retry test   laptop  +42 −10 · 3 files
 ✓ auth: tests     laptop   4m  │ ✓ >_ codex → claude tests for auth v2  laptop  12 passing
 THREADS                        │ RUNNING · host
 ● auth refactor   laptop       │ laptop  ● ✻ claude    auth refactor  editing handlers/auth.go   4m 3s
 ● docs sweep      vps-1        │           └● >_ codex tests          go test ./auth/...         1m 1s
 ‖ nightly deps    vps-2        │ vps-1   ● ✻ charles   docs sweep     reading 14 files           9m 12s
 ✗ release notes   laptop   3h  │ vps-2   ◌ offline 3m · nightly deps was running
 ───────────────────────────────┴───────────────────────────────────────────────────────────────────────
 ● laptop  ● vps-1 18ms  ◌ vps-2 offline 3m      y approve  d deny  r reply  ␣ peek  f follow  ⏎ open
```

- `b` flips the right side to the Board (direction B's table); `g` cycles grouping; `b` again flips back. Below 100 columns the left list hides and Home is one column.
- People are `@name` in the TUI (no circles in a terminal); agents are their mark and name.

**(b) A thread**

```
 auth refactor ✻a7f · deck/api · ⎇ auth-v2 · on laptop      ✻ >_ >_ @ana @milo       t work  ? keys
 ──────────────────────────────────────────────────────────────────────────────────────────────────
 you  Move auth to v2 tokens. Keep the phone app working.

 ✻ claude
 I'll map the call sites, rewrite the handlers and hand the tests to codex.
 ▸ read 6 files, ran 2 commands                                                              0:41
 ✻ claude · ‹model› · high · thread                                                   4m · $0.22
 ┌ hand-off ✻ claude → >_ codex · t3 · sub-thread ─────────────────────────────────── running ┐
 │ Write tests for v2 token refresh · done when go test ./auth/... passes                     │
 │ ‹model› · medium (inherited) · ● go test ./auth/...  1m · 1s          ␣ peek  f follow  ⏎ open│
 └─────────────────────────────────────────────────────────────────────────────────────────────┘
 @ana  can we keep v1 for the phone until Friday?
 ┌ result ✓ ✻ map call sites → claude · subagent · 3m ─────── 23 call sites in 9 files ⏎ report ┐
 ──────────────────────────────────────────────────────────────────────────────────────────────────
 children: ✓ map  ● >_ tests 1m  ○ >_ reviewer queued                          alt+1…9 peek a child
 > Message claude…
 ✻ claude · ‹model› · ▂▄▆ high · ask · ⎇ auth-v2              ctrl+l picker  alt+e effort  alt+⏎ hand
```

- The lead line under the editor stays (`internal/ui/model/lead.go:121-158`): who you are talking to, always visible.
- Effort bars use `▁▂▃▄▅▆▇`, with unfilled levels dimmed, matching the desktop glyph.

**(c) The delegation tree** (`t` in a thread)

```
 Work · auth refactor · 4 agents · on laptop · 18m · budget 62%                 B board  t close
 › ● ✻ claude          lead         editing handlers/auth.go          ‹model› high
   ├ ✓ ✻ map call sites subagent    done 3m · 23 call sites
   ├ ● >_ codex "tests" sub-thread  go test ./auth/...  1m · 1s       ‹model› medium  inherited
   │ └ ✓ >_ fixtures    subagent    done
   └ ○ >_ reviewer     sub-thread   queued · after "tests"            ‹model› high    profile
 ── following >_ codex "tests" · on laptop ──────────────────────────────── ⏎ open  f unfollow ──
 $ go test ./auth/...
 --- FAIL: TestRefreshRotates (0.02s)
     refresh_test.go:48: got token v1, want v2
```

- Follow is a split under the tree; it takes the lower third and keeps updating while you move.

**(e) Hosts** (`H` in nav focus)

```
 Hosts                                                         a add  s sign in  i invite  ⏎ details
 › ● laptop   local                   ✻✓ >_✓ ▯—      4 threads · 2 running   you
   ● vps-1    tailnet · direct 18 ms  ✻✓ >_!         3 threads · 2 running   ana viewing
   ◌ vps-2    offline 3m              ✻✓ >_✓         1 thread  · was running          w wake via SSH
   ● studio   relay vps-1 62 ms       ✻✓             ana's · member · 2 threads
```

- `a` runs the Add a host steps inline, one step per line, the same as the desktop sheet; `deck host add <alias>` does the same from a shell.

**(f) The picker** (`ctrl+l`; `alt+m` opens it on Models)

```
 claude · this thread                                                   catalog 2h · refreshing
 / search
 AGENTS               │ MODELS · claude on laptop        │ EFFORT
 ★ >_ reviewer   ●    │ ● Default · ‹model› (profile)    │   Default · medium
   ✻ charles     ●    │   ‹model›  fast, cheaper         │   ▁    low
 › ✻ claude      ●    │   ‹model›  strongest reasoning   │   ▁▂   medium
   >_ codex  82% ▮▮▯  │   ‹model›  long context          │ › ▁▂▃  high
   ▯ opencode ! sign  │                                  │   max
 tab/←→ column · ↑↓ move · ⏎ apply · p save to profile · esc close
```

- Non-modal and docked above the editor (03 §3.5). `alt+e` cycles effort without opening it.

**TUI keymap (final).** "Any focus" works while typing; "nav focus" works after `esc`.

| Key | Focus | Action |
|---|---|---|
| `alt+n` | any | Home (Inbox + Now) on / off |
| `alt+j` | any | Next item that needs you, across hosts |
| `n` / `N` | nav | Next / previous item that needs you |
| `b` / `g` | nav, Home | Flip to the Board and back / cycle grouping |
| `j` `k` `⏎` `space` `f` | nav | Move, open, peek, follow |
| `y` / `d` / `D` / `r` | nav, on a request | Approve / deny / deny with a reason / reply (deny moves from `n` to `d`, because `n` is next) |
| `x` / `s` | nav, on a run | Stop (confirms) / steer |
| `t` / `H` / `A` | nav | Work tree / Hosts / Agents |
| `S` / `z` | nav, on a thread | Settle / snooze (undo with `u`) |
| `ctrl+l` / `alt+m` / `alt+e` | any | Picker / picker on Models / cycle effort |
| `alt+⏎` | composer | Hand to… (also `/hand <agent> …`) |
| `alt+1…9` | any | Peek child N of this thread |
| `ctrl+p` | any | Palette (kept from today) |
| `?` | nav | Keys for the current view |

Only `●` animates, at a stepped 2–3 frames per second, and only when it is on screen; nothing else triggers a redraw while idle (the TUI already idles at ~0 B/s, `research-tui.md` "What the TUI does well").

### 3.10 Phone translation

Tabs: **Inbox** (root) · **Now** · **Agents** (D19). Hosts open from the header chip. A thread pushes on the stack from any row.

```
 Inbox (root)                          Now                                  Agents
┌──────────────────────────────────┐ ┌──────────────────────────────────┐ ┌──────────────────────────────────┐
│ Inbox          ● 2 hosts ◌ 1  ⚙  │ │ Now               group: host ▾  │ │ Agents                    + New  │
│ Needs you 3 · Review 2           │ │ LAPTOP                           │ │ [>_] reviewer         ● 1 thread │
│ ┌──────────────────────────────┐ │ │ ● ✻ claude  auth refactor   4m   │ │ Reviews diffs; read-only · vps-1 │
│ │ ! [>_] codex · db migration  │ │ │   editing handlers/auth.go       │ │ [✻] charles           ● docs     │
│ │ sqlc generate && go test …   │ │ │   └● >_ tests  go test …   1m    │ │ Writes and checks docs · vps-1   │
│ │ vps-1 · 2m                   │ │ │ VPS-1                            │ │ [✻] claude            ● 1 thread │
│ │ [ Deny ]       [ Approve ]   │ │ │ ● ✻ charles docs sweep      9m   │ │ General agent · laptop           │
│ └──────────────────────────────┘ │ │   reading 14 files               │ │ [>_] codex            ! 1        │
│ ┌──────────────────────────────┐ │ │ VPS-2                            │ │ PEOPLE                           │
│ │ ? [✻] claude · auth refactor │ │ │ ◌ offline 3m · nightly deps      │ │ (A) ana  here on studio          │
│ │ Keep v1 tokens for the phone?│ │ │                                  │ │ (M) milo away                    │
│ │ (Yes, until Friday) (No)     │ │ │                                  │ │                                  │
│ └──────────────────────────────┘ │ │                                  │ │ tap: New thread · Hand a task ·  │
│ REVIEW                           │ │ tap: thread · hold: Follow       │ │      Edit spec                   │
│ ✓ flaky retry test  +42 −10  1h  │ │                                  │ │                                  │
├──────────────────────────────────┤ ├──────────────────────────────────┤ ├──────────────────────────────────┤
│  ▣ Inbox      ● Now     ☺ Agents │ │  ▣ Inbox      ● Now     ☺ Agents │ │  ▣ Inbox      ● Now     ☺ Agents │
└──────────────────────────────────┘ └──────────────────────────────────┘ └──────────────────────────────────┘
```

```
 Thread                                Picker (bottom sheet)                 Notification
┌──────────────────────────────────┐ ┌──────────────────────────────────┐ ┌──────────────────────────────────┐
│ ‹ auth refactor     on laptop  ⋯ │ │              ───                 │ │ deck · vps-1                 now │
│ [✻][>_] (A)                      │ │ claude · this thread             │ │ codex wants to run               │
│ you                              │ │ AGENT                            │ │ sqlc generate && go test ./db/…  │
│ Move auth to v2 tokens…          │ │ [✻ claude][>_ codex][>_ review…] │ │ db migration · deck/api          │
│ [✻] claude                       │ │ MODEL                            │ │ [ Approve ]   [ Deny ]           │
│ I'll map the call sites…         │ │ ● Default · ‹model› (profile)    │ └──────────────────────────────────┘
│ ▸ 6 files read, 2 commands       │ │ ○ ‹model›  fast, cheaper         │ ┌──────────────────────────────────┐
│ ┌ hand-off ✻ → >_ codex ───────┐ │ │ ○ ‹model›  strongest reasoning   │ │ deck · laptop                1m  │
│ │ Write tests for v2 refresh   │ │ │ EFFORT                           │ │ claude asks: keep v1 tokens for  │
│ │ ● go test …  1m   [Follow]   │ │ │ [Default][▂ low][▂▄ med][▂▄▆ hi] │ │ the phone app?                   │
│ └──────────────────────────────┘ │ │                                  │ │ [ Yes, until Friday ]  [ No ]    │
│ ✓ map  ● tests 1m  ○ reviewer  › │ │ Applies to this thread only.     │ └──────────────────────────────────┘
│ ┌──────────────────────────────┐ │ │ Defaults change in Settings on   │ ┌──────────────────────────────────┐
│ │ Message claude…              │ │ │ a computer.                      │ │ deck · vps-1  destructive        │
│ │ [✻ claude · ▂▄▆]  ask  ↑     │ │ │                                  │ │ reviewer wants to run rm -rf …   │
│ └──────────────────────────────┘ │ │                                  │ │ [ Review in Deck ]               │
└──────────────────────────────────┘ └──────────────────────────────────┘ └──────────────────────────────────┘
```

- **Inbox root:** Needs you cards are answerable in place, questions included (they are a dead end today, `research-mobile.md` B1). Review rows open a read-and-comment Review.
- **Now:** live trees grouped by host. Tap opens the thread; long-press (or the Follow button) opens **Follow** as a half-height sheet with live output; drag it up for full screen; Open goes to the thread.
- **Agents:** the roster with descriptions and people with presence. Tap offers New thread, Hand a task, Edit spec (03 §2.3).
- **Thread:** the same timeline and cards. The child strip is a horizontal chip row above the composer. An approval for this thread is a compact pill that expands into a sheet; it never docks over the transcript (`research-mobile.md` B5).
- **Picker:** one bottom sheet with three stacked sections; every tap applies at once; tapping the effort bars on the chip cycles effort with a haptic tick and Undo (03 §3.5). The phone never writes defaults (D19).
- **Notifications:** normal-tier approvals and simple single-choice questions are answered from the notification. Destructive approvals show only "Review in Deck" and need a passkey in the app (D15). Running work is never pushed.
- **Phone sizes:** chat 16pt (no zoom on focus), UI 15pt, meta 13pt, touch targets at least 44pt.

---

## 4. Design tokens

One light theme and one dark theme, following the system by default. No theme catalog, no art directions, no mix-and-match appearance settings (D18). The values below are the source; the GPUI app, the TUI and the phone generate their themes from one file, `design/tokens.json`, so a value changes in one place.

### 4.1 Colour roles

| Role | Light | Dark | Used for |
|---|---|---|---|
| `background` | `#FAFAF9` | `#0B0B0C` | App canvas, timeline |
| `surface` | `#F4F4F2` | `#111113` | Sidebar, right panel, your turn's block |
| `elevated` | `#FFFFFF` | `#19191C` | Cards, composer, popovers, sheets |
| `hover` | `#0000000A` | `#FFFFFF0A` | Row and control hover (alpha over any surface) |
| `selected` | `#E7E7E4` | `#222226` | Selected row, active tab (solid, so contrast can be checked) |
| `border` | `#E4E4E0` | `#FFFFFF14` | Hairlines between regions (decorative) |
| `border_strong` | `#8A8A91` | `#6A6A72` | Edges of inputs, the composer and toggles (≥ 3:1) |
| `text` | `#18181B` | `#EDEDEF` | Body text, titles, the working glyph |
| `text_muted` | `#5E5E66` | `#A0A0A8` | Secondary text, labels |
| `text_faint` | `#6B6B73` | `#85858D` | Meta: timestamps, footers, kbd hints. Not used on `selected`; meta on a selected row switches to `text_muted`. |
| `text_disabled` | `#A3A3AA` | `#56565D` | Disabled controls only (exempt from contrast rules) |
| `accent` | `#0B7A68` | `#3DC2A8` | Focus ring, primary button, links, the selected sidebar marker. Never a status. |
| `on_accent` | `#FFFFFF` | `#06201B` | Text on the primary button |

The accent is a green-teal at hue ≈ 170°: not T3 Code's indigo (≈ 264°, `apps/web/src/index.css`), not Google's blue, not Grok's purple. One primary button per view.

### 4.2 Status colours

| Role | Light | Dark | Tint background (light / dark) | Meaning |
|---|---|---|---|---|
| `attention` | `#9A5700` | `#F0B13C` | `#FBF1DF` / `#2A2111` | Your move: needs you, no progress |
| `error` | `#C42B22` | `#FF7A70` | `#FCEBEA` / `#2B1513` | Failed |
| `success` | `#2E7D32` | `#86C66A` | `#EAF4E6` / `#16220F` | A result is back: review |
| working | `text` | `text` | — | The agent's move; the glyph pulses |
| quiet | `text_muted` | `text_muted` | — | Blocked (not on you), queued, done, offline |

Diff colours: created text `#1E7A3A` / `#6FD08C` on `#E6F4EA` / `#12261A`; deleted text `#C42B22` / `#FF8A80` on `#FCEBEA` / `#2E1715`; modified gutter `text_faint`. Gutter bars are the full-strength text colour; line fills are the tint.

### 4.3 Identity: CLI marks and colours, and the identity palette

Each CLI keeps its own terminal mark and colour, as the TUI does today (`internal/ui/tone/tone.go:239-257`). Dark values are unchanged; three light values are darkened to pass AA on the new `surface`.

| Kind | Mark | Dark | Light | Change |
|---|---|---|---|---|
| claude | `✻` | `#FF9F5A` | `#A94A17` | light was `#C4581B` (4.0:1 on `surface`) |
| codex | `>_` | `#A1A1A6` | `#6E6E73` | — |
| opencode | `▯` | `#64D2FF` | `#00739E` | light was `#0077A8` |
| antigravity | `◎` | `#BF5AF2` | `#8944AB` | — |
| pi | `π` | `#30D158` | `#1F7A36` | light was `#248A3D` (4.0:1) |
| anything else | `▌` | `text_muted` | `text_muted` | the card edge from Deck's own mark |

- A **named agent** keeps its kind's mark (so you can tell which CLI and plan it uses) and is drawn in its profile's identity colour, with its name. (Chapter 03's sketches use `✳` as a stand-in for "a named agent".)
- The **identity palette** has 8 slots for named agents and people. A profile picks a slot (`color` in `deck.json`, 03 §2.2); a person gets the slot hashed from their identity key, never from their display name. The palette avoids the accent's hue.

| Slot | Dark | Light |
|---|---|---|
| rose | `#E48FA6` | `#B03A5B` |
| orchid | `#DE8FD0` | `#A33A93` |
| lilac | `#BC95EE` | `#7A45C2` |
| periwinkle | `#929CF0` | `#4652C9` |
| sky | `#6EB8E0` | `#1D6A98` |
| sand | `#CDA77F` | `#85582A` |
| olive | `#B8B36A` | `#66611A` |
| stone | `#A9B4C2` | `#4F5B6B` |

Rules: identity colour appears only as the mark, the name, an avatar's 16% fill and the 2px rail on a hand-off card. It never fills a status dot. Some identity hues sit near status hues (claude's clay near amber, pi's green near success); that is acceptable only because status always carries its glyph and word, and identity never does.

**Avatars.** Agents: a rounded square (radius 4 at 20px, 6 at 28px), fill = identity colour at 16% over the surface, the mark centred in the identity colour. People: a circle with one or two initials in 600 weight on the same 16% fill. Status: an 8px badge at the bottom-right corner, drawn as the status glyph on its status colour (amber `!`, green `✓`, and so on), with a 2px ring in the surface colour. Presence (a person in this thread) is a 1.5px ring in `text_muted`.

### 4.4 Contrast check (WCAG 2.1 AA)

Computed with the WCAG relative-luminance formula. Normal text needs 4.5:1; UI component edges need 3:1.

| Foreground | On | Dark | Light | AA |
|---|---|---|---|---|
| `text` | `background` | 16.8 | 17.0 | ✓ |
| `text` | `elevated` | 15.0 | 17.7 | ✓ |
| `text_muted` | `surface` | 7.3 | 5.8 | ✓ |
| `text_muted` | `selected` | 6.1 | 5.2 | ✓ |
| `text_faint` | `elevated` | 4.8 | 5.3 | ✓ |
| `text_faint` | `surface` | 5.2 | 4.8 | ✓ |
| `text_faint` | `selected` | 4.3 | 4.3 | ✗ → use `text_muted` on selected rows |
| `accent` (link) | `background` | 8.9 | 5.0 | ✓ |
| `accent` | `surface` | 8.5 | 4.8 | ✓ |
| `on_accent` | `accent` | 7.7 | 5.3 | ✓ |
| `attention` | `surface` | 9.9 | 5.1 | ✓ |
| `attention` | its tint | 8.4 | 5.0 | ✓ |
| `error` | `surface` | 7.4 | 5.1 | ✓ |
| `error` | its tint | 6.8 | 4.9 | ✓ |
| `success` | `surface` | 9.3 | 4.7 | ✓ |
| `success` | its tint | 8.1 | 4.54 | ✓ (tight; do not lighten) |
| diff created text | created fill | 8.4 | 4.7 | ✓ |
| diff deleted text | deleted fill | 7.4 | 4.9 | ✓ |
| `border_strong` | `elevated` | 3.3 | 3.4 | ✓ (3:1) |
| claude mark | `surface` | 9.3 | 5.2 | ✓ |
| codex mark | `surface` | 7.3 | 4.6 | ✓ |
| opencode mark | `surface` | 11.0 | 4.8 | ✓ |
| antigravity mark | `elevated` | 5.0 | 6.0 | ✓ |
| pi mark | `surface` | 9.3 | 4.9 | ✓ |
| identity sky (worst slot) | `selected` | 7.2 | 4.8 | ✓ |

The focus ring is 2px of `accent` with a 1px offset; it is 8.9:1 (dark) and 5.0:1 (light) against the canvas. A CI test in `design/` recomputes this table from `tokens.json` and fails below the thresholds.

### 4.5 Type

| Token | Desktop | Line height | Weight | Use |
|---|---|---|---|---|
| `micro` | 10 | 12 | 500 | Avatar badges, counts in badges |
| `meta` | 11 | 14 | 400 | Timestamps, reply footers, kbd hints |
| `ui_small` | 12 | 16 | 400 | Chips, secondary row text |
| `ui` | **13** | 18 | 400 / 500 | Rows, controls, cards (500 for names and labels) |
| `chat` | **14** | 21 | 400 | Agent prose and your messages |
| `title` | 15 | 20 | 600 | Thread title, section headers |
| `page` | 20 | 26 | 600 | Page titles (Hosts, Agents, Settings) |
| `mono` | **13** | 19 | 400 | Diffs, terminal, tool output, paths, ids, numbers |
| `mono_small` | 12 | 16 | 400 | Inline code in rows, route badges |

- Weights 400, 500 and 600 only. No bold paragraphs.
- UI font: the system sans (SF Pro on macOS, Segoe UI Variable on Windows) and bundled Inter on Linux. Mono: bundled JetBrains Mono on every platform, so diffs and tool output line up the same everywhere. Both are SIL OFL.
- `tabular-nums` on every number (timers, counts, diff stats). Mono for everything a machine wrote.
- Smallest text is 10px, and only inside badges. T3's 7px and 8px badges are not copied.
- Phone: chat 16, UI 15, meta 13, mono 14.

### 4.6 Spacing and sizes

- 4px grid. Steps: 2, 4, 6, 8, 12, 16, 20, 24, 32, 48.
- Rows: 28px compact, 32px default (sidebar, tree, roster, palette, menus). Fixed heights so GPUI's `uniform_list` can be used (Zed source, not re-checked).
- Title bar 40. Child strip 28. Sidebar 256 / 48. Right panel 380 (300–560). Timeline max 736. Card padding 12. Popover padding 6. Gutter 12, 20 from 1180px.
- Hit targets: 24px minimum on desktop, 44pt on the phone.

### 4.7 Radii

| Radius | Used for |
|---|---|
| 4 | Badges, kbd hints, agent avatars at 20px, small chips |
| 6 | Rows, buttons, inputs, chips, agent avatars at 28px |
| 8 | Cards (hand-off, result, Needs you), the composer |
| 12 | Popovers, the palette, sheets, dialogs |
| full | People avatars, status badges |

### 4.8 Elevation

Solid surfaces and shadow; no glass and no blur (GPUI has no element backdrop blur, zed discussion #47429).

| Level | Where | Fill | Edge | Shadow (light / dark) |
|---|---|---|---|---|
| 0 | Canvas | `background` | — | — |
| 1 | Sidebar, panels | `surface` | `border` on the inner edge | — |
| 2 | Cards in the timeline and on Home, the composer | `elevated` | `border` (cards), `border_strong` (composer) | none: a card in a list does not float |
| 3 | Popovers, palette, peek | `elevated` | `border` | `0 1px 2px #0000000D, 0 8px 24px -12px #00000026` / `0 12px 32px -16px #000000BF` plus a 1px inner top line `#FFFFFF0A` |
| 4 | Dialogs, sheets | `elevated` | `border` | level 3, over a scrim of `#00000033` / `#00000099` |

### 4.9 Motion

| What | Duration | Curve |
|---|---|---|
| Panels, sidebars, tabs, view switches | **0 ms** | — |
| Hover, selection, pressed | 80 ms | linear |
| Popover, palette, peek enter | **140 ms** | `cubic-bezier(0.32, 0.72, 0, 1)` (T3's `--ease-drawer`), from 98% scale and 0 opacity |
| Exit | 100 ms | linear, opacity only |
| A thread coming back to the top of the list | 160 ms | same curve |
| Working pulse | **2 s cycle, 6 steps**, duty-cycled | stepped (T3's `steps(6)` pulse) |

- GPUI: no `with_animation` loops for status. A pulse is a timer that calls `notify` on the visible status elements a few times a second and stops when they scroll away or the window is hidden (Zed source, not re-checked). An idle window draws nothing.
- Reduced motion (system setting): no slide, no scale, no pulse; the glyphs stay.
- No user setting for panel animation in v3.0. Panels snap.

### 4.10 Iconography

- Lucide icons (ISC licence), 16px with a 1.5px stroke, 14px in 28px rows, embedded as SVG in the GPUI app and through `lucide-react-native` on the phone. One weight.
- An icon sits next to a word, except toolbar icon buttons, which always have a tooltip with the action and its key.
- No icon in a circle at the head of a row or card (desktop-shell's "Not AI-template" list, `desktop-shell/docs/design-glass.md` §5).
- **Status glyphs and CLI marks are not icons.** They are the same shapes everywhere: Unicode characters in the TUI, and small SVGs drawn to match those characters on desktop and phone (font coverage for `✻` and `◆` varies, so we don't rely on it).
- Deck's own mark is the card edge `▌` followed by the word `deck` in 600.

### 4.11 Mapping to each client

**GPUI.** The `theme` crate is part of Zed, not of `gpui`; Deck defines its own `DeckTheme` struct but uses Zed's field names, so Zed theme JSON could be read later (Zed source, not re-checked).

| Deck role | GPUI theme field |
|---|---|
| `background` | `background`, `editor_background` |
| `surface` | `surface_background`, `panel_background`, `title_bar_background`, `tab_bar_background` |
| `elevated` | `elevated_surface_background` |
| `hover` | `element_hover`, `ghost_element_hover` |
| `selected` | `element_selected`, `ghost_element_selected`, `tab_active_background` |
| `border` / `border_strong` | `border_variant` / `border` |
| `accent` | `border_focused`, `text_accent`, `icon_accent`, `accents[0]` |
| `text` / `text_muted` / `text_faint` / `text_disabled` | `text` / `text_muted` / `text_placeholder` / `text_disabled` |
| `attention` / `error` / `success` (+ tints) | `StatusColors.warning` / `.error` / `.success` (+ `*_background`) |
| diff created / deleted / modified | `StatusColors.created` / `.deleted` / `.modified` |
| identity palette | `PlayerColors` (`cursor` = slot colour, `background` = 16%, `selection` = 24%) |
| CLI colours and marks | `DeckTheme.agent_kinds` (Deck-specific map) |

Components own their look through variants (T3's "primitives own their look" rule; Zed's `ui` crate `ListItem`, `Indicator`, `PopoverMenu` work the same way). Feature code never sets a colour or size directly; a lint or review rule enforces it.

**TUI (lipgloss).** `internal/ui/tone` keeps its shape. `Palette` fields map as `Canvas`←`background` (used only where the TUI paints), `Surface1`←`surface`, `Surface2`←`elevated`, `Selected`←`selected`, `Hairline`←`border`, `HairlineStrong`←`border_strong`, `Text`, `Muted`←`text_muted`, `Faint`←`text_faint`, `Quaternary`←`text_disabled`, `Signal` is renamed `Accent`, `Warning`←`attention`, `Destructive`←`error`, `Success`←`success`, the five CLI colours stay, and `Teal, Purple, Indigo, Yellow, Pink` are replaced by `Identity[8]`. Status styles live in one map, `tone.Status[key]`, built from §5.1 (glyph + style + label), used by every view.

On terminals with 16 colours, the automatic nearest-colour mapping is overridden for status so the roles stay distinct:

| Role | ANSI 16 |
|---|---|
| `attention` | 3 yellow |
| `error` | 1 red |
| `success` | 2 green |
| `accent` | 6 cyan |
| working | default foreground, bold |
| `text_muted` / `text_faint` | default foreground, faint / 8 bright black |
| identity slots | 5, 4, 13, 12, 14, 3, 11, 7 (in palette order) |

`NO_COLOR` drops colour; glyphs and words still carry every status.

**Phone (`apps/mobile/src/ui/themes.ts`).** The 14 palettes become two, `deck` and `deck-light`. `Palette` fields map as `bg`←`background`, `raise`←`elevated`, `fg`←`text`, `muted`←`text_muted`, `primary`←`accent`, `onPrimary`←`on_accent`, `warn`←`attention`, `danger`←`error`, `success`←`success`. `info` is dropped (working uses `fg`). New fields: `surface`, `selected`, `border`, `borderStrong`, `faint`, `identity[8]`, `kinds{claude, codex, opencode, antigravity, pi}`, and the three status tints. Material You colours are not used: the phone looks like Deck on every platform.

---

## 5. States and copy

### 5.1 The status table (shared by all clients)

The engine sends the status and, for Needs you, the request kind (03 §4.2–4.3). Clients look up label, glyph and colour here. Priority is the roll-up order from 03 §4.2; one status shows per thread.

| Rank | Engine status | Kind / cause | Label | Glyph | Colour | Motion | Inbox | Push |
|---|---|---|---|---|---|---|---|---|
| 1 | needs you | approval, brake | "Needs approval" / "Budget used: continue?" | `!` | attention | — | Needs you | yes |
| 1 | needs you | question | "Asked you" | `?` | attention | — | Needs you | yes |
| 1 | needs you | plan, delegation plan | "Plan to review" | `◆` | attention | — | Needs you | yes |
| 2 | failed | — | "Failed" (+ one-line reason) | `✗` | error | — | Review until seen | yes |
| 3 | blocked | plan limit, dependency, lease, host offline | "Resumes 14:20" / "After t9" / "Waiting for auth/*" / "vps-1 offline 3m" | `‖` | quiet | — | Running | no |
| 4 | review | result, finished turn with a diff, race | "Ready to review" | `✓` | success | — | Review | setting (on for phone-started tasks) |
| 5 | working | — | current action, e.g. "editing auth.go" | `●` | text | stepped pulse | Running | no |
| 5 | working + no progress flag | > 5 min without progress | "no progress 6m" | `●` | attention label | pulse stops | Running, sorted first | no |
| 6 | done | accepted, or no output | "Done" / "Cancelled" | `✓` | quiet | — | Settled | no |
| 7 | idle | queued task | "Queued" / "After ‹task›" | `○` | quiet | — | Running | no |
| 7 | idle | nothing pending | — (no glyph) | — | — | — | Threads | no |

Host states (05 §2.3 owns the route badges):

| State | Label | Glyph | Colour |
|---|---|---|---|
| Reachable | route badge, e.g. "tailnet · direct 18 ms" | `●` | `text_muted` |
| Connecting | "Connecting over tailnet…" (names the path being tried) | `◌` with stepped pulse | `text_muted` |
| Offline | "vps-1 offline 3m" (time since last contact) | `◌` | `text_muted`; `attention` when threads there need you |
| Needs sign-in | "codex not signed in" | `!` | attention |
| Version behind | "3.0.1 ↑" | `↑` | `text_muted` |

Each status also has an accessible label of the form "‹label›, ‹thread›, on ‹host›". GPUI's screen-reader support must be checked against the version we pin; the labels exist in the view model either way.

### 5.2 Copy rules

- Plain words, Deck's voice: short sentences, verbs on buttons ("Approve", "Send back to claude", "Sign in on vps-1"), no exclamation marks, no "Oops".
- Name the agent, the thread and the host. "codex wants to run …" not "An agent requests permission".
- Times are relative and short ("2m", "3h", "yesterday"); hover shows the absolute time.
- Say what happens next. "Queued · sends when vps-1 is back · expires in 8 h."
- Never "loading" without a subject. "Connecting to vps-1 over tailnet…" not a bare spinner.

### 5.3 Empty states

No centred logo tile, no icon in a circle, no two equal cards (the "Not AI-template" list). An empty state is one line of text in `text_muted`, and at most one action.

| Where | Copy | Action |
|---|---|---|
| First run, Home | The hero composer: "What should we work on?", with "Runs on laptop ▾" and the crew band listing the CLIs found here and their sign-in state | Send |
| Needs you band | "Nothing needs you. 5 running on 3 hosts." | — |
| Review band | (hidden) | — |
| Running band | "Nothing running." | — |
| Crew band, no named agents | the built-in agents, then "Name an agent to hand work to it." | + New agent |
| Hosts, only this computer | "Only this computer. Add a VPS to keep agents running when the lid is closed." | Add a host |
| A host with no CLI signed in | "No agent can run on vps-3 yet." | Sign in on vps-3 |
| Work tab, no children | "claude is working alone in this thread." | — |
| Search | "No threads, agents or hosts match "migr"." | — |
| Settled | "Settled threads land here after 3 days idle." | — |

### 5.4 Errors

Errors appear where the thing failed, inline, never as a toast storm. Toasts are only for confirmations with Undo.

| Situation | Copy | Actions |
|---|---|---|
| Send failed | "Not sent: vps-1 didn't answer. Your message is still here." (text stays in the composer) | Retry · Queue it |
| Spec not honoured | Under the reply: "codex has no "max" effort for this model; ran at high." | Change |
| CLI not signed in | "codex isn't signed in on vps-1." | Sign in on vps-1 |
| Request answered elsewhere | "ana approved this on her phone 10s ago." (the card resolves) | — |
| Not allowed by role | "Needs an admin. You're a member on vps-1." | Ask an admin (copies a link) |
| Engine stopped | "deck stopped on laptop." | Restart · Show log |
| Host version behind | "vps-1 runs 3.0.1. Move needs 3.0.2." Everything else keeps working. | Update vps-1 |
| Hand-off refused | "reviewer can't take this: its daily budget is used ($5.00)." | Raise budget · Pick another agent |
| Move preflight failed | "vps-1 doesn't have deck/api checked out." | Clone on vps-1 |
| A write lost a race | (no error) The engine returns the resulting state (D4); the UI shows it. | — |

### 5.5 Offline host

```
┌ auth refactor ✻a7f · deck/api ─────────────────── on vps-1 · offline 3m ─┐
│ (timeline greyed: as of 10:42)                                           │
│ …                                                                        │
│ ┌ vps-1 offline 3m · last seen 10:42 over tailnet ─────────────────────┐ │
│ │ Agents keep running there if the host is up; Deck can't reach it.    │ │
│ │                                     [Retry now]  [Wake via SSH]      │ │
│ └──────────────────────────────────────────────────────────────────────┘ │
│ ┌ queued · sends when vps-1 is back · expires in 8 h ────── [Cancel] ──┐ │
│ │ also run the race tests                                              │ │
│ └──────────────────────────────────────────────────────────────────────┘ │
│ [ Message (will queue) …                                            ]    │
└──────────────────────────────────────────────────────────────────────────┘
```

- The badge `offline 3m` appears on the host row, in every one of its threads' headers and on its Running group. The time counts up from the last contact.
- The cached view stays readable, greyed, with "as of 10:42". Nothing disappears because a host went away.
- Only the queueable set queues (05 §3f). Stop, mode changes and interrupts show "Can't do this while vps-1 is offline" on the control.
- A laptop that sleeps reads "laptop asleep", not "offline", when it said so before sleeping.

### 5.6 Loading

- **Never a blank screen.** Every client renders its last cached snapshot at once, stamped "as of 10:42" in `text_faint` until the first live update arrives (D4's revision-stamped snapshot).
- Skeleton rows only on the very first connect to a host, at fixed row heights so nothing jumps; after 300 ms they gain words: "Connecting to vps-1 over tailnet…", naming the path being tried (D14).
- Timeline history pages in from the top with a one-line "Loading earlier messages" row; the scroll position does not move.
- The model catalog never blocks (D5): the picker shows the cached list with "refreshing…" in the footer.
- No spinner that redraws every frame. The busy glyph is the same stepped `◌`/`●`.

---

## 6. What we do not copy, and how we avoid looking like a clone

| From | We do not copy | Why |
|---|---|---|
| T3 Code | Glass and backdrop blur (`surface-glass`, `dialog-glass`), the `surface-grain` noise texture, scroll-fade masks | GPUI has no element backdrop blur; these are also the most recognisable parts of T3's look |
| T3 Code | The indigo primary (≈ 264°) and the wordmark | Brand |
| T3 Code | Pure `#000` dark background | Elevation stops reading; we use `#0B0B0C` |
| T3 Code | 7px and 8px badge text; the sprawl of Labs toggles and appearance sliders | Legibility; critics call T3 "cluttered" (snippet); D18 drops mix-and-match settings |
| Antigravity 2 | Google blue, Material styling, the rounded input bar of a community theme mod (snippet) | Brand; we could not verify the official look anyway |
| Antigravity 2 | Splitting the editor and diff away from the agent hub; forced updates that reportedly wiped history and settings (snippet) | Code stays in reach (Review, Changes, Files, Terminal are in the app). **Updates are never forced:** adjacent client and host versions keep working, data migrates forward with a backup, and v2 threads import before the old app is removed |
| Grok Bot | The purple activity colour, mascot faces and "moods", iMessage-style bubbles (snippet) | Brand; status is a glyph and a word, not a face |
| Grok Bot | **Hidden hand-offs** (bot-to-bot messages in the bots' own chats, (snippet)) | Principle 4: every hand-off is a card in the thread |
| Grok Bot | **A single unbounded thread per agent** (snippet) | Principle 3: threads are context bubbles |
| Grok Bot | Hidden tool calls and no indication of which model answered (snippet) | Principle 6 and the reply footer |
| Grok Bot | Bots deciding among themselves who answers an untargeted message in a group (snippet) | A thread's messages go to its lead; delegation is explicit |
| Grok Bot | Private per-member chats the owner can't see (snippet) | Teammates work in the shared thread with a role (D8) |
| All three | Streaming text on overview screens; live previews on by default | Principle 7 |
| desktop-shell | Liquid Glass, Riso and Stitch art directions, the 78 artworks, YoloFx, the app-icon picker, 10 palettes and 9 diff themes | D18 |

**How Deck stays recognisably Deck:**
1. **The card edge and the corner index.** Deck's mark is `▌deck`, and every thread carries a mono corner index in its lead's mark (`✻a7f`) in the header, the palette and the CLI. Nobody else does this, and it is useful, not decoration.
2. **Squares and circles.** Agents are rounded squares with their CLI's own mark; people are circles. You can read a crew at a glance.
3. **One green-teal accent, spent sparingly**, on grey chrome. No other product in this space uses it.
4. **Hand-off cards with a route** (`claude → codex`) and a rail in the delegate's colour. Visible delegation is Deck's promise; the card is its signature component.
5. **Place on every row.** "on vps-1" and route badges are everywhere, because Deck is about your own fleet, not a cloud.
6. **Mono for what machines wrote**, plain sans for what people and agents said, and plain copy in Deck's voice.
7. **Our own structure where it differs:** the crew band on Home, Running grouped by host, the Work tab that merges the tree and the board, Review with per-hunk accept or reject and batched comments, and the board on one key.

A check before shipping any screen: put it next to a T3 Code screenshot and an Antigravity 2 screenshot (once someone can open them; this research could not). If a stranger can't tell which one is Deck, the screen goes back.
