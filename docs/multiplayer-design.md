# Deck multiplayer: many agents, subagent trees, peers

Status: proposal. Phases 1 to 7 (§3 to §7, and §8.1 to §8.4) are
implemented, except the review stage; the rest is not. It builds on what
already ships (verified against `main` at 4508226): spawn and tasks
(`session/spawn.go`, `tasks.go`, `sched.go`), SUB capsules (`docs/sub.md`),
and the peer link (`internal/deck/peer`: SSH transport, pairing, pinning,
supervisor, team chat, capsule pull).

Goal: one Deck runs any number of agents of any kind, lets them start
subagents that start subagents, and works with other Decks (teammates and
machines) as one team, while staying local-first, CLI-agnostic, and never
calling a model API or reading a credential.

## 1. Where Deck stands

| Area | Today | Gap |
|---|---|---|
| Same-kind agents | A thread can hold `claude` and `claude2` (`manager.go:641`), but only through a spawn; `AddMember` (`manager.go:517`) has no UI caller | No way to add a second one; `$claude` picks the first live one (`address.go:121`); an agent's `thread.send {to:"claude"}` fails with two claudes (`thread.go:270`); the roster in `instructions` (`briefing.go:290`) is fixed at process start |
| Subagents | `agent.spawn`, ephemeral members with `parent`, optional worktree, results back via `notifyCreator` | Depth 1 (`spawn.go:85`), 4 per member, no cascade when a parent exits, full 40k-rune briefing, scheduler always spawns the default kind, no UI for apply/discard (`ApplyMember`/`DiscardMember` unused) |
| Shared tree | Capsule verify/apply/three-way merge | No leases, no overlap warning, no "who is editing what"; agents in one tree overwrite each other silently |
| Runaway brakes | 6 hops per user turn | Spawns, dispatches and results are not hops; no token/cost/time budget; no plan-limit-aware scheduling |
| Peers | Pairing, pinned SSH, `peer.status`, `console.post` (human chat), `sub.closure`/`sub.objects` | A peer cannot give another node work; agents cannot read or post team chat; chat dials fresh per send; no outbox, no presence beyond up/down, no shared thread; `docs/peers-design.md` status line is stale (phase 6 closed all but the first and the shared thread) |

## 2. What others do (October 2026)

| | Amoeba | mpai | manycode | Repowire | MCP Agent Mail | amux | Claude Code teams | **Deck target** |
|---|---|---|---|---|---|---|---|---|
| Same-kind fan-out | Y | N | N | Y | Y | Y | Y | Y, plus best-of-N race |
| Nested subagents | P | N | N | P | N | P | N (1 level) | Y, budgeted tree |
| Other machines | Y (hosted) | Tailscale | tunnel/relay | hosted relay | ? | N | via Anthropic | direct SSH, Deck relay |
| Multi-human presence | Y | Y | Y | P | P | N | N | Y |
| File leases | advisory | N | N | N | TTL + pre-commit | heartbeat | N | enforced at apply |
| Merge semantics | overlap warning | N | diff-bound approval | N | P | rebase | N | capsules + 3-way |
| Cost / plan limits | ? | N | N | N | N | N | N | Y (routing by headroom) |
| Local-first | N (hosted "Brain") | Y | P | P | Y | Y | P | Y |
| Any CLI | Claude, Codex | Claude, Codex | Y | Y | Y | Pi only | Claude only | Claude, Codex, OpenCode, Antigravity |

Amoeba (useamoeba.com, v0.1.x, Sep 2026) is the closest product: a
multiplayer GUI IDE with shared sessions, per-person agent columns,
overlap warnings, advisory locks, worktrees per joined session, role-gated
approvals and code-anchored memory. Its coordination ("Brain": transcripts,
plans, memories, overlap detection) is hosted and processed by a model API.
Its internals were read from search extracts only; the site blocked direct
fetches.

