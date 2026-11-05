# 01. Why Deck is buggy

The owner's first complaint about Deck is that the UI is buggy. This chapter explains why, and it does not go easy on the code. It covers the engine, desktop-shell (Electron), the TUI and the phone, at commit `eec5677`. The Gio desktop app is already gone. Line numbers are as of that commit. The ten worst rows in the defect table were re-checked against the repo. All other references come from the four research audits.

---

## 1. Verdict

Deck's bugs are not a pile of separate mistakes. They come from a few structural causes, and every client inherits them. Patching one client fixes one copy of a bug and leaves the others. The causes, worst first:

| # | Root cause | In one line |
|---|---|---|
| **R1** | **No single source of truth** | One conversation lives in three stores: the engine JSONL log, Crush's SQLite and `deck-threads.json`. Clients read a lossy re-rendering of the real log. Each client then rebuilds its own state: the desktop has about 16 module stores, the phone has two diverging PWAs plus a native app, and the TUI has a memo stack. "Needs you" is computed three ways. There is one engine per project folder, and each one saves the whole config file. |
| **R2** | **The event stream drops, numbers after the drop, and orders at random** | Message created/deleted events go through a lossy pubsub. Sequence numbers are added *after* that layer, so a drop leaves no gap a client can see. Rows created in the same second are ordered by random UUID. Snapshots carry no revision, so clients guess how to merge them with live events. |
| **R3** | **Full-message re-sends and polling** | Every 200 ms the whole streaming message is sent again (O(n²) over a long reply). It goes to every desktop subscriber, unscoped. Board, agents, work, limits, git, team and the TUI's lead are polled because they have no events. |
| **R4** | **Prose as protocol** | Hand-offs, sub-thread reports and task reports are written as text and parsed back with regexes in three places. Workflows are prompt boilerplate. Plans are found by spotting numbered lists. `$name` addressing outlived the feature it served. |
| **R5** | **Overloaded, kind-keyed, fire-and-forget writes** | `SetModel` switches the lead, changes the model and rewrites the global config in one call. It is addressed by *kind*, not by member. `""` means three different things. Writes return `Empty` and carry no client op id, so retries duplicate and optimistic UI can't reconcile. |
| **R6** | **The TUI is coupled to the engine in-process** | The TUI type-asserts a dozen engine interfaces. It reads engine state under locks inside `Update` and `View`, and it rides Crush's single-slot permission and question services. Its event path is not the one remote clients use, so it has its own bugs. |
| **R7** | **Local actions have global side effects** | Plan/yolo in one thread changes every thread. A model pick for one thread changes every future thread. Closing a terminal pane in project A kills project B's shell. A sidebar toggle writes the global config. |
| **R8** | **Agent-written churn with no stabilisation** | desktop-shell is 196 commits in 12 days (Oct 19–30), 73 of them on Oct 26. It went through three visual directions in three days and five agent-picker redesigns. The TUI composer's sigil model was redesigned four times in about 10 days. There are 9 `WIP: work in progress when the agent stopped` commits on main. Fix commits mostly "buy time" in tests. No TODOs exist anywhere: the debt is unmarked, not absent. |

R1–R3 cause most of the "the screen is wrong" bugs: missing rows, ghost rows, vanished approvals, wrong order, stale lead. R5 and R7 cause the "it did something I didn't ask for" bugs. R8 is why none of this was caught: nothing stood still long enough to be tested, and the tests that exist cover pure renderers, not the stores, streams and focus logic where these bugs live.

---

## 2. Defect table

Fate in v3:
- **Protocol**: removed by the D4/D5 protocol (revisions, stamped snapshots, deltas, structured events, AgentSpec, `client_op_id`).
- **GPUI**: disappears with the desktop rewrite (D2), because the code that has the bug is deleted.
- **Engine**: needs an engine fix, sometimes alongside the protocol.
- **Client**: the TUI or phone client is kept, so the bug is fixed in that client.
- **Cut**: the feature is deleted (D8, D13, D19).

Rows marked ✓ were re-checked in the repo at `eec5677`.

