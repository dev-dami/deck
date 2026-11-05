# 03 · Agents, delegation and everything running

Status: v3 plan, chapter 3. Date: 2026-11-05.

This chapter defines what an agent is in v3, how work is handed from one agent (or person) to another, how model and effort are picked, and the one place where you see everything that is running. It builds on decisions D3–D7, D15, D18 and D19 in the decisions list and does not re-argue them.

Related chapters:
- Visuals (colours, sizes, motion, the look of cards and rows): [04-design.md](04-design.md). Wireframes here show structure only.
- Hosts, Deck Link, fleet merge and moving a thread between hosts: [05-fleet-and-connect.md](05-fleet-and-connect.md).
- The protocol (revisions, snapshots and deltas, `WatchHost`, `WatchThread`, `client_op_id`) is in the protocol chapter ([07](07-protocol-and-performance.md)). This chapter names only the messages that belong to agents and delegation.

It replaces [../lead-agent-and-subthreads.md](../lead-agent-and-subthreads.md) and [../agents-and-mentions.md](../agents-and-mentions.md). Once v3 ships, mark both as superseded.

Evidence notation: `file:line` refers to the repo at `f093055`. External claims carry the research labels: **[V]** vendor page or docs, **[S]** secondary source, **[A]** anecdote. Every external claim comes from search-engine snippets: the research session could not open x.ai, docs.x.ai or most blogs. A claim marked **(snippet)** has not been checked against the full page.

---

## 0. What changes, in short

1. **Agents become persistent teammates.** A named agent is a host-level entity with a profile (name, description, kind, model, effort, tools/permissions, instructions). It is no longer just a recipe copied into each thread.
2. **Delegation becomes one typed object, the task.** A task has an owner, a delegate, an objective, a done-when, a status and a result. It shows up as a card that ends in a reviewable result. Today there are seven ways to delegate (subagent, sub-thread, task, race, review, scheduler, native subagent). In v3 there are three nouns: **task**, **run** and **sub-thread**.
3. **The person can delegate directly.** "Hand this to reviewer" is a verb in every client. Today it does not exist (`research-desktop.md` §3 item 7).
4. **One picker: `agent · model · effort`.** It has an explicit target and scope, never writes global defaults, never blocks on the catalog, lets you choose "Default" again, and changes effort in one action. `SetModel` is split into `SetLead`, `SetMemberSpec` and `SetDefaults`, plus one `GetCatalog`.
5. **Every reply shows the model and effort that produced it, and where each value came from.**
6. **One home: Inbox + Now**, across all hosts. Sections are Needs you, Review, Running and, later, Routines. The engine owns the status vocabulary and the attention rules. Clients never derive them.
7. **Inside a thread, a strip of child agents sits above the composer.** From it you can peek, steer or stop each child.
8. **Approve the delegation plan, not every step.** The orchestration level (off/assist/auto/full) becomes a three-value delegation policy: off, ask or auto.
9. **Routines** (scheduled or triggered runs on a host) come in phase 2.

---

## 1. The v3 domain model

### 1.1 Entities

| Entity | What it is in v3 | Today | Change |
|---|---|---|---|
| **Host** | One machine running one `deck` daemon. It serves every project on that machine and keeps threads running with no client attached (D3). It has a stable host key and a name ("vps-1"). | No host in the proto (`deck.proto:409-418` has `project` but no host id). Today there is one `deck serve --rpc` per project folder, and it dies with the desktop's stdin (`desktop-shell/src/main/decks.ts:249,410`; `internal/cmd/serve.go:325-329`). | New. Every id is scoped by `host_id`. See 05. |
| **Project** | A working directory on a host, with its `deck.json`. | The cwd, plus a Crush SQLite under `<project>/.deck` (`internal/config/config.go:19`). | Identified by `(host_id, path)`. The Crush SQLite stops being a message store (D4). |
| **Thread** | One conversation and one context. It has a lead run, child runs, tasks and sub-threads. It is stored once, in the engine log. | `store.Thread` (`store/store.go:153-217`), mirrored 1:1 to a Crush "session" through `deck-threads.json` (`deck_workspace.go:126,159-185`). | One store (D4). The word "session" leaves every client and the proto. |
| **Agent** | A **persistent named teammate** on a host, with a profile: name, description, kind, model, effort, tools/permissions, instructions. It can be in many threads at once and outlives all of them. Every installed CLI kind also appears as a plain built-in agent ("claude", "codex"). | A profile is a recipe in `deck.json` (`deck/profile/profile.go:34-56`). It has no description, no status, no identity beyond the member it is copied into (`manager.go:631-670`), and no list of where it is working. | New first-class entity, held by the engine (§2.2). |
| **Run** | **One agent working in one thread**, as lead, subagent or race entrant. It has a stable `run_id`, a resolved spec (with sources), a status, a last-progress time, a context meter and a cost. Its CLI process starts lazily and can be reaped when idle; the run continues with `--resume`. | `store.Member` (`store.go:68-115`) and `agentState` (`manager.go:1895-1985`). Ids are renamed when a second member of a kind joins (claude → claude1, `multiplayer-design.md` §3). Id and name are conflated (`pkg/rpc/team.go:184-190`). | Renamed from member to run. Ids never change and display names are never keys. `SetMemberSpec` keeps D5's name and takes a `run_id`. |
| **Task** | **A unit of delegation**: owner (you or an agent), delegate (an agent), objective, done-when, boundaries, check, budget, spec, status, result. It is shown as a card. | `store.Task` with its status machine (`store/task.go:13-60`) and `store.Contract` (`task.go:194-209`). Reports reach clients as prose (`task t3 "Title" done: …`, `session/handoff.go`). | It keeps the status machine and the evidence check. The hand-off terms are renamed from `Contract` to `Brief` (§1.4). It gains `delegate_agent`, `sub_thread`, a typed `TaskResult` and structured events. |
| **Sub-thread** | A thread with a parent: a separate context that reports back to whoever opened it. You can talk to it directly. | `Thread.Parent` and `ParentAgent` (`subthread.go:13-30`). The side chat is a hidden thread (`research-desktop.md` §3 item 4). | Same engine model. **Every task delegated to a named agent runs in its own sub-thread** (§2.6). The side chat becomes an explicit sub-thread. |
| **Subagent** | A short-lived run inside the parent's thread, bound to one task, with no user conversation of its own. It reports to its parent run when it stops. CLI-native subagents (a CLI's own task tool) appear read-only as children. | An ephemeral member with `Parent` (`spawn.go:15-60`, `155-165`). The person cannot message it (`session/lead.go:22-26`). Native subagents are `store.SubAgent` (`store.go:117-133`). | You can now **steer** or stop it from the child strip (§4.6). Steering goes through the task, and the parent sees it. You still do not chat with it. |
| **Race** | One task with N entrant runs, each on its own worktree, plus an optional judge. The result is a comparison card, and you keep one. | A task with `Race=true` (`race.go`), and `RaceEntrant{agent, model, effort}` (`deck.proto:1314-1320`). | Same model. The legacy `StartRaceRequest.kind/count` goes (`deck.proto:1302-1308`). |
| **Request** (approval / question) | Something a run is waiting on a human for. Kinds: `approval` (a tool call), `question`, `plan` (a plan to approve), `delegation_plan`. Each has a tier, normal or destructive, and an answer rule tied to roles (D15). | Approvals with `kind: "question"`. The phone cannot answer questions: they are classed as destructive, need a passkey, and then fail with `ErrInvalid` (`pkg/remote/policy/policy.go:21-35`; `pkg/remote/adapter/questions.go:33-49`; `research-mobile.md` B1). | One typed entity, answerable in place on every client. Questions are normal tier. |
| **Attention item** | An engine-derived row that says a human should look: Needs you or Review. It names who asked and on whose behalf. | Computed three ways: `room.attention` in the rpc layer (`pkg/rpc/room.go:91-127`), the phone daemon's `pending > 0` rule (`pkg/remote/daemon/daemon.go:232-262`), and the TUI's own. They disagree (`research-engine.md` A6). | Owned by the engine and published in `WatchHost` (§4.3). |

The terms the engine keeps as they are: queue/steer (`queue.go`), leases (`leases.go`), worktrees and capsules (`work.go`), budgets and brakes (`brakes.go`), `turn_end` (`session/turnend.go:10-27`), and the tree limits (`tree.go`: depth 3, 4 children, 12 subagents).

### 1.2 Entity diagram

