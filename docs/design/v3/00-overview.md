# Deck v3: the plan

Status: plan, 2026-11-05. Nothing in it is built yet. Each chapter makes its
decisions instead of listing options. Chapters:

| | Chapter | What it settles |
|---|---|---|
| 00 | this file | The decisions, the Rust question, the order of work |
| 01 | [Why Deck is buggy](01-why-buggy.md) | Ranked defects with file:line evidence, traced to root causes |
| 02 | [What v3 deletes](02-cuts.md) | Team chat and contracts, duplicate connection systems, duplicate state and surfaces |
| 03 | [Agents, delegation and everything running](03-agents-and-delegation.md) | Agents as teammates, the new agent · model · effort picker, Inbox + Now |
| 04 | [The v3 experience and look](04-design.md) | The chosen direction, wireframes for desktop, TUI and phone, tokens |
| 05 | [Deck Link: hosts, devices and teammates](05-fleet-and-connect.md) | Your own VPS, Tailscale + SSH, roles, direct access |
| 06 | [The desktop app in Rust and GPUI](06-desktop-gpui.md) | Architecture, budgets, carry-over list, milestones |
| 07 | [One protocol, and the RAM and speed plan](07-protocol-and-performance.md) | One daemon per host, one proto, deltas, budgets |

The research behind it (eight audits and studies of the desktop app, the
TUI, the phone app, the engine, team connect, outside agent tools, the T3
Code and Antigravity 2 looks, and self-hosted connection products) is
summarised in each chapter. Claims about outside products that rest only on
search snippets are marked there.

## 1. Where Deck is

Deck's engine is strong: it drives five agent CLIs under the user's own
logins, runs subagent trees, sub-threads, races, a task board, file leases,
budgets and worktree isolation. The product around it is not:

- **It is buggy because the architecture lets it be.** One conversation lives
  in three stores. The event stream drops events, numbers them after the
  drop, and orders ties by random UUID. Every client rebuilds state itself,
  and "needs you" is computed three different ways. Hand-offs are prose
  re-parsed with regexes. One RPC (`SetModel`) switches the lead, changes the
  model and rewrites the global config. The TUI calls the engine in-process,
  so a modal for one thread pops over another. Chapter 01 has the evidence.
- **It was built faster than it was settled.** desktop-shell is about 46k
  lines of TypeScript written in about twelve days; its look changed three
  times in three days and the agent picker five times. The TUI's `@`/`$`
  addressing changed four times in ten days, and leftovers of each remain.
- **The multi-agent work is hidden.** Nothing shows what is running across
  threads. Delegation shows up on about eleven surfaces, and there are three
  parallel "needs me" lists. You can't hand a task to a named agent.
- **Picking a model is broken everywhere.** Five clicks on desktop, three
  modal steps on the TUI behind a blocking catalog fetch, a hidden long-press
  on the phone, and every pick silently changes global defaults.
- **Team connect is the wrong product.** About 32k lines for team chat, an
  outbox, two kinds of contract and a sandbox, behind a pairing flow the
  desktop can't even reach. There are four connection systems, three pairing
  flows and two relays.
- **It lives in one box.** Agents stop when the laptop closes; there is no
  first-class way to run them on your own VPS and reach them from anywhere.

## 2. What v3 is

**One deck for all your agents, on all your machines.** Agents are
teammates with profiles. You hand them work, they hand work to each other in
plain sight, and the results come back to you for review. One home shows
what needs you, what's ready to review and what's running, across every host
you own. The same threads open in a native desktop app, the terminal and
your phone, and a teammate you invite opens them the same way.

The usage model is xAI's Grok Bot (persistent agents with profiles,
delegation by profile, a roster at home, three levels of watching: an
indicator, Follow, Open) with its flaws fixed: threads stay separate
contexts, every hand-off is visible, tool calls and the model that answered
are always shown. The look merges T3 Code's calm, dense structure (inbox
ordered by what came back to you, one status per thread, centred timeline,
docked composer, popover picker with effort bars) with Antigravity 2's
agent-manager ideas (an overview of subagents you can follow, reviewable
artifacts with batched inline comments), in Deck's own tokens: grey chrome,
one green-teal accent, solid surfaces, no glass.

## 3. Decisions

These are made. Chapters build on them and do not reopen them.