| # | Defect | Where | What the user sees | Cause | Fate |
|---|---|---|---|---|---|
| 1 ✓ | Created and deleted message events go through the lossy `Publish`. The rpc feed numbers events only after that layer, so a dropped event never gets a seq. | `internal/message/message.go:191,227`; `internal/pubsub/broker.go:170-196`; `pkg/rpc/server.go:81-87`; `pkg/rpc/feed.go:46-62` | A row, tool result or deletion is missing until a full refetch. The TUI never refetches: a tool spinner stays stuck until the session is reloaded. `(epoch, seq)` resume promises gap-free delivery but can't detect this loss. | R2 | Protocol |
| 2 ✓ | `SetModel` does three things: `SetLead`, set the global default for the kind, and `saveDeckConfig` to the user's `deck.json`. | `internal/workspace/agent_picker.go:149-171` | Trying a model in one thread changes every future thread and every later subagent of that kind, on every client. Nothing in any UI says so. | R5, R7 | Protocol + Engine (D5: SetLead / SetMemberSpec / SetDefaults) |
| 3 ✓ | Lane "Apply" and the phone roster call `SetModel` with the lane's **kind**, not its member. | `desktop-shell/src/renderer/ui/Lanes.tsx:430`, `ui/Room.tsx:309`; `apps/mobile/src/screens/SessionSheet.tsx:196,286-305` | Changing `claude-2`'s model changes `claude-1` and makes it the lead. On a codex-led thread, tweaking a claude helper hands the thread to claude. | R5 | Protocol (member-addressed `SetMemberSpec`) |
| 4 ✓ | The phone v3 wire has no "answer" decision. The policy classifies questions as destructive. The panel offers only Deny / Approve. The backend rejects `allow` for a question. | `pkg/remote/web/src/protocol3.ts:378`; `pkg/remote/policy/policy.go:21-35`; `apps/mobile/src/screens/ApprovalPanel.tsx:48-52,102-129`; `pkg/remote/adapter/questions.go:35-50` | An agent asks a question. The user gets a biometric passkey prompt, then an error. Deny is the only answer that works. Push and "Needs you" both lead to this dead end. | R1 (hand-mirrored wire diverged), R8 | Protocol (one schema) + Client (D19) |
| 5 ✓ | Paged history breaks `created_at` ties (unix **seconds**) by random UUID. The TUI orders by `created_at` with no tiebreak. | `pkg/remote/adapter/adapter.go:228-233`; `internal/message/message.go:210`; `db/sql/messages.sql:14` | An assistant row, its tool result and the next row (often the same second) appear in random order. The desktop, phone and TUI disagree on message order. | R2 | Protocol (per-thread `rev`) |
| 6 ✓ | A `PermissionRequest` from **any** thread opens a modal over the current one, without naming the thread. Crush's permission service serialises all requests on one process-wide mutex. | `internal/ui/model/ui.go:1140-1150`; `internal/permission/permission.go:207-208`; `internal/ui/dialog/permissions.go:795` | An approval pops over whatever you're reading, from a thread you can't identify. Agent B's approval waits, unseen, behind agent A's. No queue depth is shown. | R6 | Protocol (approvals keyed by thread) + D7 Inbox |
| 7 ✓ | `handleQuestionNotification` closes the open question form on *any* question notification, whatever its batch. Opening a form replaces an open one without answering it. | `internal/ui/model/ui.go:5475-5486`, `:5451-5456`; `internal/workspace/questions.go:196-226` (10 ms busy-poll) | With two threads asking, settling one closes the form you're typing into. | R6 | Protocol |
| 8 ✓ | On resync the desktop refetches `Approvals` and the reducer **replaces** the list. A `approval.requested` that arrives during the fetch is overwritten by the older snapshot. | `desktop-shell/src/renderer/rpcClient.ts:156-166,250-253`; `pkg/remote/web/src/state.ts:293-296` | After a reconnect an agent waits on an approval that no longer shows anywhere. | R2 | Protocol (snapshot stamped with rev) |
| 9 ✓ | The engine dies when the desktop closes its stdin. There is one `deck serve --rpc` per project folder. | `internal/cmd/serve.go:325-329`; `desktop-shell/src/main/decks.ts:249-252,410` | Closing the app stops every agent. A thread can never be resumed while it is still running. Opening N projects starts N engines. | R1 | Engine (D3 one daemon per host) |
| 10 ✓ | Every engine saves its **whole** in-memory config (only `peers` is re-read). | `internal/workspace/peers.go:151-162`; `agent_picker.go:162-171` | A model pick or Settings change made through project A silently reverts what project B saved since A started. | R1, R7 | Engine (one daemon; per-key writes) |
| 11 ✓ | `applyMode` puts **every live thread** in the current plan/ask/yolo mode. | `internal/workspace/deck_workspace.go:402-420`; `internal/ui/model/ui.go:3872-3878` | `tab` or `ctrl+y` in one thread switches all threads to yolo. Agents elsewhere start running commands without asking. | R7 | Engine (mode per thread) |
| 12 | Desktop "steer" sends a normal queued message, then steers *every* queue item that appears within 3 s. | `desktop-shell/src/renderer/ui/useRoom.ts:32,37-55,176-180` | A phone's or teammate's queued message gets steered too. If the queue event takes over 3 s, nothing is steered and the user isn't told. | R5 (no atomic op) | Protocol (`Steer` with `client_op_id`) |
| 13 | Each streaming tick re-sends the whole message. The rpc feed ignores `Scoped` and fans every session's stream to every desktop subscriber. | `pkg/remote/adapter/events.go:21,44-48`; `pkg/rpc/feed.go:46-62`; `pkg/remote/backend.go:47-53` | With N busy threads the renderer takes about N×5 full copies a second. It re-sorts the whole thread and recomputes every room's status per batch. The UI stutters on long replies. | R3 | Protocol (text deltas, per-thread subscription) |
| 14 | Hand-offs and reports are written as text (`→ $codex · sub-thread <sid>`, `task t3 "…" done:`) and parsed with regexes in the TUI and the web code (the desktop re-exports the web parser). | writer `internal/workspace/deck_workspace.go:502-518`; parsers `pkg/remote/web/src/handoff.ts:22-25`, `internal/ui/chat/cards.go:37-39` | An agent reply that starts with `→ $name` renders as a hand-off card. Any change in wording breaks the cards. The native phone shows raw prose. | R4 | Protocol (`HandoffCard`, `TaskReport` events) |
| 15 | Attention is computed three ways: rpc `room.attention` (approvals, roster errors, blocked tasks), phone daemon `pending>0 / busy`, and the TUI's own. | `pkg/rpc/room.go:92-127`; `pkg/remote/daemon/daemon.go:232-262` | The desktop says a thread needs you. The phone says it's running, because it ignores errors and blocked tasks. | R1 | Protocol (engine-owned attention) |
| 16 | Board, agents, work, limits, git and team have no events and are polled. The poll stamps `at: now` on errors, and its responses can arrive out of order. | `desktop-shell/src/renderer/crewState.ts:7-22,50-60,84-109`; `limitsState.ts:6-9`; `ui/Git.tsx:39` | Stale tasks and agents are shown as current, with no error state. An older response can overwrite a newer one. Each busy room costs 2 RPCs every 3 s. | R3 | Protocol (board/agents/limits events) |
| 17 | An in-place model switch (codex per-turn override) emits no `roster_changed` or `session_updated`. The TUI polls the lead every 2 s. | `pkg/rpc/room.go:162-181`; `internal/ui/model/lead.go:23-26,56-78` | The lead chip shows the old model after a switch. In the TUI it snaps back after a refused pick, or goes blank if the poll raced a session switch. | R3 | Protocol |
| 18 | `SendRequest` has no client id. `SetModel`, `SetMode` and `TaskAction` return `Empty`. | `proto/deck/v1/deck.proto:566-574` | A retry after a dropped reply posts twice. A new thread's failed model pick still sends the first message to the default agent (`ui/useRoom.ts:121-131`). | R5 | Protocol (`client_op_id`, return resulting state) |
| 19 | The picker's "current" model is the *first* member of the kind, not the lead. | `internal/workspace/agent_picker.go:103-114`; `pkg/remote/adapter/convert.go:221` | In a thread with an old `claude1` and a lead `claude2`, the check mark and effort show the wrong agent. | R5 | Protocol |
| 20 | `""` means "CLI default" on the wire, "keep current" in `SetLead`, and "reset the global default" in `SetAgentModel`. | `deck.proto:641`; `internal/deck/session/lead.go:148-162` | Once a model or effort has been set, "Default" can never be chosen again. Picking "Default" resets the global default and leaves the thread unchanged. | R5 | Protocol (fully resolved AgentSpec) |
| 21 | The model catalog asks every installed CLI serially. The TUI dialog doesn't open until all have answered (30 s timeout). A CLI that failed is asked again on every open. A slow first answer can pin the static fallback list for the process's lifetime. | `internal/workspace/agent_picker.go:73-100`; `internal/ui/model/ui.go:5270-5294`; `internal/deck/agent/claude/models.go:28-31`; `session/modes.go:246-255` | The first `ctrl+l` takes seconds and spawns up to five processes. One broken CLI makes every open slow. New models don't appear. | R5 | Engine (one cached catalog RPC) |
| 22 | Delegated agents don't inherit the parent's spec. `agent.spawn` takes the global per-kind default, and can switch kind through plan-headroom routing. | `internal/deck/session/spawn.go:58-165`; `session/manager.go:642-645`; `session/brakes.go:353-386` | A subagent runs on whatever another thread last picked for that kind, or on a different CLI, with only a notice. | R5, R7 | Engine (D5 inherit by default, shown on the card) |
| 23 | The native phone creates a real session just to list models. | `apps/mobile/App.tsx:130-138,158-169` | "Untitled task" sessions pile up under Done on the phone and the desktop. A failed start leaves half-built sessions. | R5 | Protocol (catalog without a session) + Client |
| 24 | `ListMessages` paging is fake: it loads and decodes every row, then sorts and slices. A cursor on a deleted row returns `ErrInvalid`. | `pkg/remote/adapter/adapter.go:217-257` | Long threads are slow to open on every remote client. Scrolling back after an edit-and-resend fails. | R3 | Protocol (paged history from the log) |
| 25 | `messages.loaded` merges by id and `updated_at` and never removes anything. | `pkg/remote/web/src/state.ts:127-135,276-291` | A message deleted while the stream was down (edit and resend) stays as a ghost until restart. | R2 | Protocol (stamped snapshot, tombstones) |
| 26 | Opening a long session renders every message through glamour on the UI goroutine (`TotalHeight` for the scrollbar). | `internal/ui/model/chat.go:261`; `internal/ui/list/list.go:189-210` | About 1 s freeze per 1,000 messages. A resize drops every cached item and does it again (`list.go:110-117`). | R6 (Crush model), R8 | Client (height estimates, paged history) |
| 27 | TUI `Update` reads the live roster twice per message (keystrokes and ticks included). `View` reads engine state under its locks once per frame. `ctrl+s` makes O(sessions) synchronous engine calls. | `internal/ui/model/ui.go:831-852`; `model/frame_memo.go:18-41`; `dialog/sessions.go:74-78` | Typing and scrolling stall while agents are busy. The session switcher hangs with many threads. | R6 | Protocol (TUI renders from a local view-model) |
| 28 | Every thread's message events enter the TUI's `Update` about every 33 ms per streaming agent. Each one resets the frame cache. | `internal/message/message.go:21`; `internal/ui/model/ui.go:1091-1102`; `model/framecache.go:240-255` | Full-screen redraws at about 30 Hz for threads you aren't looking at. | R3, R6 | Protocol (per-thread subscription) |
| 29 | The Tasks pane cursor is a row **index** over a list that live events reorder. `x` / `a` act with no confirmation. `x` on a subagent row closes the pane. | `internal/ui/model/pane_rows.go:88-93`; `model/pane_tasks.go:65-95,115-128` | Cancel, apply a worktree, or "keep this race entry and discard the others" can hit a different row than the one highlighted. | R2, R8 | Client (cursor by id, confirm destructive) |
| 30 | `turn_end` (done / handoff / blocked / continue) is stored but nothing in `internal/ui` reads it. The README says clients show it. | `internal/workspace/deck_workspace.go:821`; `internal/message/content.go:131-133` | The TUI never says whether an agent finished, handed off or is blocked. | R4, R8 | Protocol (structured turn_end) + Client |
| 31 | The phone app never closes its socket in the background. The daemon keeps streaming full messages every 200 ms. | `apps/mobile/App.tsx:592-598`; `pkg/remote/adapter/events.go:21` | Battery and data drain while the phone is in a pocket. Combined with "a phone with a live session is not pushed to", notifications can go missing. | R3 | Client (D11) + Protocol |
| 32 | The phone inbox is every session ever, with no paging. `inbox.changed` broadcasts the full list 150 ms after any session, approval or **busy** event. The phone ignores the payload and fetches it again. | `pkg/remote/daemon/daemon.go:155-183,232-271`; `pkg/remote/web/src/state3.ts:316-317` | Each busy toggle costs two full inboxes over a Noise relay. Home slows down as history grows. | R3 | Protocol (per-host summary stream) |
| 33 | Phone resume stores no focus. Every reconnect builds a new chat and refetches 100 messages, roster, mode and changes. There is no liveness check on foreground. | `pkg/remote/web/src/state3.ts:226,337`; `apps/mobile/App.tsx:337-356` | Every network flap re-downloads the transcript. A dead socket shows "Connected". The old screen sits offline with the composer silently in "Queue" mode. | R2, R3 | Protocol (durable rev resume) + Client |
| 34 | The native app renders every `role === "user"` message as your own bubble, including a parent agent's brief to a sub-thread. Subagents appear in the rail as addressable `$name` peers. | `apps/mobile/src/screens/ChatScreen.tsx:37-58,321` | The sub-thread brief from claude looks like something you typed. Tapping a subagent "addresses" it, and nothing reaches it. | R4 | Protocol (author on messages) + Client |
| 35 | Desktop `restart()` → `boot()` returns silently if `Info` fails. Nothing subscribes or retries. | `desktop-shell/src/renderer/rpcClient.ts:118-120,193-215` | After an engine restart the project looks open and never updates. | R8 | GPUI |
| 36 | The pane layout is global. `pruneTerms` kills any project shell whose slot is not in the layout. Focusing a pane rewrites the global layout. | `desktop-shell/src/renderer/layout/store.ts:88-103`; `layout/useRoomLayout.ts:98-115`; `ui/Terminal.tsx:208-213` | Closing a terminal in project A kills project B's shell. Splitting in A spawns a shell in B. Settings › Layout wipes the pane tree (`store.ts:124-129`). | R7 | GPUI (per-room layout) |
| 37 | The whole Shell remounts on project switch (`key={current.key}`). Image attachments are component state. | `desktop-shell/src/renderer/ui/App.tsx:407-408`; `ui/Composer.tsx:150` | Scroll, graph pan, popovers and the race builder are lost on every project switch. Attachments vanish on thread switch. | R1 | GPUI |
| 38 | Popovers aren't portaled and are clipped vertically. Escape is handled by four layers at once. Nine global `keydown` listeners, two in the capture phase. | `desktop-shell/src/renderer/ui/kit/menu.tsx:47-49,62-101`; `ui/kit/dialog.tsx:27-31`; `layout/useRoomLayout.ts:192-221` | The model picker is cut off in short windows. One Escape closes a popover and the dialog under it. Shortcuts are stolen from a focused terminal. | R8 | GPUI (real focus and layer stack) |
| 39 | `call()` toasts on every non-quiet failure. `Changes` runs on every busy toggle of every room. | `desktop-shell/src/renderer/rpcClient.ts:104-116`; `ui/useRoom.ts:60-75` | An unreachable engine floods the screen with toasts. | R3, R8 | GPUI |
| 40 | Phone errors are swallowed: a failed `sessions.focus` freezes the transcript, and one undecodable approval blocks every approval in the thread. | `pkg/remote/web/src/chat3.ts:125,197`; `apps/mobile/App.tsx:274-276,290` | The transcript stops updating with no message. "Unreadable approval. Reconnect before answering." blocks every approval in the thread. | R8 | Client |
| 41 | Crush model logic still runs: session load writes the preferred model to the **global** config. The Thinking toggle flips a field Deck never reads, and reports success. | `internal/ui/model/ui.go:1007,2634-2690,3855-3869` | Opening a session changes config. "Thinking mode enabled" does nothing. | R6, R7 | Cut |
| 42 | Shared threads are a rendered-text mirror. Grants are keyed by teammate **name**. Only threads in memory are served. | `internal/deck/session/share.go:115-130`; `proto/deck/v1/deck.proto:1268` | A teammate sees read-only text with no tools or diffs. A re-pair under a reused name inherits access. Shared threads disappear after the owner restarts. | R1, R4 | Cut (D8, D13, D15: direct access by identity key) |
| 43 | Peer state files (`outbox.json`, `contracts.json`, `chat.jsonl`) are rewritten whole by every engine process, under an in-process mutex only. | `internal/deck/peer/outbox.go:50-60`; `peer/contract.go:157`; `peer/team.go:117` | Two open projects, or the TUI plus the desktop, overwrite each other's outbox and contracts. | R1 | Cut (D8, D13) |