What Deck takes from them:
- Amoeba: overlap warnings *before* an edit; role-gated actions; memory pinned to code.
- Agent Mail / amux: path leases with TTL and heartbeat; a `review` stage before `done`.
- manycode: approvals bound to content (here: the capsule hash); one driver per agent with an attributed prompt queue and a side chat.
- mpai / Claude Code: a message from a peer never approves anything; remote turns run restricted.
- Repowire / Claude Code: hand-offs that need an ack; idle notices that cost no tokens; loop throttles.
- real-a2a / manycode: join tickets carrying addresses and the key fingerprint (Deck's `deck://pair` invite already does this).

Where Deck wins and should press: no hosted coordinator, any CLI, change
exchange by content-addressed capsules instead of branches, nested trees,
pinned-key SSH with capability-scoped ops, and the only scheduler that
knows each subscription's remaining plan window.

## 3. Same-kind agents (pools) — implemented

- **Add an instance.** `$claude+` in the composer (and a `claude+` row in
  the `$` popup) adds one; `$claude+ $claude+` adds two. The first member of
  a kind is called `claude`; when a second joins, a first still called
  `claude` is renamed `claude1` and the new one is `claude2`, so every
  instance keeps a name of its own (`thread.freeName`). Ids stay global and
  unique. Renaming a member to its kind's name is refused while its thread
  has another of that kind, and names cannot contain `*` or `+`.
- **Pool addressing.** A kind token means the pool (`choose` in `address.go`):
  - `$claude`: an idle member of that kind, else the one with the fewest
    queued messages; among equals the last speaker, then the first to join.
    With none running it starts a fresh one. The message line shows who got it.
  - `$claude*`: every live member of that kind.
  - `$claude2`: exactly that member.
  - `$claude×3 …`: a race (§6, not yet).
  Agents use the same rule: `thread.send {to:"claude"}` routes to the pool
  (excluding the sender), `to:"claude*"` fans out, and `$claude*` lines in a
  final reply relay to all of them. The error listing valid recipients
  names the pools.
- **Roster.** The `thread.roster` op lists members with kind, model/effort,
  status, running task, parent, branch and context use, and how to reach
  each pool. The join line in briefings explains the pool once, when a
  second member of a kind arrives. The start instruction mentions
  `thread.roster` and stays under its 1400-char cap.
- **Per-instance settings.** Model, effort and mode are already per member.

## 4. Subagent trees — implemented (review stage not yet)

- **Depth and limits** (`session/tree.go`). A member the user brought in is
  at depth 0; a subagent is one below the member that started it (the
  scheduler's are at 1). Config `max_depth` (default 3), `max_children` live
  subagents per member (4) and `max_subagents` per thread (12, the scheduler
  included: it waits for a free slot instead of failing the task) replace the
  old "a subagent cannot spawn" rule and the fixed cap of 4. Budgets in
  tokens and time are phase 3.
- **Cascade.** When a member exits, or a subagent is retired, its live
  subagents are stopped and the tasks they own cancelled, recursively.
  `keep_orphans` turns this off. A result whose creator has stopped goes to
  the creator's nearest live ancestor, marked "it was for X, who has stopped".
- **Waiting for children.** A member's turn ending does not settle its task
  while it has live subagents still working, or a task result its turn was
  not briefed on (a child can finish mid-turn). The result arrives as a
  message, starts its next turn, and that turn's reply settles the task.
  Settlement reads the cursor as it was when the turn ended (`answered`),
  so a next turn starting before `afterTurn` runs cannot confuse it.
- **Tasks carry kind, model and effort.** `store.Task` has `Kind`, `Model`,
  `Effort` (task.create and agent.spawn take them); the scheduler spawns
  that agent, and a retry starts the same kind again.
- **Scoped briefing.** A subagent's view starts at its own brief: no thread
  history, plus one line saying who started it, for which task, and whether
  it may spawn in turn. `thread.read` is still there.
- **Review stage** (not yet). Task status `review` between `running` and
  `done`, with a reviewer of another kind; moved to phase 10.
- **UI.** The agents pane lists members as a tree (subagents indented under
  their parent, with their branch); stopped subagents drop out unless their
  worktree still holds work. In the Tasks pane, `a` on an isolated task
  applies its worktree (`ApplyMember`) and `d` twice discards it.

## 5. Working in one tree: leases, overlap, presence — implemented

All in `session/leases.go`.

- **Leases.** Ops `lease.acquire {paths, minutes?, exclusive?, reason}`,
  `lease.release {paths?}`, `lease.list`. Paths are globs relative to the
  repository root (absolute paths and `..` are refused). A lease ends when
  its member releases it or exits, or after its time (default 15 minutes,
  at most 2 hours); it does not end with a turn, because work spans turns.
  Overlapping acquires are granted, and the answer names who else holds
  what, so agents coordinate instead of stalling. Leases live in memory
  only: a restart restarts the agents too.
- **Enforcement where Deck owns the write.** `sub.patch.apply`,
  `sub.patch.revert` and `sub.merge.resolve` into the shared tree refuse
  any path under another member's exclusive lease, before the user is
  asked. Writes into a member's own worktree are not checked. The user's
  own ApplyMember from the Tasks pane is not checked either: the user
  decides.
- **Overlap detection where it does not.** Every `KindFileChange` in the
  shared tree is recorded per file. When a member changes a file another
  live member changed in the last ten minutes, or one another member holds,
  Deck logs a notice (once per file and member in that window). Notices
  with `Via: overlap` are also shown in the agents' briefings, so both
  agents see it in their next prompt without starting a turn. Changes in
  isolated worktrees are left to capsules and merges.
- **Presence.** The agents pane shows each member's leases ("holds
  internal/** (exclusive)"); it refreshes when a lease changes
  (`LeasesChangedMsg`).
- **Approvals bound to capsules** (already so). An apply, revert or merge
  approval is asked with the capsule id in its summary, which is the key a
  session-wide allow is remembered by, so a different capsule asks again.
- Not done: Amoeba-style warnings before an agent's own edit tool runs (in
  ask mode that would mean matching each edit approval against leases),
  and lease paths in `peer.status` (phase 6).

## 6. Races (best of N) — implemented

All in `session/race.go`.

- **Starting one.** The user writes `$claude×3 fix the flaky test` (`*`
  does for `×`; `$claude×2 $codex×1 …` mixes kinds). Agents call
  `task.race {brief, agents: "claude*2, codex", judge?, title?}`, which
  needs orchestration on. A race has 2 to 6 entries and needs a git
  repository. All of Spawn's limits are checked for every entry before the
  first starts.
- **The board.** A race is a task (`Race`) that Deck owns. Its entries are
  its subtasks, one for each subagent, each isolated on a worktree with one
  attempt. Each entry's brief says that it is a race, that it works alone,
  and that it ends with what it changed and which tests it ran.
- **Results.** When an entry ends, Deck records what its worktree changed
  (`Diff`: files, added and removed lines). When the last one ends, the
  race is done with one line for each entry (status, diff, tokens,
  minutes, result). That summary goes to whoever started the race; the
  entries do not report one by one. With a `judge` kind, a judge subagent
  then gets each worktree's path and base (`git -C <path> diff <base>`) and
  ranks the entries in a task of its own, after the summary.
- **Compare and keep.** In the Tasks pane the entries sit under their race,
  each with "2 files +10 -3 · 12k tokens · 4 min"; `enter` shows its files
  and result. `a` on an entry that has ended keeps it (`KeepEntry`): its
  changes are applied to your tree, the entries still running are
  cancelled, the other entries' worktrees are discarded, and the race says
  which one was kept.
- Not done: an inline diff in the compare view (the entry's branch and
  files are shown; open the branch to read it), test results other than
  what the entry reports, and keeping an entry from an agent (the summary
  goes to the agent; the user keeps one).

## 7. Budgets, brakes and plan-aware routing — implemented

All in `session/brakes.go`.

- **Work budget per user message.** Spawns by members and the scheduler's
  dispatches count against `max_work` (default 24); hops keep their own
  limit of 6. Reaching either pauses the thread (`EntryPause`, with a reason
  for the work limit); while paused, spawns are refused and the scheduler
  hands out nothing. The user's next message or Continue resets both
  counters and kicks the scheduler. Results coming back are not counted:
  they finish work rather than start it.
- **Task budgets.** `store.Task` has `Budget` (tokens, cost, minutes),
  `Spent` and `Started`. Config `budget` applies to tasks run by subagents;
  a task's own budget wins field by field, and only its creator (or the
  user) can change it, so an agent cannot raise its own limit. A turn's
  tokens and reported cost are charged when it ends to every running or
  blocked task its member owns and every task owned by a member above it,
  so a parent's task pays for its subagents. Budgets are also checked
  mid-turn (usage and tool events, counting the turn so far) and at each
  scheduler pass (minutes). Over budget, the task is blocked with the
  reason, the owner and its subtree are interrupted (nothing cancelled),
  and the creator is told. Each new attempt starts with an empty `Spent`.
- **Loop guards.** One member may `thread.send` 12 times a minute; a
  recipient with 8 relays it has not read takes no more (with `all`, the
  others still get it). The existing duplicate drop stays.
- **Routing by plan headroom.** The session keeps each kind's last
  `KindLimits`. A spawn with no kind from the spawn or the task goes to the
  default agent unless that CLI's fullest current window (one that has not
  reset since it was reported) is `route_above_pct` full (default 90); then to
  the kind with the lowest fullest window under the limit, with kinds that
  report no limits after those, and a notice says so. 100 turns it off.

## 8. Peers: from chat to teamwork

Everything below goes through the existing pinned SSH link and the two
MCP tools; new inbound ops are opt-in per peer (`allow_inbound`).

1. **Plumbing** (done). Team chat, `peers.status` and capsule fetches use
   a link that is already up: the supervisor's MCP client, else a new
   channel on a link the peer opened, and dial only when neither exists
   (`Service.withPeer`). Chat that reaches nobody waits in
   `~/.deck/peers/outbox.json` (200 calls, 7 days) and goes when the link
   is back; each call carries an idempotency key, and `console.post` drops
   a repeat. A call the peer answers, even with a refusal, leaves the
   outbox. `Manager.Subscribe` moves to phase 8, its first consumer.
2. **Presence** (done). `peer.status {repos}` adds the agents (name, kind,
   status, task title, context %), the file leases in the repositories the
   caller named by root commit, and the plan windows when
   `peers.share_limits` is on. Each Deck asks the peers it has a link to
   every 20 seconds, over that link only, and the Team pane shows
   `vps/claude2 ▸ working · fix the flaky test · 40%`, the leases and the
   plans. Not done: matching by remote URL (the root commit alone names
   the project; forks share it).
3. **Agents in team chat** (done). `team.read {since}` and
   `team.post {text}`. A post carries the agent's name (`vps/claude2`), so
   the chat shows each origin: you, your agent, a teammate, a teammate's
   agent. Posts are redacted and limited to 4 a minute per agent; what
   `team.read` returns is marked as information, not instructions.
4. **Code contracts (remote tasks)** (done, see below). This is the main
   multiplayer step and comes before the ops sandbox (vsh), because it is
   what teammates want:
   - `contract.propose {title, brief, base, capsules, result}` asks a peer
     to do a coding task. `base` is a snapshot id; the receiver pulls missing
     objects via `sub.objects`, the same way capsules move today.
   - The receiving person sees a confirmation card and picks which agent or
     pool runs it, under their own login and plan. Agent text can never
     create, widen or confirm a contract.
   - It runs in an isolated worktree on the receiver. The only things that
     come back are a capsule (published to the requester alone) and a
     schema-checked summary. The receiver's transcript stays on the receiver.
   - `contract.status` and `contract.result` feed the requester's task board:
     a task can be owned by `vps/codex`, and its dependants wait on it
     like any other task.
   - Ops contracts (podman reports etc. through `vsh`) stay as designed in
     `peers-design.md` §3-4 and come later.

   As built: an agent calls `task.contract {peer, brief, title?}`. The
   person allows it in every mode (approval kind `share`; yolo, allowed
   tools and "allow for session" do not answer it). The requester snapshots
   the agent's tree and records the contract in
   `~/.deck/peers/contracts.json`; the base's objects are served to that
   peer alone while the contract is open (`sub.closure` covers a snapshot
   too). The task runs at once, owned by `vps`, then `vps/<kind>`, with one
   attempt; the scheduler never starts it locally and `r` refuses it. The
   receiver needs the requester to have role `contractor` (the role is the
   consent; `contract.propose` is in the default allow list), keeps at most
   5 proposals per teammate waiting, and shows each as a Team pane card.
   Accepting fetches the base, checks it out into
   `~/.deck/contracts/<peer>-*` as a new repository, and starts a thread
   there with the chosen agent and the brief as its task, in the configured
   mode. When the task ends, the receiver captures a capsule against the
   base, shares it with the requester alone, and sends `contract.status`
   and `contract.result` through the outbox. The requester fetches the
   capsule, records it on the task and finishes the task, which tells the
   agent that asked. The summary is redacted and capped at 4 KB and shown
   as the teammate's words, not instructions. A capsule that cannot be
   fetched at once is tried again five times over about an hour.

   The person proposes from the Team pane (`c` on a teammate) for the open
   session's thread, without the share approval: they act themselves. A
   proposal to a teammate that cannot be reached waits in the outbox; one
   it refuses when it arrives fails the task. Ending the task on the
   requester (cancel, or an agent's `task.done`) calls the contract off
   (`contract.cancel`): a proposal still in the outbox is dropped, an
   accepted one has its task cancelled and its agent stopped. A proposal no
   one answers within 24 hours fails on the requester and is declined on
   the contractor. Adapters say whether they are contract safe
   (`agent.ContractSafe`): claude, codex and opencode ask through Deck in
   ask mode; antigravity cannot, so it never runs a contract. A contract
   thread can never be switched to yolo.
5. **Shared threads.** First a read-only mirror (`history.read`, paged,
   only threads the owner marks shared). Then *join*: a teammate's messages
   enter the owner's thread through an attributed FIFO queue
   (`[ana@laptop] …`). Roles decide what they can do:
   - observer: read only;
   - participant: can post;
   - editor: can also answer approvals.
   Approvals never travel by default. Remote-originated turns run in the
   owner's current mode, capped at ask.
6. **Reachability.** Keep direct SSH and the invite's `alt=` addresses;
   recommend Tailscale or WireGuard in the docs. Add relaying through any
   paired Deck with a public address, which forwards an opaque channel
   while the two ends run SSH inside it, so it stays end-to-end and the
   relay sees nothing.

## 9. Security rules (all phases)

- Every message carries an origin; only a local human can approve, start a
  contract or widen a scope.
- Peer-provided text is data: rendered as text, never a `$` mention that
  relays, never shell input. Qualified names (`vps/claude`) exist only in
  display and ops arguments.
- Results crossing the link are capsules (hash-checked) and schema-checked
  JSON, redacted and size-capped on both sides.
- Leases, budgets and contracts fail closed on adapters that cannot be
  locked down (the `ContractSafe` flag from `peers-design.md`).

## 10. Phases

Each phase is one or two PRs and useful alone.

1. **Instances** (done). `$claude+`, pool addressing for users and agents,
   `$kind*`, `thread.roster`, pool hint in join lines, `$` popup rows.
   `peers-design.md`'s status line fixed.
2. **Trees** (done). Depth and limits, cascade, ancestor fallback for
   results, waiting for children, task kind/model/effort, scoped briefing,
   tree in the agents pane, apply/discard in the Tasks pane.
3. **Brakes** (done). Work budget, task budgets counted up the tree, loop
   guards, routing by plan headroom. A thread-wide spend budget is left
   out: the work limit and per-task budgets cover the runaway cases.
4. **Leases and overlap** (done). `lease.*`, apply-time enforcement,
   overlap notices that reach the agents' next prompts, leases in the agents
   pane; capsule-bound approvals already held.
5. **Races** (done). `task.race`, `$kind×N`, entries compared in the Tasks
   pane, keep one, optional judge.
6. **Peer plumbing and presence** (done). Chat over live links, outbox,
   rich `peer.status`, `team.read`/`team.post`. `Manager.Subscribe` moves
   to phase 8.
7. **Code contracts** (done). `contract.*`, confirmation card, remote
   runs on a copy of the tree, capsule results, federated task board,
   `peer/agent` owners.
8. **Shared threads.** Mirror, then join with roles and attributed queue.
9. **Reach and ops.** Deck-as-relay, `vsh` and ops contracts, `deck serve`.
10. **Review and memory.** The `review` stage with cross-kind reviewers;
    notes pinned to capsule/file hashes that get flagged when the code moves.

## 11. Risks

- Pool addressing changes what `$claude` means once there are two; the
  chosen name must always show, and `$claude2` must stay exact.
- Overlap detection from tool events is best-effort (agents can write via
  shell); apply-time enforcement is the guarantee, notices are the help.
- Deeper trees multiply plan usage quickly; budgets and the work limit must
  ship with depth, not after.
- Code contracts run a teammate's brief on your machine under your plan;
  confirmation, isolation and the capsule-only return path are mandatory.
- Relaying through a peer must never terminate the inner SSH session.