```
 Host (deck daemon, host_id, name "vps-1")
  │
  ├── Agent*  (persistent; profile from deck.json + engine state)
  │     profile: name, description, kind, spec{model,effort}, tools/permissions,
  │              instructions, delegate_spec, budget/day, scope(host|project)
  │     state:   status (rolled up over its runs), runs*, cost today
  │        ▲
  │        │ agent_id (every run is "agent X working here")
  │        │
  ├── Project* (host_id, path)
  │     └── Thread*  (engine log, rev; delegation policy off|ask|auto; mode ask|plan|yolo)
  │           ├── parent ───────────────▶ Thread            (this is a sub-thread)
  │           ├── lead ─────────────────▶ Run
  │           ├── Run* {run_id, agent_id, role: lead|subagent|entrant|judge,
  │           │        parent_run?, task?, spec+sources, status, last_progress_at}
  │           │     ├── parent_run ─────▶ Run               (subagent tree, depth ≤ 3)
  │           │     └── native_subs*    (CLI's own subagents, read-only)
  │           ├── Task* {owner: person|Run, delegate: Agent, brief{objective, done_when,
  │           │         boundaries, check}, budget/spent, spec, status, result,
  │           │         runs*, sub_thread? ──▶ Thread, deps* ──▶ Task, race?, judge?}
  │           ├── Request* {kind: approval|question|plan|delegation_plan,
  │           │            tier, asked_by ──▶ Run, on_behalf_of ──▶ Task?}
  │           └── Log entries (messages, parts, typed cards: handoff, task_report, plan)
  │
  ├── AttentionItem* (derived: Needs you | Review; points at Request / Task / Run)
  └── Routine* (phase 2: agent, project, trigger, brief, budget)

 Person (identity key, role on host: viewer | member | admin; devices*)  — D15, see 05
```

Reading rules:
- An agent is **not** a thread. Talking to an agent always happens in a thread, and an agent can lead or work in any number of threads. This is how v3 avoids Grok Bot's single unbounded thread per agent (§2.1).
- A task is the **only** record of delegation. A subagent, a sub-thread opened to do work, a race and a review are all tasks with one or more runs.
- Ownership flows upward. A task's owner is the person or run that asked for it, so its result goes to that owner.

### 1.3 How the old delegation primitives map

| Today | v3 |
|---|---|
| `agent.spawn` (`spawn.go:58-165`) | `task.delegate` with `as: subagent`: a task plus one subagent run |
| `thread.open` (`subthread.go:47-115`) | `task.delegate` with `as: sub_thread`, or `thread.open` with no task when the lead just wants a separate conversation |
| `task.race` (`race.go`) | a task with `race` set and N entrant runs |
| Review stage (`review.go`) | a follow-up task to a reviewer agent, with `deps` on the build task |
| Board scheduler (`sched.go`) | the delegation policy `auto`: ready tasks start within budget |
| Native subagents | read-only child rows under their run |
| Workflow cards, written as prompt boilerplate (`desktop-shell/src/renderer/workflows.ts:114-150`) | typed templates that create tasks (§5.4) |

### 1.4 Cuts

| Cut | Where it lives today | Why |
|---|---|---|
| **"Contract", three meanings** | (a) task hand-off terms `store.Contract` (`task.go:194-209`); (b) peer code contracts (`peer/contract.go`); (c) ops contracts (`opscontract.go`, vsh) | (a) is renamed **`Brief`**, with the same fields: objective, expect, done_when, boundaries, check, explicit_done. (b) and (c) are deleted with team chat and vsh (D8). From v3 on, the word "contract" does not appear in the UI or the proto. |
| **`@`/`$` agent addressing** | `internal/deck/session/address.go` (305 lines), used only by `postAddressed` for capsule hand-overs (`capsulework.go:566`) and tests; the Manager's package doc still describes mention routing (`manager.go:15-35`); pool addressing `claude*`, `thread.freeName` renames, `scanRace`; desktop "Steer" buttons that insert `$name` into the draft (`desktop-shell/src/renderer/ui/Room.tsx:302-305`); the TUI's dead `@`/`$` completion path (`internal/ui/model/ui.go:4626-4645`); the phone's `$address` rail | Delete them. Capsule hand-over uses the typed `task.delegate`. `#` stays for files and `/` for commands. `@` in text is text. |
| **Legacy multi-member APIs** | `roster.add` / `AddAgent` on the phone wire (`pkg/remote/server/rpc.go:199-207`); the `lastSpeaker` lead fallback (`session/lead.go:31-52`); the flat `AgentKind/Name/Model/SessionID` mirror on `Thread` (`store.go:212-216`); `StartRaceRequest.kind/count`; `SetOrchestrateRequest.on` and `BoardResponse.orchestrate` (`deck.proto:1067-1095`) | Delete them. A legacy thread loads with its last lead, or its first live run, written once as the lead. |
| **`SetModel`** | `deck.proto:33,642-647` → `agent_picker.go:149-171` | Replaced by `SetLead`, `SetMemberSpec` and `SetDefaults` (§3.4). |
| **Orchestration level** off/assist/auto/full | `level.go`; set in three places (`research-desktop.md` §4) | Replaced by the delegation policy off / ask / auto (§5.5). Mode (ask/plan/yolo) stays as the permission setting. |
| **Profile `voice`; `role` as a separate field** | `profile.go:34-56` | `role` becomes the first line of `description`. `voice` is dropped: the value of a profile is its job (`agents-and-mentions.md` §1 already says so). Existing values migrate into `instructions`. |
| **Three model caches and two picker APIs** | adapter `modelcache`, `Manager.modelCache` (`modes.go:231-257`), `DeckWorkspace.models` (`agent_picker.go:73-100`); `AgentPicker` plus `Models`/`SetModel` | One engine catalog per host (§3.3). |
| **Hand-offs as prose, parsed with regexes** | `deck_workspace.go:503-517`; `pkg/remote/web/src/handoff.ts:22-30`, re-exported to the desktop | Typed `HandoffCard` / `TaskReport` events (D4). |
| **Notification bell and Highlights › Needs you** | `ui/NotificationBell.tsx`, `ui/HighlightsPanel.tsx:21,84,135` | Folded into the Inbox (D7). |

---

## 2. Agents as teammates

### 2.1 What we take from Grok Bot and what we fix

Grok Bot makes agents the organising unit: you get a roster of named Bots, each with a job, and you message them like coworkers [V] (x.ai launch post, snippet). Users like the "team of specialists" model [S] (Brian Lovin, snippet). Deck takes that idea and fixes the flaws that are documented.

| Grok Bot | Source | Deck v3 |
|---|---|---|
| A roster of Bots is the home, not a list of chats | [V] x.ai "Designing Grok Bot", **snippet** | The Agents roster sits on the home screen (§2.3, D18). Threads are still reached from the inbox. |
| A name, a job and a short description per Bot | [V] x.ai launch post, **snippet** | A profile with name, description, kind, spec, tools/permissions and instructions (§2.2) |
| **One continuous conversation per Bot**. Staff said parallel threads are "being looked at", and there is no fresh session or manual compaction. | [V-staff] Cursor forum, **snippet**; [S] Lovin, Garry Tan, **snippet** | **Threads are separate contexts.** An agent works in many threads, each delegated task gets its own sub-thread, and "New thread with reviewer" is one click. |
| Hides thinking steps and tool calls | [S] Lovin, **snippet** | Tool calls and reasoning stay visible, as today, in every client, sub-threads included. |
| Hand-offs between Bots land in the Bots' own chats, not in the group | [V] docs.x.ai chat-and-collaboration, **snippet** | **Every hand-off is a card in the thread where it was asked**, and its result comes back to the same place (§2.6). |
| No model or effort picker, and no indicator of what answered | [V] Cursor help "grok-bot/models", **snippet**; [S] MindStudio, **snippet** | Per-agent model and effort, with the value and its source on every reply (§3). |
| All Bots share one computer, so separate Bots are not a security boundary | [S] dev.to, Substack, **snippet** | Same caveat, stated plainly. Agents on one host share that host's files and CLI logins. Isolation comes from worktrees and modes, not from agent identity. The Agents page says so. |
| Approvals from routines expire after about 10 minutes | [S] learncursor.dev, **snippet** | Requests never expire silently. The run waits, and the phone gets a push (§4.3). |
| Monitor → preview → take over; live screens are not on by default | [V] x.ai design post, **snippet** | Row indicator → Follow (a pinned live preview) → Open (take over), per D18 (§4.5) |
| Stop also ends a Bot's background subagents | [S] gradually.ai changelog, **snippet** | Stopping a run stops its children, and their results no longer wake the parent (§4.6) |

### 2.2 The agent profile