Notes on the research:
- The phone audit gives the hub replay ring as 512. It is 2,000 (`pkg/remote/hub/hub.go:18`).
- The desktop audit puts the lane Apply bug at `Room.tsx:309`. That line only defines `setModel(kind, …)`. The kind is bound at `Lanes.tsx:430`.

---

## 3. By client

### 3.1 Engine

1. **It publishes a lossy stream as if it were reliable** (rows 1, 5, 8, 25). `pubsub.Publish` documents itself as "best-effort and lossy under contention", and message create and delete go through it. The gRPC feed then adds sequence numbers and an epoch, and the clients trust those numbers. The real log (JSONL) is complete. What clients see is a re-rendering of it through Crush's SQLite, with second-resolution timestamps and UUID tiebreaks. Every client invented its own way to recover: the phone refetches on welcome, the desktop on `Ready.resync`, and the TUI never does.
2. **One RPC does three things** (rows 2, 3, 19, 20). `SetAgentModel` is the only model control every client has. It changes the lead, the global default and the file on disk. It is addressed by kind, its idea of "current" is wrong, and `""` is ambiguous. This one function causes the top picker complaints on all three clients.
3. **Facts become prose** (rows 14, 30). The workspace turns a typed `RelayMsg` into a string and writes it as a chat message. Three clients then pull the structure back out with regexes. `turn_end` is computed carefully (`session/turnend.go`) and then dropped by the TUI.
4. **Lifetime and multiplicity** (rows 9, 10, 43). One engine per folder, tied to the desktop's stdin. Each engine holds a full config copy and rewrites shared files. On top of that, every member costs one CLI process plus one `deck mcp-approve` Go process (`approve/broker.go:147-155`). Idle members are never reaped, and opening an old thread eagerly resumes every member (`session/manager.go:1486-1499`).
5. **No events for half the state** (rows 16, 17). The engine already emits `TaskChangedMsg`, `ModelChangedMsg` and `MemberAddedMsg`. `DeckWorkspace.pump` turns most of them into usage telemetry (`deck_workspace.go:555-580`), so clients poll.

