# 07. One protocol, and the RAM and speed plan

Status: plan. Decisions D3, D4, D9–D11 and D13–D15 are binding here; this chapter turns them into a store, a proto, a transport and a set of budgets. Line numbers refer to `f093055` (Deck 2.0) unless a newer commit is named.

What this chapter decides, in one place:

| Question | Decision |
|---|---|
| Engine processes | One `deck daemon` per host. It serves every project on that host and outlives every client. |
| Store | **One SQLite database per host** (`~/.deck/deck.db`, WAL): an `events` table that is the per-thread log, plus materialised tables (`messages`, `parts`, `tasks`, `approvals`, `attention`, ...) written in the same transaction. The JSONL logs, the Crush per-project SQLite and `deck-threads.json` are imported once, then retired. |
| Ordering | Every thread has a stable `epoch` and a gap-free `rev` (uint64). Messages are ordered by the rev at which they were created. No timestamps or UUIDs in ordering. |
| Proto | New package `deck.v2` under `proto/deck/v2/`. `deck.v1` is frozen and deleted with the Electron app. Inside v2 only additive changes, checked by `buf breaking` in CI. |
| Streams | `WatchHost` (summary, no bodies) and `WatchThread` (snapshot at R, then ordered deltas). Paged `History`. One `Exec(Command)` RPC for every write, with `client_op_id`, returning the resulting rev. |
| Delivery | Lossless. A subscriber that falls behind is disconnected with a resume hint and resumes from its last rev. Nothing is ever dropped silently. |
| Transport | gRPC over HTTP/2 on local sockets only. Every remote path is Deck Link: a Noise IK session (the pkg/remote v3 stack) carrying deck.v2 messages in Link frames (Connect semantics). GPUI and the TUI reach remote hosts through the local link agent, which bridges gRPC calls to Link frames without decoding them; the phone speaks Link frames itself. One schema, one remote framing. `pkg/remote/wire` is deleted. |
| TUI | A client of the local daemon over the same protocol. It spawns the daemon if none is running. |

---

## 1. Today's data path

### 1.1 Diagram

```
 agent CLI (child process, one per member, started for every member when a thread opens)
   │  + `deck mcp-approve` (a second full Deck process per member)    approve/broker.go:147-155
   │ stdout
   ▼
 adapter (internal/deck/agent/*)
   ▼
 Manager.pump / onEvent  ── non-delta entries ──▶  STORE 1: ~/.deck/threads/<id>.jsonl + <id>.meta.json + index.json
   │                                                (deltas dropped: store/store.go:607-610, 680)
   │ Manager.out: ONE chan tea.Msg, cap 512, blocking emit        session/manager.go:355, 1677-1686
   ▼
 DeckWorkspace.pump: ONE goroutine for every agent of every thread  workspace/deck_workspace.go:478
   │ re-renders engine events as Crush chat rows; relays become prose "→ $codex · sub-thread <sid>"  :503-517
   ▼
 STORE 2: Crush SQLite <project>/.deck (sessions, messages)  ◀──  STORE 3: deck-threads.json (session ↔ thread)  :126
   │ message.Service: full-row rewrite every 33 ms                    message/message.go:21
   │ created/deleted on the LOSSY path                                 message/message.go:191, 227
   ▼
 pubsub.Broker.Publish: drops when a subscriber's 4,096-slot buffer is full   pubsub/broker.go:36-41, 170-192
   │                                    │
   ▼                                    ▼
 TUI (in-process)                       remote adapter: coalesce 200 ms, emit the FULL message each tick
 reads pubsub, also calls the           pkg/remote/adapter/events.go:19-21, 44-48
 engine directly from Update/View         │                                   │
 (37 Workspace type assertions             ▼                                   ▼
  in internal/ui)                       pkg/rpc feed: numbers events AFTER   phone hub: seq + 512/2000 ring,
                                        the lossy hops, ring 4000, random    focus-scoped; JSON wire v2/v3
                                        epoch per process, NOT scoped        inside Noise (pkg/remote/wire)
                                        rpc/feed.go:12, 38-60; server.go:81-86
                                          │
                                          ▼
                                        Electron desktop: one `deck serve --rpc` PER FOLDER, dies with stdin
                                        desktop-shell/src/main/decks.ts:247-252, 410; internal/cmd/serve.go:325-329
                                        + polls board/agents 3 s/10 s, git 15 s, team 30 s, limits 60 s
                                        desktop-shell/src/renderer/crewState.ts:7-10
```

### 1.2 Three stores for one conversation

1. **Engine log**: `~/.deck/threads/<id>.jsonl` plus `<id>.meta.json` and `index.json` (`internal/deck/store/store.go:1-26`). Global, no deltas, no revision, no fsync on the index (`store.go:1240`).
2. **Crush SQLite**: `sessions`/`messages` per project in `<project>/.deck` (`internal/config/config.go:19`). This is what every client reads. It is a lossy re-rendering of store 1.
3. **`deck-threads.json`**: the Crush session id ↔ Deck thread id map (`internal/workspace/deck_workspace.go:126`).

Plus in-memory copies: the engine log window (8 MB per live thread, `session/window.go:26-35`), workspace turn buffers, message-service pending state, pubsub buffers, the rpc ring (4,000 full events) and the hub ring.

### 1.3 Problems this causes

| # | Problem | Evidence | Visible as |
|---|---|---|---|
| P1 | **Sequence numbers are assigned after events can already be lost.** Created and deleted messages go through lossy `Publish`; the gRPC feed numbers what survives. `(epoch, seq)` resume looks gap-free and cannot see the loss. | `pubsub/broker.go:170-192`, `message/message.go:191,227`, `pkg/rpc/server.go:81-86`, `feed.go:45-60` | Missing rows, stuck tool spinners (TUI B16), deletions that never arrive (desktop B3 "ghosts") |
| P2 | **No revision, one-second timestamps, random tiebreak.** History sorts by `created_at` (seconds) then by random UUID. | `pkg/remote/adapter/adapter.go:228-233`, `message/message.go:210`, `db/sql/messages.sql:14` | Clients disagree on message order |
| P3 | **Deltas are full messages, sent to everyone.** SQLite rewrites the whole row every 33 ms; remote clients get the whole message every 200 ms; the desktop feed ignores `Scoped` and gets every session. | `message/message.go:21`, `adapter/events.go:44-48`, `pkg/remote/backend.go:47-53`, `feed.go:45-60` | O(n²) bytes over a long reply; N threads × 5 full copies/s to the desktop renderer |
| P4 | **Snapshots carry no rev, and paging is fake.** `ListMessages` loads and decodes every row on every page, then slices. A cursor to a deleted row fails. | `adapter.go:217-257`; `server.go:201-317` | Desktop B2 (approvals vanish after reconnect), hand-written merge rules in each client |
| P5 | **No events for board, agents, work, limits, git, team.** Clients poll. The engine already emits `TaskChangedMsg`, `ModelChangedMsg` and others, and the pump turns most into telemetry. | `crewState.ts:7-10`, `deck_workspace.go:555-580` | Stale data shown as fresh (desktop B5), stale lead after `SetModel` |
| P6 | **"Needs you" is computed three ways.** | `pkg/rpc/room.go:91-127`, `pkg/remote/daemon/daemon.go:232-262`, TUI's own | Phone and desktop disagree on attention |
| P7 | **Structured facts travel as prose**, re-parsed by regex in three clients. | `deck_workspace.go:503-517`; `pkg/remote/web/src/handoff.ts:22-30`; `internal/ui/chat/cards.go:37-70` | Cards break on wording; a reply starting `→ $x` renders as a hand-off |
| P8 | **One goroutine and one blocking channel for everything.** A slow SQLite write or slow UI back-pressures every agent's stdout. | `deck_workspace.go:478`, `session/manager.go:1677-1686` | Whole-app stalls under multi-agent load |
| P9 | **Engines die with their client.** | `internal/cmd/serve.go:325-329` | Closing the desktop stops agents; no thread can keep running |
| P10 | **One engine per project folder**, each saving its whole config. | `decks.ts:247-252,410`; `internal/workspace/peers.go:151-160` | Desktop B9: settings changed via project A revert B's |
| P11 | **Resume is a RAM ring with a random epoch.** 4,000 full-message events cycle in minutes; a daemon restart forces a full resync. | `feed.go:12, 38-42`; phone `hub.go:91-131` | Frequent full refetch; the phone re-downloads 100 messages per network flap (mobile B7) |
| P12 | **Writes are not idempotent and return nothing.** | `deck.proto:566-574` (`SendRequest` has no client id); `SetModel` etc. return `Empty` | Double posts on retry; optimistic UI cannot reconcile |
| P13 | **Two wire schemas with lossy hand converters.** | `pkg/remote/wire` (2,127 lines), `pkg/rpc/convert.go`, `pkg/remote/adapter/convert.go:198-227` | Fields lost (effort descriptions, agent status, depth) |