A profile is still declared in `deck.json` (global `~/.config/deck/deck.json`, or the project's), so it can be versioned and shared (D6). The engine loads profiles into **agents**, which also carry runtime state. Agents are per host: a profile in the host's global config is available in every project on that host, and a project profile overrides a global one with the same name, as today.

| Field | Type | Meaning | Today |
|---|---|---|---|
| `name` | string, required | How the agent is shown and how it is delegated to by name ("reviewer"). The validation rules are the same as today (`profile.go` `badNameChars`, reserved names). | same |
| `description` | string, required, 1–3 sentences | **What this agent is for and when to hand work to it.** The lead reads it to choose a delegate, and it appears on the roster and on cards. The first line is the short role. | new (absorbs `role`) |
| `kind` | enum | The CLI it runs | same |
| `model` | `SpecChoice` | A model id, or `default` (the host default for the kind) | string, "" = kind default |
| `effort` | `SpecChoice` | A level validated against the model, or `default` (the model's default) | string, unvalidated (`modes.go:326-351`) |
| `delegate_spec` | `inherit` \| `own` | The spec this agent gets **when another agent delegates to it**. `inherit` (the default for built-in kind agents) runs it on the parent's resolved spec. `own` (the default for named agents that pin a model or effort) uses this profile's values. See §3.6. | new |
| `mode_cap` | ask \| plan \| yolo | The most permissive mode its runs may use. A thread in yolo still runs a `mode_cap: ask` reviewer in ask. | new |
| `tools` | allow/deny lists of tool classes (edit, shell, browser, network, mcp:<name>) | Passed to the CLI where it supports tool filters, and enforced by the approval broker otherwise | new |
| `paths` | globs | Where it may write. Briefed, and enforced by leases where the engine supports that. | same |
| `instructions` | text | Standing instructions, appended to its briefing | same |
| `budget` | `{per_task, per_day}` in cost or tokens | Defaults for tasks delegated to it, and a daily cap across all its runs | new (per-task budgets exist, `brakes.go`) |
| `color` | token name | Identity colour from the agent palette (04-design.md) | CSS colour |

Engine-side state, not stored in `deck.json`: `agent_id` (stable, derived from scope and name, and kept across renames by an alias table), its runs on this host, a status rolled up over those runs (§4.2), cost today, and the last result.

Built-in agents: every installed kind shows up as an agent named after the kind, with a fixed description ("General agent on the codex CLI"), `delegate_spec: inherit`, and model and effort from the host defaults. You cannot rename or delete them, but you can hide them.

**Agent memory across threads is not in v3.0.** Instructions plus the existing per-repo memory notes (`memory.go`) cover it. Per-agent memory is a phase-3 candidate, and it is bounded by the same rule: threads stay separate contexts.

### 2.3 The Agents roster

The roster is a section of the home screen (D18) and a page of its own. Each row shows the agent's identity, what it is doing, and how to give it work.

```
 Agents · all hosts                                          [+ New agent]
 ───────────────────────────────────────────────────────────────────────────
 ● ✳ reviewer      Reviews diffs for correctness and tests;   working · 2 threads
                   read-only.                                  claude · <model> · high
                                                               vps-1 · $1.20 today
 ○ ✳ builder       Implements a plan in a worktree; runs the   idle
                   checks before reporting.                    codex · <model> · medium
 ◐ ✳ researcher    Reads docs and the web; answers with        needs you (1)
                   sources.                                    claude · default · default
 ○ >_ codex        General agent on the codex CLI.             idle · built-in
 ───────────────────────────────────────────────────────────────────────────
 row actions:  New thread with…   Hand a task to…   Edit   Duplicate
```

- The status dot is the agent's rolled-up status (§4.2). "2 threads" opens a list of the threads it is in.
- The spec chip shows the profile's spec. A run that overrides it shows its own chip in Now and in the thread.
- On a fleet, the roster is grouped by host. The same profile name on two hosts means two agents, each shown with its host ("reviewer · vps-1").
- Phone (D19): the Agents tab shows the same rows. Tapping a row offers New thread, Hand a task, and Edit spec (model, effort, description). Full profile editing is on desktop and TUI.

### 2.4 Creating and editing a profile

- **Desktop.** "New agent" opens a form with name, description, kind, model, effort, delegate spec, mode cap, tools, paths, instructions, budget, and scope (this host or this project). Model and effort use the same popover as the composer (§3.5), with the catalog for the chosen kind. Saving calls `SaveProfile`. The engine validates the profile, writes it to the right `deck.json` with a per-key read-modify-write (not a whole-config save, which loses updates today: `research-desktop.md` B9), and returns the resolved agent.
- **TUI.** `/agents` opens the roster. `n` creates and `e` edits, both in one form view with the same fields. The model and effort fields open the picker panel.
- **Duplicate** copies a profile under a new name. This is the supported way to get a second reviewer with a different model, instead of Lovin's "copies to force context bubbles" workaround (Grok, [S] snippet).
- **Editing a profile never changes a running run silently.** Saving affects new runs only. If runs of this agent are live, the save result lists them, and the client offers "Apply to 2 running" as an explicit action. That action calls `SetMemberSpec` on each run, and each change shows in its thread as a notice.
- **Deleting** a profile with live runs is refused until they end or are stopped. The engine names the threads.

### 2.5 Delegation by profile

There are two entry points, and both produce the same task.

**1. The lead picks a delegate.**
- The lead's briefing lists the agents available on this host (name, one-line description, spec) and the op `task.delegate`. The engine also answers `agents.list` for the full descriptions.
- The lead chooses by reading descriptions. **The engine does no hidden routing.** It validates the choice (the agent exists, the limits allow it, the delegation policy permits it) and records the lead's one-line `why` on the card.
- Plan-headroom routing (`routeKind`, `brakes.go:353-386`) used to swap the kind silently. It becomes a **suggestion on the card** ("builder's plan is at 91%; codex has room"), which you or the lead can act on.
- If the policy is `ask`, the delegation becomes part of a delegation plan for you to approve (§5.5).

```
task.delegate {
  to: "reviewer",                 // agent name; built-in kinds allowed
  objective: "...", done_when: "...", boundaries?: "...", check?: "go test ./pkg/x",
  as?: "sub_thread" | "subagent", // default: sub_thread for named agents, subagent for built-ins
  isolate?: bool,                 // own worktree
  spec?: { model?, effort? },     // explicit override; shown with source "set by <lead>"
  budget?: {...}, why: "..."
}
```

**2. The person hands work over.**
- In the composer, "Hand to ▾" (or `/hand reviewer <text>`) sends the message as a task to that agent, owned by you. It skips the lead. The engine op is `Delegate` (§5.1).
- Typing "hand this to reviewer" as a plain message to the lead also works: the lead calls `task.delegate`, and the card shows the lead as owner on your behalf.
- From the roster, "Hand a task to…" opens a new thread whose first action is the delegation. Use this when there is no current thread.

### 2.6 The card, and the result that comes back

Every delegation renders as one card in the thread where it was asked, updated in place from typed `TaskChanged` events, never parsed from prose.

```
 ┌ ✳ lead → ✳ reviewer · task t7 ────────────────────────────── working ┐
 │ Review the retry change in pkg/remote for races                      │
 │ Done when: no data race under -race; findings listed with file:line  │
 │ claude · <model> · high  (profile)      budget $0.40 · used $0.11    │
 │ why: "reviewer is the read-only code reviewer"                       │
 │ last progress 12s ago · running `go test -race ./pkg/remote/...`     │
 │                                  [Peek]  [Follow]  [Open sub-thread] │
 └──────────────────────────────────────────────────────────────────────┘
```

When the delegate ends its turn with the done-when met (or with the check passed, via `evidence.go`), the card resolves into a **result card**:

```
 ┌ ✳ reviewer → you · task t7 ────────────────────────────────── review ┐
 │ 2 findings, 1 fixed in worktree t7 (+14 −3, 1 file)                   │
 │ • pkg/remote/hub/hub.go:112  ring index read without lock  (fixed)    │
 │ • pkg/remote/server/rpc.go:88 retry can double-post        (open)     │
 │ check: go test -race ./pkg/remote/...  passed 14:02                   │
 │ claude · <model> · high (profile) · 6m · $0.31                        │
 │ [Accept & apply]  [Send back…]  [View diff]  [Open sub-thread]  [⋯]   │
 └───────────────────────────────────────────────────────────────────────┘
```

Rules:
- **Visible hand-off.** The card lives in the asking thread. The sub-thread holds the work. Nothing is delegated where you cannot see it.
- **The result goes to the owner.**
  - If you own the task, it lands in **Review** in the inbox (§4.3) as well as in the thread.
  - If an agent owns it, the owner run receives a structured `TaskReport` (summary, status, artifacts, diff ref) instead of a prose relay. The card is still visible to you, and it is not an inbox item unless the owning agent's turn needs you.
- **Send back** opens a comment box. Comments on diff lines and artifacts are batched into the next message to the delegate (D18, the Antigravity pattern), and the task goes back to `running`.
- **Accept & apply** merges the worktree into the thread's tree (with undo, as today's apply) and marks the task done. **Discard** cancels it and drops the worktree, after a confirmation.
- `turn_end` (`handoff` / `blocked` / `done`) is a typed field on the report, and the card shows it. The TUI currently does not show it at all (`research-tui.md` B9).

### 2.7 Threads are separate contexts

- An agent can be in many threads. Each thread is its own context with its own runs and its own CLI session.
- "New thread with reviewer" starts a fresh context. "Continue" picks one of the agent's existing threads from its roster entry.
- A delegated task's sub-thread starts from the brief, not from the parent's history. A subagent's view of the thread already starts at its brief (`spawn.go` comment at line 165). The delegate can call `thread.read` on the parent when it needs more.
- Switching a thread's lead to another agent is a **hand-off of the thread**, not a model change. It is labelled as one ("Hand the thread to codex: it gets a brief of the history"), and the old lead goes idle. This keeps today's behaviour (`lead.go:96-145`) but stops hiding it behind a list choice (`research-tui.md` §1.2).

---

## 3. The model / effort / agent picker

### 3.1 What is wrong today (summary)

| Problem | Evidence |
|---|---|
| One call changes the lead, can add a member, writes the global default for the kind, and saves `deck.json` | `internal/workspace/agent_picker.go:149-171` |
| "" means "CLI default" in the proto, "keep the current one" in `SetLead`, and "reset the global default" in the picker | `deck.proto:641`; `session/lead.go:148-162`; `agent_picker.go:162-168` |
| "Default" cannot be chosen again once a model or effort has been set | `lead.go:157` (`cmp.Or(effort, curEffort)`) |
| "Current" is the first member of the kind, not the lead | `agent_picker.go:107-114` |
| TUI: three modal steps to change effort; the dialog waits up to 30 s for every CLI to answer, one after another | `internal/ui/model/ui.go:5270-5294`; `agent_picker.go:73-100` |
| Desktop: 5 clicks to set agent, model and effort; radios do nothing until the action button, so Escape drops the choice | `desktop-shell/src/renderer/ui/AgentPicker.tsx:176-178,268-276` |
| Desktop: lane "Apply" on a helper makes another member the lead | `ui/Room.tsx:309`; `ui/Lanes.tsx:430` |
| Phone: a helper's effort tap changes the lead and the global default; picking is keyed by kind; opening the picker creates orphaned sessions | `apps/mobile/src/screens/SessionSheet.tsx:196,286-305`; `apps/mobile/App.tsx:130-164`; `research-mobile.md` B2–B4 |
| A profile has no model list (`Models(kind="charles")` is empty) | `pkg/remote/adapter/convert.go:218` |
| The wire catalog drops default markers and descriptions | `convert.go:215-227` |
| Subagents ignore the parent's spec and take the global default for the kind, or a routed kind, without saying so | `spawn.go:75-90,155-165`; `manager.go:642-645`; `research-engine.md` B6 |

### 3.2 The rules

1. **One control in the composer: `agent · model · effort`.** Each of the three segments is its own target.
2. **The target is what the chip shows.** In a thread, that is this thread's lead run. On a new-thread composer, it is the agent that will take the first message. In the child strip, each child chip has its own picker, and it targets that run only. **Nothing in the composer can change another run.**
3. **The scope is always "this thread"** unless you choose otherwise. The popover header says so: `This thread · reviewer`. Two other scopes exist, each behind an explicit action that says what it does:
   - **Save to profile "reviewer"**, a button in the popover footer. It calls `SaveProfile`, and the save result says which running runs it does not touch.
   - **Defaults**, only in Settings › Agents. `SetDefaults` is never called from the composer, the child strip or the phone (D5, D19).
4. **Changing effort alone is one action.** The effort segment is a bar meter whose bars you can click (D18: "popover model picker with effort bars"). A key cycles effort, and on the phone a tap on the effort segment cycles it.
5. **"Default" can always be chosen again.** It is the first row of the model list ("Default · currently <model>") and the first stop on the effort meter ("Default · currently medium"). The wire carries `SpecChoice{default: true}`, never "".
6. **The catalog never blocks.** The picker opens at once from the engine's cached catalog and refreshes in the background. A kind that never answered shows "Default model" plus "Can't list models: <reason> · Retry". You can still pick Default and work.
7. **The source of each value is shown on every reply** (§3.7). Every change appears as a notice in the timeline ("You set effort to high for this thread · 14:02").
8. **Changing the agent segment is a hand-off of the thread**, and it says so before you confirm (§2.7). Model and effort apply in place. Where the CLI must restart to apply them, the response carries `restart_required`, and the chip shows "applies on next turn".
9. **An invalid combination is fixed and explained, never stored.** If you pick a model that lacks the current effort, the engine sets that model's default effort and returns a warning, which the chip shows ("high isn't available on <model>; using its default"). Today any string is stored (`modes.go:326-351`), and the desktop clears the effort silently (`AgentPicker.tsx:184-187`).

### 3.3 AgentSpec, sources and the catalog (engine)

```proto
// A value for one field of a spec, as a person or profile chose it.
message SpecChoice {
  oneof pick {
    string value   = 1;  // a catalog model id or effort level
    bool   default = 2;  // "use the next scope down" (profile → host default → CLI default)
  }
}

enum SpecSource {
  SPEC_SOURCE_UNSPECIFIED = 0;
  CLI_DEFAULT   = 1;  // the CLI's own default; nothing in Deck set it
  HOST_DEFAULT  = 2;  // Settings › Agents (SetDefaults)
  PROFILE       = 3;  // the agent's profile
  PARENT        = 4;  // inherited from the delegating run (delegate_spec: inherit)
  DELEGATION    = 5;  // set explicitly on the task by its owner (shown with who)
  THREAD        = 6;  // set in this thread with SetMemberSpec
}

message ResolvedField { string value = 1; SpecSource source = 2; string set_by = 3; int64 set_at = 4; }

// Always fully resolved by the engine and validated against the catalog.
message AgentSpec {
  string        agent_id = 1;
  string        kind     = 2;
  ResolvedField model    = 3;
  ResolvedField effort   = 4;
}

message SpecResult {
  AgentSpec spec              = 1;
  bool      restart_required  = 2;  // applies on the next turn
  repeated string warnings    = 3;  // e.g. effort replaced by the model's default
  uint64    rev               = 4;  // thread rev that recorded the change
}
```

**Resolution order** for each field (the first one set wins):

| Run role | Order |
|---|---|
| Lead | THREAD → PROFILE → HOST_DEFAULT → CLI_DEFAULT |
| Delegate (subagent, sub-thread lead, entrant) | THREAD (a later change you made on that run) → DELEGATION (explicit on the task) → PROFILE if `delegate_spec: own` → PARENT if `delegate_spec: inherit` → HOST_DEFAULT → CLI_DEFAULT |

**Catalog.** There is one catalog per host, held by the engine and persisted to disk with `fetched_at`. It is loaded per kind, not every kind at once, refreshed in the background on daemon start, every 24 h and on request, and announced with a `catalog_changed` event. This replaces the three caches (`research-engine.md` B1) and fixes a likely bug where a slow first load pins the static fallback list for the process's life (`claude/models.go:28-31` with `modes.go:246-255`; traced in code, not reproduced).

```proto
message CatalogModel {
  string id = 1; string label = 2; string description = 3;
  bool is_default = 4;                       // the CLI's default model
  repeated EffortLevel efforts = 5; string default_effort = 6;
  int64 context_window = 7; bool video = 8;
}
message EffortLevel { string level = 1; string label = 2; string description = 3; }
message KindCatalog {
  string kind = 1; repeated CatalogModel models = 2;
  int64 fetched_at = 3; string source = 4;   // cli | help | static
  string state = 5;                          // fresh | refreshing | stale | failed
  string error = 6;
}
```

Labels: the chip shows the catalog's `label`. Ids appear only in a tooltip. That ends the "one model under three names" problem (`research-desktop.md` §2, labels).

### 3.4 Engine ops

| Op | Request | Returns | Notes |
|---|---|---|---|
| `SetLead` | `thread_id, agent_id, client_op_id` | `{lead_run_id, SpecResult, handed_off: bool}` | Makes `agent_id` the thread's lead. If a different agent was leading, its idle run is reused or a new run is added and briefed, and a `HandoffCard` is logged ("codex took over from claude"). It does not change model or effort, and it does not touch defaults. |
| `SetMemberSpec` | `run_id, model?: SpecChoice, effort?: SpecChoice, client_op_id` | `SpecResult` | Changes one run's model and/or effort in this thread (source THREAD). It works on the lead and on any non-final child run you own. It never changes the lead or adds a run, and it never writes config. An absent field is left alone; `default: true` clears the THREAD value. |
| `SetDefaults` | `kind, model?: SpecChoice, effort?: SpecChoice, client_op_id` | `{defaults, affected: {threads_on_host_default: n}}` | Settings only. Requires admin on the host (D15). Writes one key of the host's global `deck.json`. Runs whose source is HOST_DEFAULT pick up the change on their next turn, and each gets a notice. |
| `GetCatalog` | `kinds?: [], refresh?: bool` | `[KindCatalog]` | Always answers from the cache at once. `refresh` starts a background reload that ends in `catalog_changed`. It never needs a thread, so the phone no longer creates a session just to list models (`research-mobile.md` B4). |
| `SaveProfile` / `DeleteProfile` / `ListAgents` | profile with scope / name and scope / — | the resolved agent / — / `[Agent]` | Profile scope. `ListAgents` replaces `ListProfiles` and the roster calls. |

Every write echoes `client_op_id` and the resulting `rev`, so optimistic UI reconciles with the engine's answer instead of snapping back on the next poll (today the TUI polls the lead every 2 s: `research-tui.md` B17).

### 3.5 Interaction per client

**Desktop (GPUI)**

```
 composer
 ┌──────────────────────────────────────────────────────────────────────┐
 │ Ask reviewer…                                                        │
 │                                                                      │
 │ [✳ reviewer ▾] [<model> ▾] [▮▮▮▯]    ask ▾   # @files  ⏎ send  ⇥ hand │
 └──────────────────────────────────────────────────────────────────────┘
      agent          model     effort bars (click a bar = set that level)

 popover from the model segment
 ┌ This thread · reviewer ──────────────────────────┐
 │ ● Default · currently <model>        (profile)   │
 │   <model A>   fast, cheaper                      │
 │   <model B>   strongest reasoning                │
 │   <model C>   1M context                         │
 │ Effort  [Default] [low] [medium] [high] [max]    │
 │ ─────────────────────────────────────────────────│
 │ catalog 2h ago · refreshing…                     │
 │ Save to profile "reviewer"                       │
 └──────────────────────────────────────────────────┘
```

- Clicking a model row applies it at once and closes the popover. There is no separate Apply button.
- Clicking an effort bar on the chip, or a level in the popover, applies it at once: **one click for effort**. `Alt+E` cycles the effort of the focused target. A toast confirms the change ("Effort high · this thread") with Undo.
- The agent segment opens the agent list with descriptions, grouped Named / Built-in. Picking a different agent in an existing thread shows an inline confirm row: "Hand the thread to codex · it gets a brief of the history · [Hand off] [Cancel]".
- `Mod+L` opens the picker on the agent segment. Arrow keys move, Tab switches segment, Enter applies.
- Popovers follow the 140 ms motion rule and the layer stack in 04-design.md. Escape closes only the top layer.

**TUI**

`ctrl+l` opens a one-panel, non-modal picker docked above the editor. It shows three columns at once and opens instantly from the cache.

```
 This thread · reviewer                                    catalog 2h · ⟳
 Agent             │ Model                         │ Effort
 ✳ reviewer   ●    │ ● Default · <model> (profile) │   Default · medium
 ✳ builder         │   <model A>                   │ ● high
 ✳ researcher      │   <model B>                   │   max
 >_ codex          │   <model C>                   │
 ✻ claude          │                               │
 tab column · ↑↓ move · enter apply · p save to profile · esc close
```

- Moving in the Model or Effort column previews. Enter applies the column you are in and keeps the panel open. Esc closes it.
- **`alt+e` cycles effort without opening anything.** The lead line under the editor (`internal/ui/model/lead.go:121-158`, kept) updates and flashes once. This is the one-action effort change. `alt+m` opens the panel on the Model column.
- Choosing a different agent asks `Hand the thread to codex? y/n` on the panel's status row.
- The extra routes go: `ctrl+m`, the "Reasoning" command that ran the same code (`ui.go:5373-5377`), and the Crush thinking toggle (`research-tui.md` B13). The palette keeps one entry, "Agent, model and effort".

**Phone (apps/mobile, D19)**

- The composer chip reads `✳ reviewer · <model> · ▮▮▮▯`.
- **Tapping the effort bars cycles effort** (one tap), with a haptic tick and a toast with Undo.
- Tapping the agent or model part opens one bottom sheet with three stacked sections: agent list, model list, effort chips. Every tap applies at once. There is no jump back to the main sheet, which today forces a reopen (`SessionSheet.tsx:286-308`).
- The phone never calls `SetDefaults` and has no "save to profile" (D19). A profile's spec can be edited from the Agents tab, which says "affects new runs of reviewer".
- The phone targets runs by `run_id` and agents by `agent_id`, never by kind (`research-mobile.md` B3).

### 3.6 Delegation policy for specs

- **Subagents and sub-thread leads inherit the parent run's resolved spec**, unless the delegate's profile says `delegate_spec: own` or the task sets an explicit spec. This holds for every path: subagent, sub-thread, race entrant (entrants usually set their own spec), review task and scheduled task. It replaces the five different rule sets in `research-engine.md` B6.
- Built-in kind agents default to `inherit`. A named agent that pins a model or effort defaults to `own`, because pinning is the point of the profile.
- **Changing the kind** (delegating to an agent of a different kind) cannot inherit a model. The model falls through to PROFILE, then HOST_DEFAULT. Effort is inherited if the target model supports that level. Otherwise the target's default is used, with a warning on the card.
- **Every delegation card shows the delegate's spec and the source of each field**: "codex · <model> (host default) · high (inherited from lead)". You can change it on the card before the task starts (policy `ask`) or at any time after (`SetMemberSpec` on the delegate's run, which shows as a notice in both threads).
- Plan-headroom routing never swaps silently. It becomes a suggestion on the card (§2.5).
- Users are angriest when they don't know what a child ran on. That is the common bug class across Codex, Cursor and Copilot ([A] issue reports, snippets; `research-external.md` §4). Deck's answer is to show the value everywhere, not to pick a smarter default.