### 3.2 desktop-shell (Electron)

1. **Live state is unreliable**: the steer race (row 12), vanishing approvals (row 8), ghosts (row 25), stale polls shown as fresh (row 16), and a dead project after a restart (row 35). Most of these are races between an unstamped snapshot and the stream, or between two timers.
2. **The picker does the wrong thing** (rows 2, 3, 18). Lane Apply switches the lead. Every pick rewrites global defaults. A failed pick still sends the message. Settings says "picked per thread", which is false.
3. **Navigation loses work** (rows 36, 37). Terminals are killed across projects, layout is wiped by Settings, attachments are dropped, and the Shell remounts on every project switch.
4. **It renders too much per token** (row 13). The full message arrives 5 times a second. Each batch re-sorts the thread, recomputes `roomStatus` for every session and fires a global notify. Glass blur sits under the live timeline.
5. **It never stood still** (R8). 46.5k lines of TypeScript written by agents in 12 days, three looks, five picker redesigns, ~16 hand-synced module stores, and 21 `.catch(() => {})`. The README still teaches `@` addressing, which was removed. The e2e suite has 163 timeout or `setTimeout` uses, and its fix commits add time instead of removing races.

All of this code is deleted in v3 (D2). What carries over is the lesson: one owned state model per room, a real focus and layer stack, and no polling.