| # | Decision | Chapter |
|---|---|---|
| D1 | The engine and TUI stay in Go. No Rust rewrite of the engine. | §4 below |
| D2 | The desktop app is rewritten in Rust on GPUI and talks to the engine over gRPC (tonic). desktop-shell (Electron) is the feature reference and is deleted at parity. The Gio app is already gone (`eec5677`). | 06 |
| D3 | One daemon per host serves every project; threads run with no client attached. Every client and every person uses the same protocol, the TUI included. | 07 |
| D4 | The engine is the only source of truth: one store per conversation, durable revisions, a snapshot then ordered deltas, a cheap per-host summary stream, idempotent writes, engine-owned attention, structured hand-off/report/plan/turn-end events, no polling. | 07 |
| D5 | Agent, model and effort form one resolved spec with explicit scope: this thread, a profile, or the defaults (changed only in Settings). `SetModel` is split into `SetLead`, `SetMemberSpec`, `SetDefaults`; one non-blocking catalog. Every reply shows its model and effort and where they came from. Subagents inherit their parent's spec unless the profile says otherwise. | 03 |
| D6 | Named agents become persistent teammates with a description used for delegation by profile. Delegation is a structured task with an owner, a delegate, a done-when and a reviewable result. | 03 |
| D7 | One home, **Inbox + Now**: Needs you, Review, Running (across hosts), later Routines. It replaces the Inbox, the bell and Highlights. | 03, 04 |
| D8 | Team chat, the outbox, code and ops contracts, the vsh sandbox and the peer link are deleted. A teammate gets direct access to the host's threads with a role. | 02, 05 |
| D9 | One connection system, **Deck Link**, for devices, hosts and teammates, built on the phone's Noise stack (`pkg/remote`). Transport: Tailscale + SSH. | 05 |
| D10 | The agent browser runs on the host (headless Chromium + CDP) and streams to whichever client watches. | 06 |
| D11 | One phone client (`apps/mobile`): inbox with approvals and questions answerable in place, Now, Agents, threads. It closes its stream in the background and uses push. | 04, 05 |
| D13–D14 | Paths a client tries: local socket, tailnet (system `tailscaled`, else embedded tsnet; Headscale works), direct, the Noise relay (self-hostable with `deck relay`), an SSH stdio tunnel. Trust comes from Deck's pinned keys and roles, never from being on a network. | 05 |
| D15 | Roles: viewer, member, admin (the host owner is admin). Members' turns are capped at ask mode; destructive approvals need an admin. Teammates spend the host owner's plans, and the invite says so. | 05 |
| D16 | Adding a VPS takes under two minutes: `deck host up` on the server, or "Add a host" in the desktop app over your own SSH. Each host shows its CLI logins, with a Sign in terminal; Deck never reads credentials. | 05 |
| D17 | A thread can move between hosts (log + working tree; the agent resumes from a hand-off summary). "Keep running on vps-1 when the lid closes" is that move. | 05 |
| D18 | The look: chapter 04's tokens. The Liquid Glass, Riso and Stitch art directions and mix-and-match appearance settings go. | 04 |

Decisions the chapters added:

| Decision | Chapter |
|---|---|
| One SQLite database per host (`~/.deck/deck.db`, WAL) holds a gap-free event log per thread and the materialised messages, written in one transaction. Clients see an event only after it is on disk. The Crush per-project SQLite and `deck-threads.json` are folded in; the session id becomes the thread id. | 07 |
| A new `deck.v2` proto: `WatchHost`, `WatchThread`, paged `History`, one `Exec(Command)` for every write (with `client_op_id`, returning the resulting revision). `deck.v1` is frozen and deleted with Electron; `pkg/remote/wire` is deleted. | 07 |
| gRPC only on local sockets. Every remote path carries `deck.v2` as Link frames inside Noise IK. The desktop and TUI reach hosts through one Go link agent per machine user, so tsnet, SSH and path logic live only in Go. | 05, 07 |
| A run (one agent in one thread) replaces "member". A task is the only record of delegation; work handed to a named agent gets its own sub-thread. The hand-off "contract" is renamed **Brief**; the word "contract" leaves the UI and the proto. | 03 |
| The orchestration dial (Off / Assist / Auto / Full) becomes a delegation policy: off, ask, auto. Under ask, the lead's proposals arrive as one plan to approve. | 03 |
| The desktop app lives in a Rust workspace at `desktop/` (`deck-proto`, `deck-client`, `deck-state`, `deck-ui`, `deck-desktop`) on `gpui-pre` and gpui-component (Apache-2.0). Deck is proprietary, so no GPL Zed crate (`ui`, `editor`, `terminal`, `markdown`, `theme`, …) is used or copied; `cargo deny` enforces it. | 06 |

## 4. Do we switch to Rust?

**For the desktop app, yes (D2). For the engine and TUI, no (D1).**