### 3.7 The source on every reply

Each agent reply in the timeline ends with a quiet footer:

```
 ✳ reviewer · <model> · high · profile / thread              6m · $0.31
```

- Hovering (desktop) or tapping (phone) expands it: "model: profile 'reviewer' · effort: set by you in this thread at 14:02". In the TUI, the footer is the reply's dim header line.
- The values come from `turn_end` metadata on the message (engine-recorded at turn start), not from the current state of the run. That way old replies keep the spec they really ran with.
- A switch in the middle of a turn (where the CLI applied it in place) appears as a notice between parts.

---

## 4. Everything running

### 4.1 The home: Inbox + Now

One home replaces the three parallel "needs me" lists: the desktop Inbox page, the bell centre and Highlights › Needs you (`research-desktop.md` §4). It also fills the gap in the TUI, which has no cross-thread view at all (`research-tui.md` §3.5), and replaces the phone's flat three-state list (`daemon.go:247-262`). It spans **all connected hosts**. The client merges each host's `WatchHost` summary stream (05-fleet-and-connect.md). Every row says where it runs.

```
┌ Sidebar (inbox) ────────┐┌ Now ──────────────────────────────────────────┐┌ Follow ──────────────┐
│ ⌂ Home          ⌘0      ││ Team  ●reviewer ○builder ◐researcher ○codex   ││ ✳ builder · t9       │
│ NEEDS YOU             3 ││──────────────────────────────────────────────  ││ live · vps-1         │
│ ◐ Approve shell: rm -rf ││ ▾ Fix flaky hub test         laptop · deck    ││ $ go test ./pkg/...  │
│   build/ · builder · t9 ││   ● ✻ claude (lead)  working · 4m · 3s ago     ││ ok  pkg/remote 1.2s  │
│ ◐ Question: which API   ││     ├ ● ✳ builder  t9  working · 2m · 1s ago   ││ FAIL pkg/rpc ...     │
│   version? · researcher ││     │    codex · <model> · high (inherited)    ││                      │
│ ◐ Delegation plan (3)   ││     └ ◐ ✳ reviewer t10 needs you · waiting 40s ││ [Open] [Stop] [Steer]│
│   · lead in "Billing"   ││ ▸ Billing export              vps-1 · api     ││                      │
│ REVIEW                2 ││   ✓ review · reviewer finished t4 · +120 −14  ││                      │
│ ✓ reviewer: 2 findings  ││ ▸ Nightly deps check          vps-1 · api     ││                      │
│ ✓ builder: plan done    ││   ○ idle · last run 03:00 (routine, phase 2)   ││                      │
│ THREADS (came back)     ││                                                ││                      │
│ ● Fix flaky hub test    ││ group: host/project ▾   filter: all ▾          ││                      │
│ ✓ Billing export        ││                                                ││                      │
│ ○ Docs pass             ││                                                ││                      │
│ ROUTINES (phase 2)      ││                                                ││                      │
└─────────────────────────┘└────────────────────────────────────────────────┘└──────────────────────┘
```