### 3.3 TUI

1. **Multi-thread routing is broken** (rows 6, 7). Approvals and questions come from Crush's single-slot services. They pop over the current thread without naming their own, queue invisibly behind each other, and dismiss the wrong form. No test covers more than one thread asking at once.
2. **The engine is read inside the render loop** (rows 27, 28, 17). Roster reads in `View`, two roster reads per `Update`, synchronous session listing, a 2 s lead poll, and every other thread's stream forcing redraws. Each performance fix added another memo layer: frame cache, frame memo, pane memos, list freeze, warm cache. Each layer has its own invalidation rules.
3. **Long sessions are slow** (row 26). 1.08 s and 266 MB allocated to open 1,000 messages. A resize re-renders the whole transcript. The whole transcript is held in memory, unpaged.
4. **Global side effects** (rows 11, 41). Plan and yolo apply to every thread. Session load and the sidebar toggle write the global config.
5. **Delegation is invisible** (rows 14, 30). Cards are parsed from text, `turn_end` isn't shown, sub-thread cards can't be opened, and `thread.open` / `task.race` render as raw JSON. The user can't start a race or a sub-thread at all.

Most of this goes away when the TUI becomes a protocol client (D3) and renders from a local view-model. Rows 26 and 29 are TUI code and must be fixed there.