Every one of these is a structural cause, not a local bug. Fixing them one at a time inside today's pipeline would add a fourth store or another memo layer. v3 replaces the pipeline.

---

## 2. The v3 engine shape

### 2.1 Shape

```
┌──────────────────────── host (laptop or VPS): one `deck daemon` ───────────────────────────┐
│                                                                                            │
│  agent CLIs (started on first delivery, reaped when idle)                                  │
│     │ stdout                                                                               │
│     ▼                                                                                      │
│  adapters ──▶ thread actor (one per live thread: assigns rev, applies to in-memory state)  │
│                  ▲ Exec(Command)            │ events                                       │
│                  │                          ▼                                              │
│                  │                 writer (one per host): group commit ≤ 50 ms             │
│                  │                          │  events + materialised rows + op receipt     │
│                  │                          ▼  in ONE transaction                          │
│                  │                   deck.db (SQLite, WAL, synchronous=FULL)               │
│                  │                          │ after commit                                 │
│                  │                          ▼                                              │
│                  │                 fan-out: per-subscriber bounded queue; overflow ⇒        │
│                  │                 close stream with resume hint (never drop)              │
│                  │                     │ WatchHost (summaries)   │ WatchThread (deltas)    │
└──────────────────┼─────────────────────┼─────────────────────────┼─────────────────────────┘
                   │      local socket (gRPC)  or  Deck Link (Noise IK) — chapter on Link
             TUI ──┴── GPUI desktop ── phone ── teammate's client (same proto, role-checked)
```

Rules:
- **The daemon is the only process that opens `deck.db` and the agent CLIs.** Clients never touch the store. `store.Lock` (`store.go:330-370`) becomes a single exclusive daemon lock.
- **Threads are independent of clients.** The daemon runs as a systemd user unit (with linger) or a launchd agent; desktop and TUI start it if absent and never stop it on exit.
- **Each live thread is an actor.** It owns the thread's in-memory state, assigns revs and applies commands in order. Agent stdout pumps feed only their own thread's actor, so one slow thread cannot stall another (fixes P8).
- **Publish after commit.** A client never sees a rev that is not on disk, so a crash cannot make the daemon reuse a rev a client already holds.
- **The engine derives everything once:** attention, running state, unread, last progress line, hand-off cards, `turn_end`. Clients render; they do not infer (fixes P6, P7).

### 2.2 Store decision: SQLite per host, not JSONL

**Decision: one SQLite database per host with an append-only `events` table as the per-thread log, plus materialised tables written in the same transaction.** The JSONL log is retired after import.

Why SQLite and not "keep the JSONL log and add revs":