- **Needs you:** requests (approvals, questions, plans, delegation plans) and anything blocked on a person.
- **Review:** results with something to look at (task results you own, a finished turn with a diff, a race to judge).
- **Running:** the live trees in Now. Rows are threads, with their runs nested underneath.
- **Routines:** phase 2 (§6).
- The **Team** strip at the top of Now is the roster in compact form (D18: team roster at home). Clicking an agent filters Now to its runs.

### 4.2 Status vocabulary (engine-owned)

The engine computes one status per run, per task and per thread, plus `last_progress_at`. Clients display the status and do not derive it. Colours are fixed tokens in 04-design.md.

| Status | Meaning | Derived from (engine events) | Clears when |
|---|---|---|---|
| **working** | A run is in a turn and making progress | turn started; any part delta, tool call or tool result stamps `last_progress_at` | the turn ends |
| **needs you** | A person must act before work can go on | an open Request (approval, question, plan, delegation plan); a brake pause that needs a decision (budget exhausted, the `max_work` or hop limit, `brakes.go`); `turn_end = blocked` addressed to the person | the request is answered, or the pause is resolved by an action. **Never by viewing.** |
| **blocked** | Cannot go on, and the reason is not you | plan-limit wait (`waits.go`, with "resumes 14:20"); a dependency task failed or is not done; a lease conflict; the host is offline (client-side, from the connection) | the cause clears |
| **review** | Finished with something for you to look at | a task you own reached `done` or `review` with a result; a turn ended with a diff or artifact in a thread you lead; a race finished | Accept, Send back, Discard, a reply in the thread, or "Mark reviewed". Opening it marks it read but keeps it in Review. |
| **done** | Finished, and nothing is waiting on you | a task reached `done` and was accepted; a turn ended `done` with no reviewable output | — |
| **failed** | Ended without success | a task reached `failed`; the CLI process exited non-zero or crashed; an adapter error | retried, dismissed, or superseded by a new turn |
| **idle** | Nothing running and nothing pending | no active turn, no open request, no live child | — |