### 3.4 Phone

1. **Questions are a dead end** (row 4). Passkey, then an error. This is the most user-hostile bug in Deck. It exists because the v3 wire is a hand-written mirror that lost the `answer` decision, and the native app never imported `questions.ts`.
2. **Model changes on the phone rewrite the computer's defaults and switch the lead** (rows 2, 3). Model and effort are keyed by kind. Opening the picker creates orphan sessions (row 23).
3. **The connection lifecycle is wrong** (rows 31, 33). The socket stays open and streaming in the background. There is no liveness check on foreground, and every flap refetches the transcript.
4. **Three diverging clients** (R1, R8). Fixes land in the per-PC PWA and are not carried over. The native app lacks questions, sub-threads, hand-off cards and markdown. It still has `$name` toggles that the PWA removed. It has no push notifications at all (`apps/mobile/README.md:120-122`).
5. **Payloads scale with history** (row 32): the unbounded inbox, the full-list broadcasts, full messages every 200 ms.

The shared TS core is the best-engineered part of the client side and should be kept. It has generation and revision guards, events received mid-refresh win over the snapshot, idempotency keys are reused across retries, and the offline queue has preconditions and receipts. v3 promotes its `client_op_id` and receipt model to the whole protocol.

---

## 4. Measurements we have

All measured at `f093055`/`eec5677` on linux/amd64 unless marked as an estimate.