- The engine and TUI are about 188k lines of Go with 91k lines of tests. A
  rewrite would spend months reproducing features that work, and would
  throw away the tests that protect the redesign.
- The speed and RAM problems are architectural, not linguistic: every
  token of every thread sent to every client, full-message re-sends every
  200 ms, no list virtualisation in the old desktop, the TUI rendering a
  whole 1,000-message transcript through markdown to size a scrollbar
  (1.08 s, 266 MB), polling. Each would be just as slow in Rust. Chapter 07
  fixes them in the protocol and the engine.
- The agent CLIs dominate RAM on the host, and no client language changes
  that. The engine work that does (reaping idle CLIs, starting members
  lazily, one daemon per host instead of one engine per open project) is in
  chapter 07.
- The desktop rewrite is justified on its own: desktop-shell's bugs are
  mostly architectural (whole-state re-renders, remounts on project switch,
  a global pane layout, 21 swallowed errors, nine competing key listeners),
  and GPUI gives a native, GPU-rendered app with small memory use, the
  editor-grade text and list primitives Zed is built on, and docks, key
  contexts and follow mode we can borrow. Its one hard gap, the lack of a
  webview, is solved by moving the agent browser to the host (D10).
- GPUI risks (a pre-1.0 API that moves with Zed, accessibility, IME, no
  element-level backdrop blur) and their mitigations are in chapter 06.

## 5. Order of work

Foundation first. Each phase ships on its own and leaves Deck working.

**Phase 0: freeze and measure (1 week).** No new features on desktop-shell,
the TUI or the phone. Land the replay harness: record real CLI sessions to
event files and replay them deterministically into each client. Put the
budgets from chapter 07 in CI so every later phase is measured.

**Phase 1: the foundation (engine + protocol).** The work that removes whole
bug classes for every client at once (chapters 01, 07):

1. One durable event log per thread with revisions; fold the Crush-derived
   SQLite and `deck-threads.json` into it.
2. Ordered, lossless delivery: sequence numbers assigned before fan-out,
   revision-stamped snapshots, deltas (`message.append_text`), stable resume
   across engine restarts.
3. Engine-owned attention and status, one vocabulary (chapter 03).
4. Structured events for hand-offs, task reports, plans and turn ends; delete
   the regex parsers.
5. Split `SetModel` into `SetLead`, `SetMemberSpec`, `SetDefaults`; one
   cached catalog; delegation inherits the parent's spec.
6. Events for the board, agents and limits; delete polling.
7. Idempotent commands (`client_op_id`) and an atomic send-and-steer.
8. One daemon per host serving every project.

**Phase 2: the TUI on the protocol, and the quick wins users feel.** Move the
TUI onto the same streams (removing the `[IN-PROC]` bugs in chapter 01), lazy
transcript measurement, the new picker, Inbox + Now as one key, per-thread
approvals. The TUI is the first client on the new protocol because it is in
the same repository and language as the engine.

**Phase 3: the GPUI desktop app** (chapter 06 milestones): M0 connected to a
host, M1 threads + composer + picker, M2 Inbox/Now + delegation, M3 changes,
diff and terminal, M4 fleet + host-side browser, M5 parity. desktop-shell
keeps shipping until M5, then is deleted.

**Phase 4: Deck Link** (chapter 05): generalise the Noise stack to any client
and any host, `deck host up`, Add a host over SSH, tailnet paths, the fleet
view, moving threads between hosts. Ship roles and teammate access in the
same release that deletes the peer link, so teammates are never left with
nothing.

**Phase 5: the cuts** (chapter 02): the peer link, vsh, contracts, team chat,
`deck remote` v2, the extra phone clients, the legacy addressing code, the
art directions, desktop-shell. Several cuts can land earlier, as soon as
nothing depends on them; chapter 02 orders them.

**Phase 6: the phone** on the same protocol: questions answerable in place,
push, the picker sheet, Now and Agents. Routines (scheduled and triggered
runs on a host) follow.

Phases 3, 4 and 6 can overlap once phase 1 has landed; phase 1 cannot be
skipped, because every client depends on it.

## 6. How we keep it from getting buggy again

- **The engine owns state; clients are projections.** No client derives
  status, attention or hand-offs on its own.
- **Every bug fix comes with a replay test** from a recorded session.
- **Multi-thread tests are mandatory** for approvals, questions and streaming.
- **No redesign churn.** The look in chapter 04 and the picker in chapter 03
  are settled; changes go through a short written proposal first.
- **A release gate:** the budgets in chapter 07 and the replay suite pass on
  all three clients before a release.
- **Agent-written code gets the same review as anyone's**, and WIP commits
  don't land on main.