- **No progress** is a flag, not a status. A `working` run whose `last_progress_at` is older than 5 minutes shows "no progress for 6m" in the warning colour. Linear treats a session as stale after 30 minutes of no activity [V] (snippet); 5 minutes fits CLI turns, and it can be configured per host. This answers "parents can't tell if a child is progressing" (Codex issue #16900 [A], snippet).
- **Roll-up** for a thread or agent row takes the highest of its own and its children's statuses, in this order: needs you > failed > blocked > review > working > done > idle. D18 says one status per thread, in priority order. A thread whose child needs you shows needs you, and the row names the child.
- Task statuses (`task.go` pending/ready/running/review/blocked/done/failed/cancelled) map onto this list. pending and ready become idle with the label "queued". cancelled becomes done with the label "cancelled". `TaskBlocked` with a user cause becomes needs you, and with any other cause becomes blocked. The engine splits today's single `blocked` ("a dependency failed, or it needs the user", `task.go:22`) into those two causes.

### 4.3 Attention rules (owned by the engine)

The engine keeps one list of `AttentionItem`s per host, published on `WatchHost`. These rules are written once, in `internal/deck/attention` (new package), and tested as a table.

1. **What creates a Needs-you item:** an open Request of any kind; a brake pause that needs a decision; a run with `turn_end = blocked` waiting on the person; a delegation plan under policy `ask`.
2. **What creates a Review item:** a task result where the owner is a person; a finished race; a turn that ended with a diff or artifact in a thread the person leads; a routine run with output (phase 2). A routine run with no output is auto-archived, not shown (§6).
3. **What creates neither:** reports between agents (an agent owns the task), routine progress, and running work. Running work shows only in Now.
4. **One item per thing.** A thread with three pending approvals is one row with "3". The item names who asked and on whose behalf: "builder (delegate of claude, task t9) wants to run `rm -rf build/`".
5. **Cleared by action, never by viewing.** This is carried over from the desktop's `attention.ts`. "Read" (unbolded) and "cleared" are separate states.
6. **Who can act** follows roles (D15). Viewers see items but cannot act. Members answer normal-tier requests. Destructive-tier requests need an admin, with a passkey on the phone. Questions and plans are normal tier. Each item carries `can_answer` for the viewing person, computed by the engine.
7. **Nothing expires silently.** A request from a routine or a background run waits. It is pushed to the phone, re-surfaced in the daily summary, and cancelled only by its own run's budget or by a person.
8. **Order:** Needs you and Review are each sorted by "came back to you" time, newest first (D18), and each row shows how long it has been waiting.
9. **Push:** Needs-you items are pushed (D11, E2E). Review pushes are a setting (on for tasks you started from the phone). Running is never pushed.
10. **Jump:** `Ctrl+Alt+L` (desktop, kept) and `alt+j` (TUI) go to the next Needs-you item across hosts (D18: jump to the next one needing approval).

```proto
message AttentionItem {
  string id = 1; string host_id = 2; string thread_id = 3;
  enum Lane { NEEDS_YOU = 0; REVIEW = 1; } Lane lane = 4;
  string kind = 5;              // approval | question | plan | delegation_plan | brake | result | race | failed
  string asked_by_run = 6; string on_behalf_of_task = 7;
  string title = 8;             // one line, engine-written
  uint32 count = 9; int64 came_back_at = 10; bool read = 11;
  string tier = 12;             // normal | destructive
  bool can_answer = 13;         // for the viewing principal
}
```

### 4.4 The Now tree

Each row of the Now tree is one of:

- **Thread row:** title, rolled-up status, host and project ("vps-1 · api"), lead agent, elapsed time, last progress, cost.
- **Run row** (nested under its thread or parent run): status dot, agent glyph and name, task id and title, spec chip with source, elapsed time, last progress, budget used, diff stat.
- **Native subagent rows** under their run, read-only and dimmed.
- **Sub-threads** nest under the run that opened them. The row links to the sub-thread.

Grouping: by host and project (default), by agent, or flat by status. Collapsed parents show a child count and the highest child status. The tree is fed entirely by `RunningSummary` upserts on `WatchHost` (no bodies, D4), so it costs nothing per token.

### 4.5 Peek, Follow, Open

D18 sets three levels of attention. Peek is the inline form of the first.

| Level | What you get | How |
|---|---|---|
| **Indicator** | Status, last progress, the current tool in one line | always, on the row |
| **Peek** | The row expands in place: the last 6 lines of output, the current tool call, and a reply/steer box, plus Stop. Peek opens a `WatchThread` for that thread only while it is expanded. | desktop: Space or click the chevron; TUI: Space; phone: tap |
| **Follow** | A pinned live preview in the right panel (terminal output, browser frames from the host-side browser, D10, diff so far). It stays while you navigate. | desktop: `F` or "Follow"; TUI: `f` opens a split; phone: "Follow" opens a full-screen live view |
| **Open** | Navigate to the thread and take over | Enter |

A live preview is never shown by default. Follow is always a choice, per D18 and the xAI rationale that prominent live screens invite supervision rather than delegation ([V] snippet).

### 4.6 The child strip in a thread, and stopping or steering

Above the composer, a thread with live child runs shows one chip per child. This follows the Codex pattern of active subagents above the composer [V] (snippet).

```
 ┌ children ───────────────────────────────────────────────────────────┐
 │ ● builder t9 · 2m · 1s   ◐ reviewer t10 · needs you   ✓ researcher t8│
 │                                                    [Stop all] [▾]   │
 └─────────────────────────────────────────────────────────────────────┘
 [✻ claude ▾] [<model> ▾] [▮▮▯▯]   Ask claude…
```

- Clicking a chip opens its peek as a popover above the strip, with Steer, Stop, Open and that child's own `model · effort` picker (target: that run).
- The strip shows only direct children. Deeper descendants appear as a count on the chip ("+2"), and the peek shows their tree.
- Finished children stay as dimmed chips until their result is reviewed or accepted, then they collapse into the thread's card.

**Stop:**
- `StopRun(run_id)` interrupts the run and cancels its task. It also stops the run's own children.
- Their late results no longer wake the parent: the engine drops events whose run id is no longer active, which fixes the cancelled-run-overwrites-new-run race (`research-external.md` §5).
- The parent receives a typed `TaskReport{status: cancelled, by: <person>}`, so it does not wait or retry.
- "Stop all" stops every child, and it does not stop the lead. The phone's single Stop button kills every agent today (`research-mobile.md` B6); that goes away.

**Steer:**
- `SteerRun(run_id, text, client_op_id)` delivers the text into the child's running turn. Codex has native steer; for other CLIs it is interrupt-and-resend (`context-design.md` §2), using the existing queue/steer machinery (`queue.go:363-470`).
- For a subagent, the steer is recorded on its task and shown in the parent thread ("You told builder: use the v2 client"), so the parent agent sees it on its next turn.
- This replaces the dead Steer buttons that inserted `$name` (`ui/Room.tsx:302-305`) and relaxes the old rule that the person cannot message a subagent (`lead.go:22-26`): you can steer it, but you still do not chat with it.
- On a sub-thread lead, Steer is just a message to that sub-thread.

### 4.7 Engine data for Now

`RunningSummary`, per thread and with no bodies, carries everything Now and the inbox need: `thread_id, host_id, project, title, parent_id, status, attention_count, lead {agent_id, spec}, runs [{run_id, agent_id, role, parent_run, task_id, task_title, status, spec, started_at, last_progress_at, current_tool, cost, budget, diff_stat}], last_line (≤280 chars), came_back_at, rev`. The protocol chapter owns the stream mechanics. This chapter owns the fields above and the status and attention rules.

The engine must emit events it emits today but drops before clients see them: `TaskChangedMsg`, `ModelChangedMsg`, `MemberAddedMsg` and `WaitChangedMsg` become usage telemetry only (`deck_workspace.go:555-580`). It must also stop recomputing a whole session list per roster change (`pkg/rpc/room.go:171-179`). In v3 these become `RunChanged`, `TaskChanged`, `AttentionChanged` and `RunningSummary` upserts. The desktop's 3 s / 10 s board and agents polling (`crewState.ts:7-10`) goes.

### 4.8 Per client

| | Desktop (GPUI) | TUI | Phone |
|---|---|---|---|
| Home | Sidebar inbox + Now + right Follow panel (wireframe above). `Mod+0` goes home. | **`alt+n`** toggles a full-screen Inbox + Now view (one key, D7). It shows the same sections; `j/k` move, Space peeks, Enter opens, `y/n` answer the focused request, `f` follows. | Tabs: Inbox (Needs you + Review, answerable in place), Now (trees), Agents. Thread view from any row (D19). |
| Peek | Inline expand / popover | Inline expand under the row | Bottom sheet |
| Child strip | Above the composer | One line above the editor: `children: ● builder t9 2m · ◐ reviewer t10`. `alt+1…9` peek a child. | Horizontal chips above the composer; the approval pill expands into a sheet and never docks over the transcript (`research-mobile.md` B5) |
| Stop / steer | Peek buttons | `x` stop (confirms), `s` steer in the peek | Swipe actions in Now; buttons in the sheet |

Key choices in this table were checked against the current TUI keymap (`internal/ui/model/keys.go`): `alt+n`, `alt+e`, `alt+m`, `alt+j` and `alt+1…9` are free. The final keymap lives in the design chapter ([04](04-design.md)).

---

## 5. User-initiated delegation flows

Every flow creates one or more tasks with **you as the owner**. Every flow shows a card in the thread, runs on the host the thread lives on, and returns its result to Review. Starting a flow is your approval of that delegation, so you are not asked again. Tool-level approvals inside the delegate still follow the thread's mode and the delegate's `mode_cap`.

### 5.1 Delegate a task to a profile

1. In the composer, choose "Hand to ▾" and pick an agent, or type `/hand reviewer` (desktop and TUI). On the phone, use the "Hand to" chip.
2. The composer gains a small form: **objective** (the message text), **done when** (optional, with suggestions drawn from the agent's description), **isolate** (own worktree; on by default for agents with edit rights), and **budget** (from the profile's `per_task`).
3. Send. The engine creates the task and the sub-thread, and the card appears at once.

```proto
rpc Delegate(DelegateRequest) returns (DelegateResponse);
message DelegateRequest {
  string thread_id = 1; string to_agent_id = 2;
  Brief  brief = 3;                 // objective, expect, done_when, boundaries, check, explicit_done
  bool   isolate = 4; Budget budget = 5;
  SpecChoice model = 6; SpecChoice effort = 7;  // optional override (source DELEGATION)
  string as = 8;                    // sub_thread (default) | subagent
  string client_op_id = 9;
}
message DelegateResponse { Task task = 1; string sub_thread_id = 2; uint64 rev = 3; }
```

### 5.2 Start a race (best of N)

- Use `/race`, or "Best of N" in the Hand-to menu. The builder takes the objective, 2–6 entrants (each an agent plus an optional spec; kinds can be mixed and repeated), an optional judge agent, and a budget per entrant. It is the desktop's `RaceBuilder` design, now in every client. Today the TUI and the phone cannot start a race (`research-tui.md` §1.3; `research-mobile.md` §1).
- Each entrant runs in its own worktree, with one attempt.
- The result is a comparison card: diff stat, check result, cost and time per entrant, and the judge's verdict with reasons. **Keep** applies one entrant and discards the others, after a confirmation (today `a` in the TUI keeps one with no confirmation: `research-tui.md` B11).
- `StartRace{thread_id, brief, entrants: [{agent_id, model?, effort?}], judge_agent_id?, budget_each, client_op_id}`.

### 5.3 Open a sub-thread

- "Open sub-thread" replaces the hidden side chat. It is available from the thread menu, `/sub`, or a message's "Discuss in sub-thread". You pick an agent (default: the current lead's agent), give an optional brief (with a "Include a digest of this thread" toggle), and choose whether to isolate it.
- You talk to the sub-thread directly. What you say there stays there. **Send to parent** posts a typed summary card to the parent thread, carrying over today's rule (`lead-agent-and-subthreads.md` decision 2).
- `OpenSubThread{parent_thread_id, agent_id, brief?, include_digest, isolate, client_op_id}`. No task is created unless you mark it "Track as task", which adds a done-when.