**TUI** (`internal/ui/model/renderbench_test.go`)

| Benchmark | Result |
|---|---|
| `BenchmarkSessionLoad`, 1,000 messages, 140×40 | **1.08 s/op, 266 MB allocated, 24.3 M allocs**. pprof: 87% in `TotalHeight → AssistantMessageItem.Render → glamour`, 60% of that in thinking traces. |
| `BenchmarkDrawStreaming` (chat only) | 1.2–1.4 ms per frame, 230–280 KB and 14–21 k allocs per frame, at about 30 Hz per streaming message |
| `BenchmarkDrawIdle` | 0.24–0.44 ms |
| `BenchmarkView_MovingScroll_Uncached` | 0.9 ms per frame, 101 KB, 1.6 k allocs |

**Binary and RSS** (Go 1.24.7, default `go build`)

| Measurement | Value |
|---|---|
| `deck` binary | **68.9 MB**, not stripped |
| `deck --help` | 38.6 MB peak RSS, 0.07 s |
| `deck session list` | 42.4 MB peak RSS |
| `deck doctor` (includes probed CLIs) | 135.6 MB peak RSS, 0.47 s |
| TUI idle on landing, 200×50 pty | **70–73 MB RSS**, 1.65% CPU, 0 B/s output once settled (the intro writes ~220 KB) |
| TUI first frame in a pty that doesn't answer DA1 / OSC 11 | 3–6 s (immediate in real terminals) |

**Desktop bundle** (`vite build`)

| Item | Size |
|---|---|
| `dist/renderer` | **5.8 MB** |
| Main JS chunk (no code splitting) | **1,659 kB** (485 kB gzip) |
| CSS | 163 kB |
| Art | 3.5 MB of JPGs shipped (18 MB in source) |
| Fonts | ~520 kB across six families |
| Electron RSS at idle | *not measured*; estimated 350–600 MB before any engine or agent page |

**Phone payloads** (from code and contract; nobody has measured on a device, `apps/mobile/README.md:117-124`)

| Path | Payload |
|---|---|
| Streaming | the whole `Message` every 200 ms per streaming message; caps of 64 KiB text, 8 KiB per tool output, 256 KiB diff |
| Inbox | the full list of every session ever, sent 150 ms after any session, approval or busy event, then fetched again by the phone: 2× full inbox per busy toggle |
| Reconnect | 100 messages (up to 100 × 64 KiB of text) plus roster, mode and changes, on every flap |
| Frames | Noise chunks ≤ 65,518 B, reassembled frames up to 12 MiB. Relay limits: frame 1 MiB, mail 64 KiB, snapshot 256 KiB. Offline snapshot ≤ 60,000 B. |
| RPC timeout | 120 s before an error shows |

**Engine event volume**: SQLite rewrites the full message row every 33 ms while streaming (`internal/message/message.go:21`). Each pubsub subscriber has a 4,096-slot buffer. The desktop replay ring holds 4,000 full-message events (`pkg/rpc/feed.go:12`) and the phone ring 2,000 (`hub.go:18`). A few busy threads cycle the desktop ring in minutes, so any reconnect after that is a full resync.

We have **no** measurements of: Electron RAM, phone CPU/battery, end-to-end latency from token to pixel, or pubsub drop counts under load. The quality bar below adds them.

---

## 5. What this means for v3

### 5.1 Bug classes the foundation removes (D3, D4, D5)

