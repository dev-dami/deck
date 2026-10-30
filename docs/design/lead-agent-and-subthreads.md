# One lead agent, subagents and sub-threads

Status: proposed, 2026-10-30. Supersedes the user-facing half of
[agents-and-mentions.md](agents-and-mentions.md): the person no longer
addresses agents with `@`, and agents no longer hand work to each other with
`@name` lines. Named agents (profiles) stay, as dropdown entries.

## Why

Deck felt fragmented and looped. Every thread was a room of peers that the
person and the agents steered by mentions: a message went to whoever spoke
last, `@name` lines in replies started turns, and a single agent was briefed
to run a crew. Products that people return to (Codex, Claude Code) give one
agent per thread, picked from a dropdown, and let *that* agent decide when to
delegate. Claude Code's subagents cannot be talked to; they report to the
agent that started them. Magentic-One routes all work through one
orchestrator; MAST traces loops to free peer chatter. Deck keeps its crews,
but behind one lead.

## The model

| | Made by | The person can talk to it | Shown as |
|---|---|---|---|
| **Lead** (the thread's agent) | the person, from the composer's agent dropdown | yes: every message goes to it | the thread |
| **Subagent** | the lead (`agent.spawn`) | no: it reports to its parent and stops | a folded card under the parent's reply; the Agents tab |
| **Sub-thread** | the lead (`thread.open`), or the person (side chat) | yes, as its own thread with its own lead | nested under its parent in the thread list |

No `@` anywhere. `#` stays for files, `/` for commands.

## Decisions

1. **Switching the lead's kind mid-thread** (claude → codex) keeps the
   history: the next message goes to a member of the new kind, briefed on the
   thread so far (its cursor starts at 0, within the briefing budget); the old
   lead goes idle. Model and effort change in place (`SetModel`). A profile
   (named agent) is a dropdown entry like a kind.
2. **A sub-thread reports to its parent only for turns the parent asked for**
   (its brief, and each `thread.post`). What the person says inside the
   sub-thread stays there, unless they send it up ("Send to parent").
3. **Named agents stay** as dropdown entries and as kinds the lead may spawn
   or open sub-threads with (`kind: "charles"`). Teammates keep the Team
   panel; `@alice` is gone (team.post covers agent-to-teammate).
4. **All clients change at once**: engine, desktop, terminal app, phone.

## 1. Engine (`internal/deck/session`)

### Lead

- `thread.lead` (member id), stored in `store.Thread.Lead`. A new thread's
  first member is its lead. `defaultTarget` (whoever spoke last) goes.
- `Post` sends every user message to the lead. `ParseAddressing` is dropped
  from the user path: `@` and `$` are plain text. The thread's first message
  creates the lead from the default agent (or the dropdown's pick).
- `SetLead(threadID, kind|profile, model, effort)`: the same kind and profile
  only sets model and effort; another one reuses an idle member of it, or
  adds one, and makes it the lead. A notice says so ("codex took over from
  claude").
- Legacy threads with several members: the lead is the last one that spoke
  to the person; the dropdown lists their members.

### Agents talk through ops only

- `afterTurn` stops relaying `@name` lines (`parseMentions`, the "mention"
  via, goes).
- `thread.send` keeps working between a lead and its own sub-threads' leads
  and subagents (instructions to a running helper), never to the person's
  thread lead from a subagent except as its report.
- The person cannot message a subagent: `Post`, steer and queue refuse an
  ephemeral member ("subagents report to <parent>; open a sub-thread to talk
  to an agent").

### Sub-threads

- `store.Thread.Parent` (thread id) and `ParentAgent` (the member that
  opened it), and the reverse list in memory.
- Ops for a lead:
  - `thread.open {title, brief, kind?, model?, effort?, isolate?}`: makes a
    child thread in the same project (or a worktree with `isolate`), adds its
    lead and posts the brief as a message from the parent agent. Returns the
    sub-thread's id.
  - `thread.post {thread, message}`: a follow-up to one of its sub-threads.
  - `thread.children`: its sub-threads, their status and last result.
- Reports: when the sub-thread's lead ends a turn that answered the parent's
  brief or post (tracked by the entry's cursor, like `settleHanded`), its
  final reply goes to the parent agent as a relay with via "subthread",
  shown as a hand-off card with "Open sub-thread".
- Budget: turns a parent causes in its sub-threads charge the **parent
  thread's** turn ledger (`chargeTurnLocked` walks up `Parent`), so a lead
  cannot loop through children. A sub-thread the person talks to directly
  charges nothing.
- Levels: `off` lets the lead open sub-threads and spawn subagents only in a
  turn where the person asked; `assist`/`auto`/`full` as today, plus a
  `max_subthreads` per level (2/4/8/16).
- Side chat (desktop) becomes a sub-thread the person opens.

### API (`proto/deck/v1/deck.proto`, `pkg/rpc`, `pkg/remote`)

- `Session` gains `parent_id` and `lead` (`{kind, model, effort, profile}`).
- `rpc SetLead(SetLeadRequest{session_id, kind, model, effort})`; the remote
  protocol gets `session.lead.set`.
- `CreateSessionRequest` gains `parent_id` and `lead`.
- `ListSessions` returns children with `parent_id`, so every client nests
  them.

## 2. Desktop (`desktop-shell/src/renderer`)

- Composer: one **agent dropdown** (glyph, kind or profile, model, effort),
  like Codex's model picker, replaces the per-agent chips, the `@`/`$` pickers
  and the new-thread screen's "Write to" row. `#` files and `/` commands stay.
- Timeline: subagents render as folded cards under the lead's reply (the
  `SubTurn` component already does this for CLI-native subagents); the lanes
  strip goes; the Agents tab keeps the tree.
- Sidebar: sub-threads nest under their parent (indent, ↳, status dot);
  opening one shows "Sub-thread of <parent>" with a link back and "Send to
  parent".
- Hand-off cards: a sub-thread's report gets "Open sub-thread".
- Workflows (`workflows.ts`) become instructions to the lead: "Second
  opinion" opens a sub-thread of the other kind; "Build and review" keeps the
  reviewed task; a race's entries are subagents.

## 3. Terminal app (`internal/ui/model`) and phone (`pkg/remote/web`)

- TUI: `@`/`$` completion goes (`mentions.go`); `ctrl+l` (models) becomes
  the lead picker; the sidebar's sessions pane nests sub-threads; the
  agents pane shows subagents read-only.
- Phone: the composer's mention chips (`composer.ts`, `mentions.ts`) become
  a lead picker; the thread list nests sub-threads.

## 4. Tests

- Engine: every user message reaches the lead; `@codex` is text; switching
  the lead briefs the new one on the history; posting to a subagent is
  refused; `thread.open` reports once per parent-asked turn and not for the
  person's own turns; a parent's turn budget covers its sub-threads; legacy
  multi-member threads open with a lead.
- Desktop: the dropdown switches kind and model; a sub-thread nests and
  links back; its report card opens it; no `@` picker. Update the specs that
  type `$claude`/`@codex` (rpc, mentions, room-shots, overflow).
- TUI and phone: picker and nesting.

## 5. Order of work

1. Engine: lead, plain-text `@`, no mention relays, subagent refusal (one
   change; every client keeps working, `@` just stops routing).
2. Engine + API: sub-threads, `SetLead`, `parent_id`.
3. Desktop, TUI, phone together: dropdown, nesting, cards.
4. Docs: README "Agents and threads", the named-agents section, and
   agents-and-mentions.md marked superseded.