### 5.4 Plan → build → review

Workflows become **typed templates that create linked tasks**, not prompt boilerplate in your message (`workflows.ts:114-150`). v3 ships three of them:

| Template | Tasks created | Where you are asked |
|---|---|---|
| **Plan → build → review** | t1 plan (planner agent, plan mode, read-only) → t2 build (builder, worktree, `deps: [t1]`) → t3 review (reviewer, read-only, `deps: [t2]`) | After t1: the plan arrives as a `plan` request in Needs you (approve, edit, or send back). After t3: the review result arrives in Review. |
| **Build and review** | t1 build → t2 review | After t2 |
| **Second opinion** | t1 to a second agent (different kind by default), read-only, given the same objective and the lead's answer | After t1 (a Review item comparing the two answers) |

- The template picker sits in the Hand-to menu and on the new-thread screen. Each slot defaults to the agent whose description best fits it (the role line names "planner", "builder", "reviewer"), and you can change any slot before starting.
- `StartWorkflow{thread_id, template, objective, slots: {planner, builder, reviewer}, budget, client_op_id}` → the tasks with `deps`. The board already supports deps and statuses (`task.go:46-56`), and the scheduler starts t2 when t1 is done (`sched.go`).
- Cut: "Split by area" and "Full crew" (`workflows.ts:43-106`). The lead does these under the `auto` policy, with a delegation plan.

### 5.5 Budget and approval rules

**The delegation policy** is set per thread in the composer's mode menu. It replaces the orchestration level (`level.go`). Defaults come from Settings.

| Policy | The lead may… | Replaces |
|---|---|---|
| **off** | not delegate. User-started flows (§5.1–5.4) still work. | `off` |
| **ask** (default for new threads) | propose delegations. Each turn's proposals are gathered into **one delegation plan** for you to approve. | `assist` |
| **auto** | delegate within the thread's budget and the tree limits without asking. Anything over budget or limit becomes a delegation plan. | `auto`, `full` |

**The delegation plan** is one Needs-you item (Request kind `delegation_plan`). This is the Devin pattern, where the coordinator proposes children for approval before launching them [V] (snippet).

```
 ┌ claude proposes 3 delegations · "Billing export" ─────────── needs you ┐
 │ 1 ✳ builder    implement CSV export      codex · <model> (profile) ·   │
 │                worktree · $0.60          high (inherited)        [edit]│
 │ 2 ✳ reviewer   review t1 when done       claude · <model> · high       │
 │                read-only · $0.30         (profile)               [edit]│
 │ 3 >_ codex     write fixture data        inherited · $0.10       [edit]│
 │ total $1.00 of $3.00 thread budget · depth 1 of 3                       │
 │                       [Approve all]  [Approve selected]  [Reject…]      │
 └─────────────────────────────────────────────────────────────────────────┘
```