| Class | Rows | Removed by |
|---|---|---|
| Lost, unnumbered or reordered events | 1, 5, 25 | One store per conversation. Events come from the engine log with a durable per-thread `rev` assigned before fan-out. Ordering is `rev`, never a timestamp or UUID. |
| Snapshot/stream races | 8, 25, 33 | Snapshots stamped with `rev`; the client subscribes from that `rev`. Resume replays from the log, not from a process-lifetime ring. |
| Full re-sends, unscoped feeds | 13, 28, 31, 32 | Text deltas, per-thread subscriptions, a per-host summary stream with no bodies. |
| Polling and stale panels | 16, 17 | Events for board, agents, limits and the lead. |
| Divergent derived state | 15 | Engine-owned attention and running state in the summary stream. |
| Prose as protocol | 14, 30, 34 | Structured `HandoffCard`, `TaskReport`, `Plan`, `turn_end`, and an author on every message. |
| Overloaded, kind-keyed writes | 2, 3, 19, 20, 22 | AgentSpec with explicit scopes. `SetLead` / `SetMemberSpec` / `SetDefaults` addressed by member id. Inheritance by default. |
| Duplicate posts, unreconciled optimism | 12, 18 | `client_op_id` on every write, and writes return the resulting state and `rev`. `Steer` is one atomic op. |
| Multiple engines, client-bound lifetime | 9, 10, 43 | One daemon per host, one config writer, threads that outlive clients. |
| TUI in-process coupling | 6, 7, 27, 28 | The TUI becomes a protocol client. Approvals and questions are keyed by thread, many at once, and shown in the Inbox (D7). |

The GPUI rewrite deletes rows 35–39. The cuts (D8, D13, D19) delete rows 41–43 and the PWA half of row 4.

### 5.2 What still needs tests

The protocol makes these bugs *preventable*. It doesn't make them *impossible*. The following stay in client or engine code and need tests:

- **Client reducers**: applying snapshot + deltas must equal the engine's final state, in GPUI, the TUI and the phone.
- **Multi-thread routing**: approvals and questions from several threads at once, answered from several clients at once.
- **Lists keyed by id under live reordering** (row 29), and confirmation on destructive actions.
- **Picker semantics**: a thread pick never changes defaults, and a subagent inherits the parent's spec unless a profile overrides it.
- **Rendering cost**: opening long threads (row 26) and per-token frame cost.
- **Connection lifecycle**: background, foreground, flapping and daemon restart (rows 31, 33).

### 5.3 Quality bar

These are requirements for v3, not suggestions.

1. **Deterministic event-replay tests from recorded sessions.**
   - Record real sessions at the adapter boundary: agent CLI output, approval and question requests, client ops with their `client_op_id`, and timing.
   - Check in about 20 of them: one agent, lead switch, subagents, sub-thread report, race, edit-and-resend, plan-limit wait, a 1,000-message thread.
   - Replay them through the engine with a fake clock, and through each client's reducer, headless.
   - Assert these invariants: per-thread `rev` is gap-free and monotonic; snapshot-at-`rev` + deltas == the final snapshot; all three clients produce the same message order and the same attention state; every hand-off is a typed card; no text parsing anywhere in the client path.
   - Replay with injected faults: a dropped connection at every 10th event, duplicate writes, a daemon restart mid-turn.
2. **Multi-thread and multi-client tests.**
   - 4 threads streaming at once, each raising an approval and a question while another client answers.
   - Two clients on one thread, one of them a teammate with the member role.
   - Assert that nothing is answered for the wrong thread, nothing is lost, and nothing is applied twice.
   - Run these in the engine with the in-memory transport (bufconn) and the generated clients.
3. **Drops are failures.** Any pubsub or feed drop counter above zero fails the test run. The soak test below must end with every drop counter at zero.
4. **No sleeps in new tests.** Tests wait on events or `rev`s, never on wall time. Existing sleep-based tests are quarantined and rewritten, not given more time.
5. **Release gate.** A build ships only when all of these hold:
   - all replay and multi-thread tests pass, three runs in a row;
   - a 30-minute soak with 4 busy threads and two clients ends with zero drops and zero ghost or missing rows when compared against the log;
   - the performance budgets are met: TUI and GPUI first paint of a 1,000-message thread under 100 ms; streaming traffic per thread proportional to new text, not to message size; phone idle traffic zero in the background;
   - the "Needs you" state matches the engine on every client in the replay suite.
6. **Stabilisation rules for agent-written code** (against R8).
   - No `WIP` commits on main. Agent work lands through reviewed branches.
   - A visual direction or interaction model changes only through a decision entry in this plan, not in a commit.
   - Each milestone ends with a stabilisation week: no new features, only fixes, tests and deletion of dead paths, stale comments and stale docs.
   - Every swallowed error (`.catch(() => {})`, `_ =` on a write) needs a comment saying why, or it fails lint.