| Need | JSONL + meta.json + index.json | SQLite per host |
|---|---|---|
| Event + materialised state + command receipt atomically (T3 Code's pattern; it is what makes `client_op_id` retries safe) | Three files, renames, no transaction across them | One transaction |
| Paged history by rev, with stable cursors | Needs a separate index; today's window and marks code is per process and in memory (`window.go`) | `INDEX (thread_id, created_rev)` |
| "Everything running" across all projects and threads on the host | `index.json` rebuilt from meta files; no fsync (`store.go:1240`) | One query over `threads` and `attention` |
| Retention and compaction of streaming deltas | Rewrite files | `DELETE ... WHERE rev < floor` |
| One writer, many readers | Locks per file, shared/exclusive store lock | WAL: one writer, concurrent readers |
| Tooling | Custom scanners | `sqlite3`, the existing drivers in `internal/db` (modernc/ncruces) |

What we give up: logs are no longer greppable text, and one corrupted file now affects a host rather than a thread. Mitigations: `deck thread export <id>` writes the old JSONL format; nightly `VACUUM INTO` backup to `~/.deck/backup/` (keep 3); `PRAGMA integrity_check` at boot after an unclean shutdown.

Schema sketch (columns abbreviated):

```sql
CREATE TABLE threads   (id TEXT PRIMARY KEY, project_id TEXT, epoch TEXT, rev INTEGER,
                        materialised_rev INTEGER, min_resumable_rev INTEGER,
                        parent_id TEXT, lead_member TEXT, title TEXT, mode TEXT, level TEXT,
                        state TEXT, created_ms INTEGER, updated_ms INTEGER, archived INTEGER);
CREATE TABLE events    (thread_id TEXT, rev INTEGER, kind INTEGER, actor TEXT,
                        client_op_id TEXT, at_ms INTEGER, body BLOB,      -- body = deck.v2 ThreadEvent bytes
                        PRIMARY KEY (thread_id, rev)) WITHOUT ROWID;
CREATE TABLE messages  (thread_id TEXT, id TEXT, created_rev INTEGER, rev INTEGER,
                        author TEXT, member_id TEXT, spec BLOB, turn_id TEXT, state INTEGER,
                        turn_end INTEGER, created_ms INTEGER,
                        PRIMARY KEY (thread_id, id));
CREATE INDEX messages_order ON messages (thread_id, created_rev);
CREATE TABLE parts     (thread_id TEXT, message_id TEXT, idx INTEGER, kind INTEGER,
                        body BLOB, full_bytes INTEGER, PRIMARY KEY (thread_id, message_id, idx));
CREATE TABLE members   (...);  CREATE TABLE tasks (...);  CREATE TABLE approvals (...);
CREATE TABLE attention (id TEXT PRIMARY KEY, thread_id TEXT, kind INTEGER, required_role INTEGER, ...);
CREATE TABLE ops       (principal TEXT, client_op_id TEXT, thread_id TEXT, rev INTEGER,
                        result BLOB, at_ms INTEGER, PRIMARY KEY (principal, client_op_id));
CREATE TABLE host_events (host_rev INTEGER PRIMARY KEY, body BLOB);
CREATE TABLE read_marks (person TEXT, thread_id TEXT, rev INTEGER, PRIMARY KEY (person, thread_id));
-- moved from the Crush DB: usage, behavior, trace_span, limit_snapshot
```

Write path and cost:
- **Text deltas are events.** The thread actor coalesces agent token chunks per part on a 50 ms tick into one `message.append_text` event. A 60-second reply at 20 events/s is about 1,200 small rows (~100 KB). That is cheap.
- **Materialised parts trail the log.** The streaming part's materialised text is rewritten at most once a second and at part end, not per delta, so a 30 KB part does not cost 30 KB of page writes every 50 ms. `threads.materialised_rev` records how far materialisation got; on boot the daemon replays `events` after it. Snapshots of live threads come from the actor's in-memory state, which is always current.
- **Group commit.** One writer goroutine commits every 50 ms or every 256 events, whichever comes first, with `synchronous=FULL` (one WAL fsync per commit, at most 20 per second per host). Disk stalls back-pressure only the threads that are writing.
- **Retention.** `events` keeps at least 7 days and at least the last 20,000 events per thread. Below that floor, rows are deleted and `min_resumable_rev` moves up. The materialised tables keep the full history forever; `events` is the replay log, not the archive.

### 2.3 Revisions and epochs

- **`rev`**: per thread, starts at 1, +1 per durable event, gap-free from `min_resumable_rev` upward. Ephemeral events (presence, browser frames) carry `rev = 0` and are never logged.
- **Message order**: `created_rev`, the rev of the event that created the message. Immutable and unique, so every client sorts the same way (fixes P2). `messages.rev` is the rev of the last change, so a client can always tell which of two updates is newer.
- **`epoch`**: a random 128-bit id stored on the thread at creation. It changes only when the log's identity changes: import from v2 data, restore from backup, `deck thread repair`, or a move to another host (D17; the target starts a new log). **A daemon restart keeps every epoch**, because revs are durable and publish-after-commit means no client holds an uncommitted rev. A reconnect after a restart is a normal resume (fixes P11).
- **`host_rev`**: the same idea for the host summary stream, in `host_events` with a 10,000-event retention. The summary snapshot is small, so falling off the end only costs one snapshot.

### 2.4 How the old stores fold in

| Old | v3 |
|---|---|
| `~/.deck/threads/<id>.jsonl` + `.meta.json` + `index.json` | Imported into `deck.db` on first daemon start (one transaction per thread; a thread that fails import stays on disk and is listed in `deck doctor`). Each imported thread gets a fresh epoch. Old files move to `~/.deck/threads.v2/` and are deleted one release later. |
| Crush SQLite `<project>/.deck`: `sessions`, `messages` | **Not imported.** They are a re-rendering of the JSONL log. Dropped with the workspace pump. |
| Crush `files` table (file history used for TUI session diffs, `internal/ui/model/session.go:149-180`) | Replaced by the engine's change tracking (`EntrySnap` git snapshots), served as `changes.*` events and a `Changes` RPC. |
| Crush `usage`, `behavior`, `trace_span`, `limit_snapshot` | Copied into `deck.db`, keyed by thread id. |
| Crush `mcp_*`, `read_files` | Dropped; any setting still read moves to `deck.json`. |
| `deck-threads.json` | Deleted. **Session id = thread id.** The proto uses "thread" only. |
| Three model caches (`modelcache.Cache`, `Manager.modelCache`, `DeckWorkspace.models`) | One catalog per host in `deck.db`, refreshed in the background per kind. |
| rpc ring (4,000), hub rings (512/2,000), pubsub buffers | Gone. Replay comes from `events`; each subscriber has one bounded queue. |
| `deck.json` profiles and defaults | Stay as files (they are config people edit), read and written only by the daemon. Changes emit `profile.*` / `defaults.changed` host events. |

---

## 3. The v3 proto

### 3.1 Versioning

- **New package `deck.v2`** in `proto/deck/v2/`, split by service: `host.proto`, `thread.proto`, `command.proto`, `agent.proto`, `access.proto`, `browser.proto`, `workspace.proto` (files, git, terminal), `link.proto` (pairing and Link frames). The ids, the event model and the write model all change, so this is a new major version, not an edit of v1.
- **`deck.v1` is frozen** and served only for the Electron app until the GPUI app reaches parity (D2); it is deleted with `desktop-shell`.
- **Inside v2, additive only.** `buf breaking --against main` runs in CI. Optional behaviour is announced in `HelloResponse.features` (versioned strings such as `browser.v1`), replacing the per-method capability probing of `room.go:216-247`.

### 3.2 Services

```proto
syntax = "proto3";
package deck.v2;

service Host {
  rpc Hello(HelloRequest) returns (HelloResponse);           // host id, name, version, principal, role, features
  rpc WatchHost(WatchHostRequest) returns (stream HostEvents);
  rpc ListThreads(ListThreadsRequest) returns (ListThreadsResponse); // paged; archived; search
  rpc GetCatalog(GetCatalogRequest) returns (Catalog);       // cached; never blocks on a CLI
}

service Threads {
  rpc WatchThread(WatchThreadRequest) returns (stream ThreadEvents);
  rpc History(HistoryRequest) returns (HistoryPage);
  rpc GetPart(GetPartRequest) returns (Part);                // full tool output / diff beyond the inline cap
  rpc Changes(ChangesRequest) returns (ChangesResponse);
}

service Commands {
  rpc Exec(Command) returns (CommandResult);                 // every write goes here
  rpc Presence(PresenceUpdate) returns (Empty);              // ephemeral, throttled, not logged
}

service Browser {                                            // D10: Chromium runs on the host
  rpc WatchBrowser(WatchBrowserRequest) returns (stream BrowserFrame);
}

service Workspace {                                          // carried over from v1, unchanged in spirit
  rpc ListFiles(ListFilesRequest) returns (ListFilesResponse);
  rpc TerminalOpen(TerminalOpenRequest) returns (TerminalRef);
  rpc TerminalOutput(TerminalRef) returns (stream TerminalChunk);
  // git ops; git *status* arrives as events (no 15 s poll)
}

service Access {                                             // D8/D15; details in the team-access chapter
  rpc ListPeople(Empty) returns (ListPeopleResponse);
  // Invite, Grant, Revoke go through Exec so they are audited like any write.
}
```

Everything v1 had that is cut by D8 (team chat, outbox, code and ops contracts, `ReadShared`/`PostShared`, `Team`) has no v2 equivalent.

### 3.3 Host summary stream

Cheap, always on, one per host per client. No message bodies. A client connected to several hosts merges the streams by `(host_id, thread_id)` into "Running" and "Needs you" (D7, D9).

```proto
message WatchHostRequest {
  string host_epoch = 1;        // empty on first connect
  uint64 from_host_rev = 2;
}

message HostEvents {            // one stream message = one batch
  uint64 prev_host_rev = 1;     // continuity check; mismatch ⇒ resubscribe
  repeated HostEvent events = 2;
}

message HostEvent {
  uint64 host_rev = 1;
  oneof kind {
    HostSnapshot snapshot = 10;          // all summaries + attention for this principal
    ThreadSummary thread_upsert = 11;
    string thread_removed = 12;
    AttentionItem attention_upsert = 13;
    string attention_cleared = 14;
    AgentSummary agent_upsert = 15;      // persistent agents (profiles) and their current runs
    CatalogChanged catalog_changed = 16;
    LimitsChanged limits_changed = 17;   // plan windows per kind; replaces the 60 s poll
    HostStatus host_status = 18;         // cpu, mem, CLI login state per kind (D16)
    Defaults defaults_changed = 19;
  }
}

message ThreadSummary {
  string thread_id = 1;
  string project_id = 2;
  string title = 3;
  string parent_id = 4;
  AgentRef lead = 5;
  ResolvedSpec lead_spec = 6;
  ThreadState state = 7;                 // idle, running, needs_you, blocked, error, waiting_limit
  repeated RunningAgent running = 8;     // member_id, profile, kind, status, task_title, current_tool, since_ms
  string last_progress = 9;              // ≤ 140 chars, written by the engine
  int64 last_activity_ms = 10;
  uint64 thread_rev = 11;
  uint64 read_rev = 12;                  // this person's read mark ⇒ unread = thread_rev > read_rev
  uint32 viewers = 13;                   // presence count
  string runs_on = 14;                   // host display name, for the "on vps-1" chip
}

message AttentionItem {                  // engine-owned "needs you" (D4, D7)
  string id = 1;
  string thread_id = 2;
  AttentionKind kind = 3;                // approval, question, plan, blocked, error, review, limit
  string label = 4;                      // "codex wants to run rm -rf build/"
  string on_behalf_of = 5;               // "subagent of reviewer, in sub-thread 'tests'"
  Role required_role = 6;                // member for normal approvals, admin for destructive (D15)
  int64 since_ms = 7;
}
```

`AttentionItem`s are created and cleared by the engine when the underlying fact changes (an approval is answered, a task leaves `blocked`, a review is accepted). Viewing a thread does not clear attention; acting does.

### 3.4 Thread stream

Opened only for threads on screen (focused, split, or a Follow preview). Snapshot at R, then every event after R, in rev order.

```proto
message WatchThreadRequest {
  string thread_id = 1;
  string epoch = 2;              // empty ⇒ snapshot
  uint64 from_rev = 3;           // last rev the client applied
  uint32 tail = 4;               // messages in a snapshot; default 50
}

message ThreadEvents {
  uint64 prev_rev = 1;           // continuity check
  repeated ThreadEvent events = 2;
}

message ThreadEvent {
  string thread_id = 1;
  uint64 rev = 2;                // 0 for ephemeral events
  string client_op_id = 3;       // set when a command caused it
  Actor actor = 4;               // person (with device) | agent (member id) | engine
  int64 at_ms = 5;
  oneof kind {
    ThreadSnapshot snapshot = 10;
    Resumed resumed = 11;                 // "your epoch and rev are good; deltas follow"
    MessageAppendText append_text = 12;
    Message message_upsert = 13;          // created, finished, edited; full but capped (§5.3)
    MessageDelete message_delete = 14;    // edit-and-resend truncation, never silent
    ToolUpdate tool_update = 15;          // status, elapsed, output tail
    Approval approval_requested = 16;
    ApprovalResolved approval_resolved = 17;
    Task task_upsert = 18;                // full typed task: owner, delegate, terms, budget, spent, status, result
    string task_removed = 19;
    Handoff handoff = 20;                 // typed: from, to, task_id, brief, result, sub_thread_id
    TurnEnd turn_end = 21;                // done | handoff | blocked | continue, with the member
    Member member_upsert = 22;            // agent status enum, spec, current tool, depth, parent, context, cost
    ThreadMeta meta = 23;                 // title, mode, level, lead
    Queue queue = 24;
    Plan plan = 25;                       // typed plan card, not numbered-list detection
    WorkChanged work = 26;                // worktree, git status, leases
    Presence presence = 27;               // rev 0
    BrowserState browser = 28;            // tabs, url, who controls; frames go on WatchBrowser
    Notice notice = 29;
  }
}

message ThreadSnapshot {
  string epoch = 1;
  uint64 rev = 2;                         // the snapshot is exactly the state at this rev
  uint64 min_resumable_rev = 3;
  ThreadMeta meta = 4;
  repeated Member members = 5;
  repeated Task tasks = 6;
  repeated Approval approvals = 7;
  Queue queue = 8;
  repeated Message tail = 9;              // newest `tail` messages, oldest first
  string history_cursor = 10;             // to page older messages
}

message Message {
  string id = 1;
  uint64 created_rev = 2;                 // the sort key
  uint64 rev = 3;                         // last change
  Actor author = 4;
  repeated Part parts = 5;
  ResolvedSpec spec = 6;                  // model + effort used for this reply, with source (D5)
  string turn_id = 7;
  MessageState state = 8;                 // streaming, done, error, cancelled
}

message MessageAppendText {
  string message_id = 1;
  uint32 part = 2;
  PartKind kind = 3;                      // text | reasoning
  string text = 4;                        // appended to the part
}

message Part {
  uint32 index = 1;
  oneof body { Text text = 2; Reasoning reasoning = 3; ToolCall tool = 4; Image image = 5; }
  bool truncated = 6;                     // full content via GetPart
  uint32 full_bytes = 7;
}
```

Snapshot consistency: the server registers the subscriber, reads the snapshot at rev R from the actor (live thread) or from one SQLite read transaction (cold thread), then sends buffered events with `rev > R`. No client merge heuristics are needed (fixes P4 and desktop B2, B3).

### 3.5 Resume semantics

| Client sends | Server state | Server answers |
|---|---|---|
| no epoch | any | `snapshot` at current R, then deltas |
| epoch matches, `from_rev ≥ min_resumable_rev` | normal, including after a daemon restart | `resumed{rev: from_rev}`, then every event after `from_rev` |
| epoch matches, `from_rev < min_resumable_rev` | the client was away longer than retention | `snapshot` |
| epoch differs | log replaced (import, restore, move) | `snapshot`; the client drops its cached thread |
| `prev_rev` in a batch ≠ the client's last rev | should not happen; a bug | the client resubscribes from its last rev and reports a telemetry event |

A subscriber whose queue fills (1,024 batches) is disconnected with `RESOURCE_EXHAUSTED` and a `resume_from` trailer. It reconnects and resumes from the log. This replaces lossy `Publish` and the "kick" in `feed.go:55-60`: slow clients cost themselves a reconnect, never a missing row.

### 3.6 History

```proto
message HistoryRequest {
  string thread_id = 1;
  uint64 before_created_rev = 2;  // from history_cursor or the oldest message held
  uint32 limit = 3;               // default 50, max 200
}
message HistoryPage {
  repeated Message messages = 1;  // oldest first
  uint64 as_of_rev = 2;
  bool more = 3;
}
```

Served from `messages_order` by index, not by loading every row. `created_rev` never changes, so a cursor stays valid even when messages are deleted (fixes the `ErrInvalid` in `adapter.go:243-245`).

### 3.7 Commands

One RPC for every write. One envelope makes idempotency, role checks, audit and the phone's offline queue uniform.

```proto
message Command {
  string client_op_id = 1;        // ULID from the client; required
  string thread_id = 2;           // empty for host-level ops
  uint64 expected_rev = 3;        // optional; FAILED_PRECONDITION if the thread moved past it (used for edits)
  oneof op {
    // threads
    CreateThread create_thread = 10;      // project, title, lead: AgentRef, first message
    Send send = 11;                       // text, attachments; to = lead unless set; steer flag
    EditMessage edit_message = 12;
    Interrupt interrupt = 13;
    Continue continue_ = 14;
    SteerQueued steer_queued = 15;        // by queue item id; replaces the 3 s race in desktop useRoom.ts:32
    CancelQueued cancel_queued = 16;
    SetMode set_mode = 17;                // per thread (fixes TUI B12: plan/yolo are global today)
    SetLevel set_level = 18;
    RenameThread rename = 19;
    ArchiveThread archive = 20;
    DeleteThread delete = 21;
    MarkRead mark_read = 22;
    MoveThread move_thread = 23;          // D17

    // agent specs (D5)
    SetLead set_lead = 30;                // thread scope: who leads; may add a member (shown in result)
    SetMemberSpec set_member_spec = 31;   // thread scope: model/effort of one member, by member id
    SetDefaults set_defaults = 32;        // global scope; only Settings sends it; admin only
    SaveProfile save_profile = 33;        // profile scope
    DeleteProfile delete_profile = 34;

    // delegation (D6)
    Delegate delegate = 40;               // to: AgentRef (profile or member), brief, terms, spec_override, report_to
    TaskAction task_action = 41;          // accept_result, reject_result, retry, cancel, keep_entry (by task id)
    StartRace start_race = 42;

    // approvals and questions
    Answer answer = 50;                   // approval id, decision, answers; role-checked (D15)

    // browser (D10)
    BrowserInput browser_input = 60;      // take control, release, pointer, keys
    BrowserOpen browser_open = 61;

    // access (D8/D15)
    Invite invite = 70;
    Grant grant = 71;
    Revoke revoke = 72;
  }
}

message CommandResult {
  string client_op_id = 1;
  uint64 rev = 2;                         // the thread rev after the command's events
  bool duplicate = 3;                     // replayed result of an earlier identical op
  oneof result {
    ThreadMeta thread = 10;
    Message message = 11;
    LeadResult lead = 12;                 // resolved spec, member added or reused, restart_required
    ResolvedSpec spec = 13;
    Task task = 14;
    Defaults defaults = 15;
  }
}
```

Idempotency: `ops` is keyed by `(principal, client_op_id)` and written in the same transaction as the command's events. A retry returns the stored result with `duplicate = true` (fixes P12). Every event a command causes echoes its `client_op_id`, so the client can swap its optimistic row for the real one. The phone's offline queue (`pkg/remote/queue`, today's receipts in `wire/v3.go:133-200`) becomes a store of `Command` bytes replayed on reconnect; the 30-second veto window and expiry stay.

### 3.8 Agent specs and the catalog (D5)

```proto
message AgentSpec   { string kind = 1; string model = 2; string effort = 3; } // never empty on the wire
message AgentRef    { oneof ref { string profile = 1; string member_id = 2; string kind = 3; } }
message ResolvedSpec {
  AgentSpec spec = 1;
  SpecSource model_source = 2;            // thread | profile | defaults | inherited | cli_default
  SpecSource effort_source = 3;
}

message SetLead       { AgentRef agent = 1; AgentSpec spec = 2; }       // spec optional; resolved and validated
message SetMemberSpec { string member_id = 1; string model = 2; string effort = 3; }
message SetDefaults   { string kind = 1; AgentSpec spec = 2; }

message GetCatalogRequest { string kind = 1; }                           // empty = all cached kinds; never spawns all CLIs
message Catalog {
  repeated KindCatalog kinds = 1;
}
message KindCatalog {
  string kind = 1;
  repeated ModelEntry models = 2;
  int64 fetched_ms = 3;
  CatalogSource source = 4;               // cli | help | static
  bool refreshing = 5;
}
message ModelEntry {
  string id = 1; string label = 2; string description = 3;
  bool is_default = 4;                    // "CLI default" is an explicit entry, not ""
  repeated EffortLevel efforts = 5;       // level + description; SetMemberSpec rejects others
  string default_effort = 6;
  uint32 context_window = 7;
}
```

`GetCatalog` answers from the host cache immediately and refreshes in the background; the refresh ends in a `catalog_changed` host event. Picking a model never writes defaults: only `SetDefaults` does, and only Settings sends it. The agent chapter owns the semantics; this chapter owns the shape.

### 3.9 Roles and presence (D8, D15)

- Every call is made as a **principal**: a person id (identity key) plus a device id, established by the transport (§3.11). `HelloResponse` returns the person's role on this host: `viewer`, `member` or `admin`.
- The daemon checks each `Command` against the role: viewers send nothing; members post, answer normal approvals and run agent turns capped at ask mode; destructive approvals, `SetDefaults` and access ops need admin. A refusal is `PERMISSION_DENIED` with the required role.
- `Actor` on every event names who did it, so a teammate's message shows as theirs, not as prefixed text (today's `[vps] ...`).
- `Presence{thread_id, person, device_kind, state: viewing | typing | idle}` is sent by clients at most once per second per thread, fanned out as `rev = 0` events, and expires after 60 s without refresh. Never logged.

### 3.10 Browser frames (D10)

```proto
message WatchBrowserRequest { string thread_id = 1; string tab_id = 2; BrowserQuality quality = 3; } // full | preview
message BrowserFrame {
  string tab_id = 1;
  uint64 frame_seq = 2;
  ImageFormat format = 3;                 // webp | jpeg
  uint32 width = 4; uint32 height = 5;
  bytes data = 6;
  Outline outline = 7;                    // set when the accessibility outline changed
}
```

Chromium runs headless on the host and streams CDP screencast frames only while at least one client watches. Frames are ephemeral and never logged; tab list, URL, origin approvals and "who has control" are durable `BrowserState` thread events. The Electron browser controller RPCs (`RegisterBrowserController`, `BrowserResult`, `BrowserUpdate`) are not carried over.

### 3.11 Transports: one schema, gRPC on local sockets, Link frames inside Noise (D13, D14)

The connection system itself (paths, pairing, the link agent, the relay) is the fleet chapter (`05-fleet-and-connect.md`). This section fixes what travels over it.

A client tries a host's paths in this order and keeps the first that works: **local socket → tailnet → direct (LAN/public) → Noise relay → SSH stdio tunnel** (`ssh <alias> deck link --stdio`). Trust always comes from Deck's pinned keys and roles, never from the network path.

```
 GPUI (tonic) ─┐ gRPC/HTTP2 over link.sock         ┌─ local host: gRPC/HTTP2 over host.sock (no Noise)
 TUI (grpc-go) ┴─▶ link agent (Go, in `deck`) ─────┤
                   generic gRPC proxy:             └─ remote host: Noise IK over tailnet | direct | relay | SSH stdio
                   call ⇄ Link frames                    └─ Link frames ⇄ the same deck.v2 service implementation
 phone (Connect-ES transport) ── Link frames in Noise IK over relay | direct | tailnet ──▶ same hosts
```

| Hop | Encryption and auth | Framing |
|---|---|---|
| GPUI / TUI → link agent (`~/.deck/run/link.sock`, 0600 in 0700; named pipe on Windows) | none; same OS user | gRPC over HTTP/2; the target host is chosen by a `deck-host` call header |
| Link agent → this machine's daemon (`~/.deck/run/host.sock`) | none; peer credentials ⇒ owner, admin | gRPC over HTTP/2 (the agent passes calls through untouched) |
| Link agent or phone → remote host, on any of tailnet, direct, relay, SSH stdio | **Noise IK** (the pkg/remote v3 stack: pinned host static, per-device static) | **Link frames**, one per Noise transport message, chunked as in `docs/mobile-contract.md` §3 |

Decisions and reasons:
- **Inside Noise there is exactly one framing: Link frames.** A Link frame is a protobuf `LinkFrame{call_id, method, kind (headers | message | end | cancel | credit), status, bytes payload}`; `payload` is the serialized deck.v2 request or response message. It follows Connect protocol semantics: method paths like `/deck.v2.Threads/WatchThread`, unary and server-streaming calls, end of stream with a status code, cancellation, deadlines in the headers frame. Per-call `credit` frames give flow control, so one busy `WatchThread` cannot starve an `Exec` on the same session. One framing on every remote path means one set of fuzz and hostile-input tests, and it fits the relay's message limits (frame ≤ 1 MiB, 65,518-byte chunks, `docs/mobile-contract.md:53-69`).
- **gRPC over HTTP/2 only on local sockets**, where tonic and grpc-go give GPUI and the TUI streaming, cancellation and deadlines for free and no Noise is needed (the OS user is the owner).
- **The link agent bridges the two generically.** It is a grpc-go proxy with a raw-bytes codec (`UnknownServiceHandler`): it maps a gRPC call to a Link call with the same method path and the same payload bytes, without decoding them. So a new RPC needs no agent change, and GPUI never implements Noise, path racing or tsnet (all Go, written once).
- **The phone speaks Link frames directly.** React Native has no HTTP/2 client we can run over our own encrypted channel. It uses protobuf-es generated clients with a custom Connect-ES `Transport` over Link frames inside its existing TypeScript Noise client.
- **The daemon serves both framings from one implementation.** A small generated dispatch table (`protoc-gen-decklink`) routes incoming Link calls to the same Go service handlers that serve gRPC on `host.sock`. The principal comes from the Noise static (device ⇒ person ⇒ role) for Link calls and from peer credentials for the socket.
- Running Noise even over the tailnet keeps one trust model (D13); the double encryption costs little.

So there is one schema (`deck.v2`), one service implementation, one trust model and one remote framing.

What gets deleted from pkg/remote: `wire` (the JSON v2/v3 schema, 2,127 lines plus fixtures), `adapter` (1,604), `hub` (290), `server` (2,738), the inbox in `daemon`, and `pkg/rpc/convert.go` and `feed.go`. What stays and is reused: `identity`, `passkey`, `push`, `relay`, `relayclient`, `queue` (now a command outbox), `devstore`, `auth`, and the Noise code. Pairing bodies that lived in `wire` move into `link.proto`.

---

## 4. The TUI on the protocol

### 4.1 How it connects

- `deck` (the TUI) dials the link agent on `~/.deck/run/link.sock`; for this machine the agent passes calls straight to the daemon on `~/.deck/run/host.sock`. If no daemon answers, the TUI starts `deck daemon --detach` (the same binary; the link agent runs inside it) and waits up to 2 s for the socket. Quitting the TUI leaves the daemon and its threads running.
- `deck --no-daemon` runs an in-process server reached through `bufconn`, for CI containers and one-shot use. The TUI still talks to it through the generated gRPC client, so there is no second code path. Tests use the same mode.
- The TUI opens a remote host exactly as the desktop does, by naming it in the `deck-host` header; the link agent does the Noise and path work. A TUI on a laptop can watch the VPS's threads.

### 4.2 Structure

```
internal/client      shared Go client: dial + path order, WatchHost/WatchThread with resume,
                     Exec with client_op_id and retry, per-thread reducer (pure Apply(event))
internal/ui          renders view-models from internal/client; never imports internal/deck/session
                     or internal/workspace; never blocks Update or View on IO
```

The 37 `m.com.Workspace.(workspace.X)` type assertions in `internal/ui` (AgentPicker, Roster, Orchestration, UsageReader, SettingsEditor, Contracts, Peers, ...) are replaced by the client. The Crush services the TUI rides on (`internal/message`, `internal/permission`, `internal/question`, `internal/app` event fan-in, `internal/pubsub`) are deleted from the TUI's path.

### 4.3 `[IN-PROC]` bugs this removes

From `research-tui.md` §0.1 and §2.3:

| Bug | Today | Why the protocol removes it |
|---|---|---|
| B5 Approval modal from any thread, no thread named | `ui.go:1140-1150`, `5434-5447` | Approvals arrive on `WatchThread` for open threads and as `AttentionItem`s (with thread, label, on-behalf-of) on `WatchHost` for the rest. The TUI shows them in Needs you, not as a modal over another thread. |
| B6 Global single-slot approval and question queues; 10 ms busy-poll | `permission.go:207-208`, `question.go:246-271`, `workspace/questions.go:196-226` | Approvals and questions are engine records keyed by id; many can be open at once; answers are `Exec(Answer)`. The Crush permission and question services are gone. |
| B7 A resolved question dismisses the wrong form | `ui.go:5475-5487` | `approval_resolved` names the approval id; the form for that id closes, nothing else. |
| B8 Hand-offs as regex-parsed text | `deck_workspace.go:503-517`, `chat/cards.go:37-70` | `Handoff` is a typed event. |
| B12 Plan and yolo global to the process; sidebar toggle writes config in `Update` | `deck_workspace.go:401-420, 537-544`; `ui.go:3872-3878` | `SetMode` is per thread. UI layout is client-local state, not daemon config. |
| B13 Crush model restore writes global config on every load; no-op Thinking toggle | `ui.go:1007, 2634-2690, 3855-3869` | No Crush model logic in the path; the lead and spec come from `ThreadMeta` and `Member`. |
| B14 IO on the Update loop; roster read inside View; `busyAgents()` twice per message | `dialog/sessions.go:74-78`, `frame_memo.go:18-41`, `ui.go:831-852` | The view-model is local and already current; the session switcher reads `ThreadSummary`s it holds. |
| B15 Every thread's stream forces full redraws | `message.go:21`, `ui.go:1091-1102`, `framecache.go:240-255` | The TUI subscribes only to visible threads; other threads change a summary row, which redraws one line. |
| B16 Lossy delivery ⇒ stuck spinners, missing rows | `app.go:182-296`, `pubsub/broker.go:170-190`, `ui.go:1963-1971` | Lossless stream with revs; gaps are impossible or detected. |
| B17 Lead polled every 2 s | `model/lead.go:23-26` | `ThreadMeta.lead` arrives as an event. |
| §2.3 O(sessions) synchronous engine calls when opening the switcher | `dialog/sessions_item.go:333-371` | Summaries are local. |

Bugs the protocol does **not** fix by itself, and which the TUI chapter must still do: B1–B3 (render cost; §5.5), B4 (served by `Changes` instead), B9 (`turn_end` is now typed but still has to be drawn), B10, B11 (rows must be keyed by task id), B18, B19, B20.

---

## 5. RAM and speed plan

Owners are team areas: **Engine** (daemon, store, agents), **Link** (transports, wire), **Desktop** (GPUI), **TUI**, **Phone**.

### 5.1 Engine

| Change | Today | v3 rule | Owner |
|---|---|---|---|
| Lazy member start | Opening a thread starts every non-ephemeral member's CLI (`session/manager.go:1486-1499`) | A member's CLI starts on its first delivery. Opening a thread starts nothing. | Engine |
| Reap idle CLIs | No reaper; idle members keep 150–400 MB each (`research-engine.md` §3.1) | Stop a CLI after 10 min idle (`agents.idle_reap`, default `10m`) and resume it with the CLI's own resume id on the next delivery. Never reap a member with a pending approval, a running tool, an open terminal, or a turn in progress. `Member.status = parked` shows it. | Engine |
| One codex app-server per host | One `codex app-server` per member (`agent/codex/run.go:15`) | One app-server per host and codex account, multiplexing threads, if the app-server's thread API supports per-thread cwd and approvals. **Spike first**; if not, one per thread, still reaped. | Engine |
| Replace `mcp-approve` children | One full Deck process per member (`approve/broker.go:147-155`) | The daemon serves MCP itself. CLIs that accept an HTTP MCP server get a localhost URL with a per-agent token. CLIs that need stdio get `deck-mcp-shim`, a separate tiny binary (stdio ↔ daemon socket, no Deck imports, target ≤ 5 MB RSS). | Engine |
| Bound in-memory transcripts | 8 MB log window per live thread (`window.go:26-35`), plus copies in workspace, message service, pubsub and two rings | One in-memory copy per live thread: the actor's state with the newest 200 messages or 2 MB, whichever is smaller. Older messages are read from `deck.db` on demand. A thread with no subscriber and no running agent for 5 min is unloaded. | Engine |
| Catalog | Asks every installed CLI per picker open, serially (`agent_picker.go:73-100`) | Host cache in `deck.db`; background refresh per kind, at most once an hour or on demand; `GetCatalog` never blocks. | Engine |
| Browser | Electron `WebContentsView`s per client | One headless Chromium per host, started on first browser use, stopped after 5 min with no tab activity; screencast only while watched. | Engine |
| Git and work state | Polled by clients (`Git.tsx:39` 15 s, `crewState.ts` 3 s/10 s) | fsnotify on worktrees with an active agent or a subscriber, debounced 500 ms, emitted as `work` events. Idle threads cost nothing. | Engine |

### 5.2 Clients

Rules for every client (Desktop, TUI, Phone own their implementations):
1. **Virtualised timelines.** Only visible rows plus a small overscan are laid out and rendered. Unmeasured rows use a height estimate (from text length and width) until they scroll into view. Scroll anchoring holds position when content above changes.
2. **Text deltas.** `append_text` appends to one part; only that row is re-laid out. Markdown keeps a stable-prefix cache so a delta re-parses only the open block (the TUI's `chat/streaming_markdown.go` does this today; keep it).
3. **Hold a window, not the transcript.** Keep the snapshot tail plus pages the user scrolled to, capped at about 500 messages per thread; evict pages far from the viewport and refetch via `History`. Tool output and diffs beyond the inline cap load on expand via `GetPart`.
4. **Subscribe only to visible threads.** `WatchThread` for the focused thread, split panes and Follow previews. A thread that leaves the screen keeps its stream for 30 s (fast switching back), then closes; its cache stays and resumes by rev.
5. **No polling.** Board, agents, work, limits, git, catalog and presence are events. Clocks ("3 m ago") tick once a minute and repaint only the affected text.
6. **Per-thread state, no global recompute.** A delta for thread A must not recompute status for every thread (today's `withRooms` on every token, `appState.ts:79-111`). The engine already computed status; the client stores it.

### 5.3 Wire

**Batching.** The thread actor coalesces deltas per part on a 50 ms tick. The fan-out sends one `ThreadEvents` batch per subscriber per tick. The phone's subscriber tick is 150 ms on a metered network (client hint in `WatchThreadRequest`).

**Compression.** Snapshots, history pages and catalog responses over 4 KB are gzip-compressed per message (the gRPC `grpc-encoding` on local sockets; a `compressed` flag on the Link frame remotely; grpc-go, tonic and fflate on the phone all handle gzip). Delta batches are not compressed: they are small, and compressing short attacker-influenced text next to secrets inside one compression context is not worth the risk.

**Payload budgets per event type** (enforced by encoders, checked in the replay harness):

| Event | Budget | Notes |
|---|---|---|
| `append_text` | ≤ 48 B overhead + text; ≤ 16 KB text per event | Larger appends split |
| `message_upsert` | ≤ 16 KB typical; text parts uncapped only at finish | Tool output inline ≤ 8 KB, diff inline ≤ 64 KB, rest via `GetPart` (same caps as `wire.go:56-60` today) |
| `tool_update` | ≤ 512 B + output tail ≤ 2 KB | At most 4 per second per tool |
| `approval_requested` | ≤ 4 KB | Command preview and diff summary; full diff via `GetPart` |
| `task_upsert`, `handoff`, `plan` | ≤ 4 KB | Results longer than that link to a message |
| `member_upsert` | ≤ 512 B | Status changes only; context meter at most 1/s |
| `presence` | ≤ 128 B | ≤ 1/s per person per thread |
| `ThreadSummary` | ≤ 400 B | `last_progress` ≤ 140 chars |
| `HostSnapshot`, 200 threads | ≤ 80 KB raw, ≤ 20 KB gzip | Replaces broadcasting the whole inbox on every busy toggle (`daemon.go:155-183`) |
| `ThreadSnapshot`, tail 50 | ≤ 256 KB raw | Compressed |
| `BrowserFrame` | ≤ 120 KB; ≤ 5 fps full, ≤ 1 fps preview | Never logged; none without a watcher |

### 5.4 Phone

- **Close streams when backgrounded.** On `AppState` → background, release presence, close `WatchThread` and `WatchHost` after a 5 s grace, and stop the Noise session. Today the socket stays open and receives full messages 5×/s in a pocket (`apps/mobile/App.tsx:592-598`, mobile B7).
- **Push instead.** The daemon sends end-to-end encrypted push (FCM/APNs, payloads with no content, as the current Web Push) for `AttentionItem`s of kind approval, question, blocked, error and review, when no foreground device of that person is subscribed to the host. Push carries the attention id so the app can open the item directly.
- **Foreground.** Paint the cached inbox at once, resume `WatchHost` from `host_rev`, then resume `WatchThread` only for the open thread from its rev. No transcript refetch on a network flap.
- **Offline writes.** Commands queue with their `client_op_id` and replay on reconnect; duplicates are harmless.

### 5.5 TUI: the 1.08 s / 266 MB first open

Today the first `Chat.Draw` calls `List.TotalHeight()`, which renders every message through glamour on the UI goroutine (`model/chat.go:261`, `list/list.go:189-210`). `BenchmarkSessionLoad` (1,000 messages, 140×40): 1.08 s/op, 266 MB allocated, 24.3 M allocs; 87% in `TotalHeight → AssistantMessageItem.Render`, 60% of that in thinking traces.

Fix (lazy measurement):
1. `TotalHeight` sums **estimated** heights for unrendered items (line count from text length and width; a constant for collapsed folds). Exact heights replace estimates as items render. The scrollbar uses the sum; it may shift slightly as estimates resolve, which is acceptable.
2. Render only the visible window plus one screen of overscan. `Prewarm` is removed.
3. Thinking traces and tool output render collapsed and are not rendered at all until expanded.
4. The per-item render cache becomes an LRU bounded at 8 MB per thread; finished items far from the viewport are evicted (fixes B3's unbounded cache).
5. On resize, drop cached renders lazily: visible items re-render at once, the rest when they scroll in (fixes B2's full re-render).
6. The snapshot brings 50 messages; older pages load as the user scrolls up, so a 1,000-message thread is never loaded at once (fixes B3's full load).

### 5.6 Budgets

"Today" values are measured unless marked *est.* (estimates from the research, not measured). CI runs the perf jobs on a fixed Linux runner type; a job fails if a metric is over budget or more than 10% worse than `main`'s last run.

| Metric | Today | v3 budget | Owner | How CI measures |
|---|---|---|---|---|
| Daemon cold start → first `WatchHost` snapshot (1,000 threads, no agents) | n/a; `deck --help` 0.07 s / 38.6 MB RSS | ≤ 300 ms | Engine | `perf/daemon-boot`: start on a fixture `deck.db`, time to first snapshot over the socket |
| Daemon idle RSS, 1,000 threads, no live agents | one `deck serve` per project, *est.* 30–80 MB each | ≤ 80 MB | Engine | `/proc/<pid>/status` VmRSS after 60 s idle |
| Daemon RSS, 10 live threads replaying | n/a | ≤ 150 MB (≤ 4 MB per live thread) | Engine | replay harness, VmHWM |
| Extra processes per member | 1 CLI + 1 full Deck `mcp-approve` | 1 CLI (+ ≤ 5 MB shim only where needed); 0 after 10 min idle | Engine | process-tree count with fake CLIs in the replay harness |
| GPUI cold start → inbox painted (daemon running) | Electron, not measured | ≤ 500 ms | Desktop | headless GPUI test binary on Xvfb with software Vulkan (lavapipe); time to first frame with data. **To verify**: GPUI under lavapipe in CI. |
| GPUI idle RSS, 5 threads open, one with 1,000 messages visible | Electron *est.* 350–600 MB | ≤ 200 MB | Desktop | same harness, VmRSS after 60 s idle |
| Open a 1,000-message thread → first frame | TUI 1.08 s, 266 MB alloc; desktop loads every row (`adapter.go:217-257`) | TUI ≤ 50 ms and ≤ 20 MB alloc; GPUI ≤ 100 ms | TUI, Desktop | `BenchmarkSessionLoad` (existing); GPUI criterion bench on a recorded thread |
| TUI cold start → first frame | 3–6 s when the terminal does not answer DA1/OSC 11 (B20) | ≤ 200 ms; terminal query timeout ≤ 150 ms | TUI | pty test with a terminal that answers nothing |
| TUI idle RSS (client only, daemon separate) | 70–73 MB (with the engine in-process) | ≤ 45 MB | TUI | pty RSS sampler after 60 s idle |
| TUI streaming frame | 1.2–1.4 ms, 230–280 KB alloc per frame | ≤ 1 ms, ≤ 64 KB alloc | TUI | `BenchmarkDrawStreaming` (existing) |
| Bytes per streamed token (wire) | full message every 200 ms: O(n²) over a reply | ≤ 1.3 × the text's UTF-8 bytes + 48 B per batch | Link | byte-counting `net.Conn` wrapper in the replay harness, gRPC and Link framings |
| Token → screen latency, p95 | 33 ms debounce locally; +200 ms coalescing remotely | ≤ 100 ms local; ≤ 300 ms phone via relay | Engine, Link | replay with timestamps at agent stdout and client reducer |
| Reconnect after 30 s offline, one thread streaming | phone: refetch 100 messages + roster + mode + changes (mobile B7); desktop: ring or resync | 1 round trip after the handshake; bytes ≈ missed events only; 0 snapshots | Link | fault-injection test: cut the connection at random points, count bytes and snapshot requests |
| Reconnect after a daemon restart | full resync for every client (random epoch, `feed.go:38-42`) | same as above: resume, 0 snapshots | Engine | restart the daemon mid-replay |
| Host summary, steady state, 5 threads running | full inbox broadcast per busy toggle, fetched twice (`state3.ts:316-317`) | ≤ 1 KB/s | Engine | replay harness byte counter on `WatchHost` |
| Phone bytes in background | full messages 5×/s until the OS kills the socket | 0 B after 5 s | Phone | nightly emulator run (Maestro) with a proxy counter; unit test on the app-state handler in CI |
| Client polling timers | desktop: 3 s/10 s/15 s/30 s/60 s; TUI: 500 ms, 2 s, 5 s, 20 s, 30 s | none (only a 1-minute clock tick) | all | lint rule: `setInterval`, `tea.Tick` > 1 Hz and Rust interval timers need an allow-list entry |

External numbers for comparison (Zed, Tauri vs Electron) come from blog posts and search snippets with no published method (`research-external.md` §3.1–3.2, §8). They are not used as budgets; Deck's own measurements are.

---

## 6. Testing

### 6.1 Recorded-session replay harness (shared by all clients)

- **Record.** `DECK_RECORD=<dir>` makes the daemon write, per thread, the raw adapter input from each agent CLI (with timing) and every `Command` it received. Recordings are scrubbed (paths, tokens, user names) by a scrubber with its own tests, then committed under `testdata/replay/`. Start with 20 sessions: single agent, long reply, many tools, subagents, sub-thread report, race, approval storm, edit-and-resend, plan-limit wait, browser use, two people in one thread.
- **Replay engine.** A `replay` agent kind feeds recorded CLI output into the real engine, so the real actors, writer, store and fan-out run with no CLIs. A fake clock and an injected writer tick make runs deterministic.
- **Golden outputs.** Each replay produces (a) the normalised `ThreadEvent` stream and (b) the final state as a canonical `ThreadState` proto (messages, members, tasks, approvals, attention), stored as golden files.
- **Every client consumes the same corpus.** The Go reducer (`internal/client`, used by the TUI), the Rust reducer (GPUI's client crate, headless) and the TS reducer (phone, under Node with vitest) each apply the golden event stream and serialise their model to the canonical `ThreadState`. All three must equal the engine's. This is how "one protocol" stays true across three languages.
- **Visual checks** run on top of the same fixtures: TUI golden frames at fixed sizes, GPUI screenshots, phone snapshots. A screenshot diff is reviewed, not auto-accepted.

### 6.2 Deterministic ordering tests (engine)

- Seeded schedulers interleave agent output from several members and threads, concurrent `Exec` calls from three clients, and slow subscribers. Each seed is reproducible (`DECK_SEED`); CI runs 200 seeds per change and 10,000 nightly.
- Invariants checked after every step: revs strictly increase by 1 per thread; every message's `created_rev` is unique; no client ever receives `rev ≤` its last; `client_op_id` duplicates produce one effect and identical results; a subscriber that overflows is disconnected, never fed a gap.
- Crash tests: kill the daemon at random points (including mid-commit), reopen the store, and check that every rev a client saw still exists with the same content, and that resume needs no snapshot.

### 6.3 Property tests: snapshot + delta = state

Using `pgregory.net/rapid` in Go (and proptest / fast-check for the Rust and TS reducers):
- For a random command and agent-event sequence of length N and any R ≤ N: `apply(snapshot(R), events(R+1..N)) == snapshot(N)`.
- Resume from a random rev after random disconnects equals an uninterrupted stream.
- Retention: after compaction moves `min_resumable_rev`, resume above the floor is unchanged and below it yields a snapshot equal to the true state.
- History: concatenating all `History` pages, in any page size, equals the full ordered message list at `as_of_rev`, even with deletions in between.
- Materialisation: after replaying `events` from `materialised_rev`, the SQLite tables equal the actor's in-memory state.

---

## 7. Migration, in order

Each step ships on its own and leaves every client working.

| # | Step | Ships | Removes |
|---|---|---|---|
| 1 | **Baseline.** Land the perf jobs, the RSS sampler, the byte counter and the record/replay harness against today's code. Record the fixture corpus. | CI only | Guesswork; gives every later step a number to beat |
| 2 | **Engine fixes in place.** Lazy member start, idle reaper, `deck-mcp-shim`/HTTP MCP, per-kind non-blocking catalog cache, TUI lazy measurement (§5.5), terminal query timeout. | A 2.x release | Most of the agent RAM; the TUI first-open freeze; B20 |
| 3 | **Host store, dual write.** Create `deck.db`; the Manager writes events and materialised rows there as well as the JSONL log; importer for existing threads; `deck thread export`. Reads unchanged. | Invisible to users; `deck doctor` reports import status | Nothing yet; proves the store under real load |
| 4 | **`deck daemon` + `deck.v2` on the local socket.** One daemon per host owns the Manager and `deck.db`, runs as a user service, and serves v2. Legacy surfaces move inside it: the per-project Crush workspace is fed from v2 streams instead of `Manager.out`, and still serves deck.v1 to Electron and wire v3 to the phone. Electron connects to the daemon's socket instead of spawning `deck serve --rpc` per folder (a small change in `decks.ts`). | One engine per host; threads outlive clients | P8, P9, P10 (desktop B9 config clobber) |
| 5 | **TUI on v2.** `internal/client` + reducer; the TUI spawns the daemon; Needs you and Running from `WatchHost`. | New TUI | All `[IN-PROC]` bugs in §4.3; 37 workspace type assertions |
| 6 | **GPUI desktop on v2** (alpha to parity, per the desktop chapter). | New desktop app alongside Electron | Nothing yet on the engine side |
| 7 | **Deck Link carries v2.** Link frames inside Noise on every remote path, the link agent's gRPC bridge for desktop and TUI, path order per D14; the phone moves to v2 with push and background close. | Remote hosts, phone v3 | P11, P13 on the phone path; mobile B7 |
| 8 | **Roles and presence on.** Until now only the owner (admin) exists; turn on viewer/member, presence and actor attribution (team-access chapter). | Direct access for teammates | The peer link's teammate path |
| 9 | **Delete the old path.** Once GPUI is at parity: the Crush message store and `deck-threads.json`, the workspace pump, `pkg/remote/{wire,adapter,hub,server}`, the daemon inbox, `pkg/rpc` (deck.v1, `convert.go`, `feed.go`), `proto/deck/v1`, JSONL writes (the importer stays one more release), `desktop-shell`. | Smaller binary, one schema | P1–P7, P12, the three-store split |

Step 4 is the riskiest: it moves every client onto one engine at once. It is gated behind `deck daemon` being opt-in for one release (`DECK_DAEMON=1`), with the replay harness and the crash tests from §6 green before it becomes the default.