- **Approve the plan, not every step.** Once approved, children run without further delegation prompts. Their tool calls follow the mode and `mode_cap` as usual.
- **[edit]** changes the agent, spec, budget or isolation per child before approving. Edits are recorded with source DELEGATION.
- **Mode never widens down the tree.** A child runs at most at its parent's mode and at most at its profile's `mode_cap`. A turn started by a member (D15) is capped at ask, and so is everything it delegates.
- **Approval tiers:**
  - A delegation plan is normal tier, so any member or admin can approve it.
  - It becomes destructive tier, admin only (passkey on the phone), if any child asks for yolo, writes outside a worktree to a branch other than the thread's, or the plan raises the thread budget.
  - Viewers cannot approve.
- **Budgets:**
  - Each task has a budget in cost, tokens or minutes. It defaults to the delegate profile's `per_task`, capped by what remains of the owner's budget.
  - Spending is charged up the owner tree, as today (`brakes.go`; `chargeTurnLocked` walks `Parent`).
  - The per-message brakes (`max_work` 24, hops 6) and the tree limits (`tree.go`: depth 3, 4 live children per run, 12 subagents per thread) stay as hard caps. The plan card shows them.
  - New: each agent's `per_day` budget caps all its runs on the host together. That cap matters for always-on hosts and routines.
- **When a budget runs out,** the task pauses and raises a Needs-you `brake` item with three choices: raise by an amount, accept the partial result, or stop. It never silently continues and never silently drops work.
- **Teammates spend the host owner's CLI plans** (D15). A member's delegations therefore count against the same plan limits, and the plan card shows whose plan pays.

---

## 6. Routines and background runs (phase 2)

D3 already makes background runs real in v3.0: the daemon keeps threads running with no client attached, so a long task or race goes on after you close the laptop. "Keep running on vps-1 when the lid closes" moves a thread to an always-on host (D17, see 05-fleet-and-connect.md). Phase 2 adds **routines**: runs that start themselves.

What exists today: `ScheduleSend` waits out plan limits only (`waits.go`, `store.Scheduled`). The board scheduler runs ready tasks only inside a live thread (`sched.go`). There are no cron jobs, triggers or watchers (`research-engine.md` §2).

**Model**

```proto
message Routine {
  string id = 1; string host_id = 2; string project = 3;
  string agent_id = 4;                  // who does the work
  Brief  brief = 5;                     // objective, done_when, check
  oneof trigger {
    string cron = 6;                    // host-local time
    GitTrigger git = 7;                 // push to branch, PR opened (2b)
    string webhook_id = 8;              // signed POST to the daemon (2b)
  }
  string output = 9;                    // new_thread (default) | continue_thread:<id>
  string mode_cap = 10;                 // default ask
  Budget budget_per_run = 11;
  bool   archive_if_empty = 12;         // default true
  bool   paused = 13;
}
```

**Rules**
- A routine is owned by the host and runs on it, whether or not any client is connected. Only admins can create routines on a host (D15).
- Each run creates a thread (or continues one), titled "<routine> · <date>". It shows in Now while running and lands in **Review** when it has output.
- A run with no output (turn ended `done`, no diff, no artifacts, and the agent's report says nothing to report) is **auto-archived** and never shown in Review. This is the Codex Automations pattern [V] (snippet). The Routines section shows the run history with "3 empty runs archived".
- Requests from a routine run go to Needs you, are pushed, and **wait**. They never expire into "the action didn't run" (contrast Grok Bot, [S] snippet).
- A run is skipped, not queued, when the agent's CLI plan is above the host's `route_above_pct`, or when the agent's daily budget is spent. The skip is recorded in the routine's history and shown in Routines ("skipped: plan at 92%").
- Overlap: a routine whose previous run is still going skips by default (configurable: skip | queue one).

**Ops:** `SaveRoutine`, `ListRoutines`, `RunRoutineNow`, `PauseRoutine`, `DeleteRoutine`, and a `RoutineChanged` event on `WatchHost`.

**UI:** the Routines section of the inbox lists routines with next run, last result and status. An agent's roster entry lists its routines. Creating one is "New routine" on an agent (desktop and TUI). The phone can run now, pause, and review results, but not create.

**Phasing:** 2a ships cron and manual "Run now". 2b adds git and webhook triggers.

---

## 7. Engine and protocol work list

| # | Work | Package(s) | Removes |
|---|---|---|---|
| 1 | Agent entity: load profiles into agents with `description`, `delegate_spec`, `mode_cap`, `tools`, `budget`; stable `agent_id`; `ListAgents`, `SaveProfile` (per-key write), `DeleteProfile` | `deck/profile`, new `deck/agents` | Profiles as recipes; whole-config saves |
| 2 | Run: rename member to run; stable `run_id`; no renames; drop the legacy mirror and the `lastSpeaker` fallback | `session`, `store` | `claude1` renames; id/name conflation (`team.go:184-190`) |
| 3 | AgentSpec resolution with sources; validation against the catalog; `SetLead`, `SetMemberSpec`, `SetDefaults` | `session/lead.go`, `modes.go`, `pkg/rpc` | `SetModel` and `SetAgentModel` side effects; "" semantics |
| 4 | One catalog per host: persisted, per kind, background refresh, `GetCatalog`, `catalog_changed` | `agent/*/models.go`, `session/modes.go` | Three caches; 30 s blocking picker |
| 5 | Task as the delegation record: `Brief` (rename), `delegate_agent`, `sub_thread`, `TaskResult`, typed `TaskReport`/`HandoffCard` events; `task.delegate` op for agents; `Delegate`, `StartRace`, `OpenSubThread`, `StartWorkflow` for people | `store/task.go`, `session/spawn.go`, `subthread.go`, `race.go`, `review.go` | Prose relays and regex cards; workflow boilerplate |
| 6 | One spec policy for every delegation path (§3.6); routing as a suggestion | `spawn.go`, `subthread.go`, `race.go`, `review.go`, `sched.go`, `brakes.go` | Silent kind and model swaps (`research-engine.md` B6) |
| 7 | Delegation policy off/ask/auto; `delegation_plan` requests; mode cap down the tree | `session/level.go` → `policy.go` | The level, which was set in three places |
| 8 | Status and attention: one derivation (`internal/deck/attention`), `last_progress_at` on runs, `RunningSummary` and `AttentionItem` on `WatchHost` | new package; `pkg/rpc/room.go` deleted | Three attention computations |
| 9 | `StopRun` (cascading, drop late events by run id), `SteerRun` (subagents included, logged to the parent) | `session/queue.go`, `manager.go` | Dead Steer buttons; Stop that kills everything |
| 10 | Spec stamped on each reply (`turn_end` metadata) | `session/manager.go` turn start, proto `Message` | "Which model answered?" |
| 11 | Cuts from §1.4 | `address.go`, `pkg/remote/server/rpc.go`, `deck.proto` | |
| 12 | Phase 2: routines | new `deck/routines` | |

### Tests

- **Spec resolution:** a table test over role × `delegate_spec` × the set of scopes, asserting the value and source of each field. Choosing Default returns to the next scope down. No path writes config except `SetDefaults` (assert that `deck.json` is unchanged after `SetLead`, `SetMemberSpec` and every delegation path).
- **Effort validation:** an invalid effort for a model yields that model's default plus a warning, and is never stored.
- **Status and attention:** property tests over random interleavings of engine events (turn start and end, deltas, requests, answers, task transitions, stops), replayed from recorded CLI fixtures. They assert the derived status table (§4.2), roll-up order, "cleared by action, never by view", and that a stopped run's late events change nothing.
- **Delegation:** a user `Delegate` skips the lead and returns the result to Review. A lead's delegation under `ask` produces exactly one plan per turn. Mode never widens down the tree. A member's turn caps children at ask. Budget exhaustion raises a brake item.
- **Picker:** in each client, assert from replayed fixtures that effort changes in one action, that the picker opens with a cold catalog in a "failed" state and still lets you choose Default, and that the reply footer shows the source.

---

## 8. Deferred (decided not to do in v3.0)

- **Per-agent memory across threads.** Phase 3 candidate (§2.2).
- **Delegating to an agent on another host.** In v3.0 a task runs on the host of the thread that asked. To use another host's agent, move the thread (D17) or open a thread on that host.
- **Agent-to-agent group chats** (Grok Bot's channels). Agents coordinate through tasks and `thread.send` within a thread tree. Free peer chatter is what the lead model removed (`lead-agent-and-subthreads.md` §Why).
- **An intent dial** (quick / normal / deep) over model and effort, in the style of Amp's single dial [V] (snippet). It can sit on top of AgentSpec later, as a named preset, without changing the ops above.
