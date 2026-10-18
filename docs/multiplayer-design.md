# Deck multiplayer: many agents, subagent trees, peers

Status: proposal. Phase 1 (§3) is implemented; the rest is not. It builds on what
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
| Peers | Pairing, pinned SSH, `peer.status`, `console.post` (human chat), `sub.closure`/`sub.objects` | A peer cannot give another node work; agents cannot read or post team chat; chat dials fresh per send; no outbox, no presence beyond up/down, no shared thread; `docs/peers-design.md` status line is stale |

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

## 4. Subagent trees

- **Depth and budget instead of fixed caps.** `orchestrate.max_depth` (default 3) and
  `max_children` (default 4) replace the hard `isEphemeral` refusal. Every
  spawn carries a budget (spawns, tokens, wall time) taken from its
  parent's remaining budget, so a tree cannot outgrow what the user started.
- **Cascade.** Interrupting or exiting a member cancels its live descendants
  (setting `orchestrate.orphans: "keep"` turns this off). A result whose creator is gone goes
  to the nearest live ancestor, then to the thread, never dropped.
- **Tasks carry kind, model and effort.** `store.Task` gains `Kind`, `Model`,
  `Effort`; `dispatch` (`sched.go:257`) spawns that kind, falling back to
  the routing in §7 when empty.
- **Scoped briefing.** A subagent's first prompt is its task brief, the
  capsules on the task, the parent's last message and the roster, not
  the 40k-rune thread tail; `thread.read` is still there when it needs more.
- **Review stage.** Task status `review` between `running` and `done`
  (opt-in per task, `review: "codex"`): the result goes to a reviewer of
  another kind first. Cross-vendor review is something single-vendor tools cannot offer.
- **UI.** The Team pane renders the tree (`├ claude2 ▸ task 4 · worktree`);
  `enter` on a subagent shows its transcript, `A` applies its worktree
  (`ApplyMember`), `D` discards it, `I` isolates the thread (`Isolate`).

## 5. Working in one tree: leases, overlap, presence

- **Leases.** New ops `lease.acquire {paths, ttl?, exclusive?, reason}`,
  `lease.release`, `lease.list`. Globs are repo-relative. A lease ends when
  its member's turn ends, the member exits, or the TTL (default 15 min)
  passes. Conflicting acquires are granted but return who holds what (as
  in Agent Mail), so agents can coordinate instead of stalling.
- **Enforcement where Deck owns the write.** `sub.patch.apply`,
  `sub.merge.resolve` and `ApplyMember` refuse to write into paths under
  another member's exclusive lease, unless the user approves.
- **Overlap detection where it does not.** Agents in a shared tree write
  with their own tools. Deck already sees every tool call's paths in the
  event stream and diffs the tree per turn (`diffdetect`). A `touched` map
  (path → member, turn) raises a notice and a one-line steer to both agents
  when two members touch the same file in overlapping turns. Edits that
  come through Deck's approval (ask mode) are checked *before* they run.
- **Presence.** The changes panel and Team pane show `path ← claude2`
  for open leases and recent touches. Peers see the same, summarized (§8).
- **Approvals bound to capsules.** An approval for apply/merge records the
  capsule id; a different capsule asks again.

## 6. Races (best of N)

`$claude×3 fix the flaky test` or `$claude,codex,opencode …` spawns N
isolated subagents with the same brief. Each finishes by capturing a
capsule. A compare view lists them: files, `+/-`, the tests each ran and
their results, tokens and time. `enter` diffs one, `a` applies it, and the
others are discarded. Optionally a judge agent writes a ranking.
Worktrees and capsules already exist, so this is mostly UI and one op
(`task.race {brief, agents}`).

## 7. Budgets, brakes and plan-aware routing

- **Work budget per user turn.** Hops, spawns, dispatches and results all
  count against `max_work` (default 24 units). Crossing it pauses the
  thread like the hop limit does today (`relay.go:234`), with one line saying why.
- **Budgets.** Per thread and per task: tokens, reported cost (where the
  CLI reports it; no prices of Deck's own), wall time. Exceeding one blocks
  the task and asks the user.
- **Loop guards.** Per-sender throttle, duplicate drop (generalizing
  `lastRelay`), queue depth cap per member.
- **Routing by plan headroom.** `KindLimits` already reports each
  CLI's windows. When a task has no kind, or `$any`, the scheduler picks the
  kind with the most headroom in its 5-hour window, skipping a kind above 90%.
  The Limits pane shows which kind a pool will pick next.

## 8. Peers: from chat to teamwork

Everything below goes through the existing pinned SSH link and the two
MCP tools; new inbound ops are opt-in per peer (`allow_inbound`).

1. **Plumbing.** Team chat and every peer op go over the supervisor's
   connection instead of dialing per send. Add an on-disk outbox with
   idempotency keys (design §1 of `peers-design.md`). Add `Manager.Subscribe`
   (a fan-out of `emit`) for live mirrors.
2. **Presence.** `peer.status` adds agents (kind, status, task title,
   context %), plan headroom per kind (opt-in), and lease paths for
   repositories both sides have (matched by root commit and remote URL).
   Team pane: `vps/claude2 ▸ working · task 3`.
3. **Agents in team chat.** `team.read {since}` and `team.post {text}` ops.
   Each post shows its origin (local human, local agent, peer human, peer
   agent) and is rendered as text only.
4. **Code contracts (remote tasks).** This is the main multiplayer step and
   comes before the ops sandbox (vsh), because it is what teammates
   want:
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
2. **Trees.** Depth and budgets, cascade, ancestor fallback for results,
   task kind/model/effort, scoped briefing, tree UI with apply/discard/isolate.
3. **Brakes.** Work budget, task/thread budgets, loop guards, routing
   by plan headroom.
4. **Leases and overlap.** `lease.*`, apply-time enforcement, `touched` map
   with notices and steers, presence in panes, capsule-bound approvals.
5. **Races.** `task.race`, `$kind×N`, compare view, optional judge.
6. **Peer plumbing and presence.** Supervisor-connection chat, outbox,
   `Manager.Subscribe`, rich `peer.status`, `team.read`/`team.post`.
7. **Code contracts.** `contract.*`, confirmation card, remote worktree
   runs, capsule results, federated task board, `peer/agent` owners.
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
