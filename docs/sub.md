# SUB: the Source Update Bridge

SUB moves code changes between Deck's agents as references to immutable
change sets (capsules) instead of descriptions of edits. It is not a version
control system: there is no history beyond what tasks and checkpoints refer
to, and nothing needs git to work.

Code: `internal/deck/sub` (the engine), `internal/deck/session/subops.go`
(agents, tasks, handoffs), `internal/deck/peer/sub.go` (between Decks).

## Objects

Everything lives in one store per Deck (`<thread store>/sub/objects`), named
by the blake3 hash of its bytes, so equal content is stored once.

| Object        | Holds                                                                  |
|---------------|------------------------------------------------------------------------|
| chunk         | up to 64 KiB of a file, cut by content (FastCDC, ~16 KiB average)       |
| file manifest | a file's chunk ids, one per line; its id is the file's id               |
| snapshot      | `{path: {file, exec}}` for every file of a work tree that git does not ignore |
| capsule       | base snapshot, parent capsule, who/what task, and per-path `before`/`after` file ids |

Content-defined chunks mean an edit in the middle of a large file changes
one or two chunks; everything else is already stored (and, between Decks,
already sent).

A snapshot reads only files whose size or modification time changed since
the last one of that tree (a per-tree stat cache; files written in the last
two seconds are always read). Deck's checkpoints, taken before each user
message is delivered, are SUB snapshots; this is what made them cheap (#20).

## Capsules

`Capture(root, base, parent)` diffs the tree against snapshot `base`. More
than 200 files become a chain, each capsule naming the one before as parent.
`parent` also chains a capsule onto one its base already contains: a reviewer
that applied agent A's capsule and fixed it captures a capsule whose chain
holds both, so applying it to an untouched tree brings A's work and the fix.

Applying is all or nothing. Each path's change is checked against the file
now: at any version the chain passes through it fits (and is brought to the
last); at the last it is already applied; anything else is a conflict and
nothing is written. Writes are staged to temporary files and renamed in, with
the old files moved aside and restored on any failure. Revert is the same
with each path's versions reversed.

When files changed on both sides, `sub.merge.preview` classifies each file
(fits, applied, merge, conflict) and `sub.merge.resolve` applies with a
line-level three-way merge; conflicts are refused, or settled with strategy
`theirs` or `ours`. Binary files and add/add or delete/edit never merge.

## In Deck

Agents use the ops through `execute`:
`sub.snapshot.create|inspect`, `sub.patch.capture|inspect|verify|apply|revert`,
`sub.merge.preview|resolve`, `sub.capsule.list|publish|fetch`.

- `sub.patch.apply`, `sub.patch.revert` and `sub.merge.resolve` are asked
  like a file edit: refused in plan mode, approved in yolo, and put to the
  user in ask mode (after the capsule is checked, so nobody is asked about
  one that would conflict). An unanswered request is withdrawn before the
  op's deadline and writes nothing.
- Each agent's root is its own worktree when it has one, else the
  repository root of the thread's folder; capsule paths are
  repository-relative, so they mean the same in every worktree.
- A capture diffs from, in order: the agent's last `sub.snapshot.create` or
  apply, its worktree as it was made, or the checkpoint before the user's
  last message.
- A capture is recorded on the agent's running task (or `task`), and
  `task.done {capsule}` records one too; `task.list` shows them.
- `thread.send {capsule}` checks the capsule, records it on the handed task,
  applies it in every recipient that has its own worktree, and puts a line
  under the message saying what the capsule is and how it went.
- A subagent spawned with its own worktree for a task that has capsules gets
  them applied there before it starts, and its captures chain onto them.

## Worktrees and named capsules

Big, long-running work runs on worktrees Deck tracks as lines of work
(`internal/deck/session/work.go`): an isolated thread's, a subagent's or a
race entry's, a long task's under orchestration (a budget of 30 minutes or
more, or parts of its own), or one made with `worktree.create` or from the
desktop's Work tab. Each keeps its owner, task, the commit it forked from
and the branch it keeps up with, and is stored with the thread, so it
survives a restart.

- `worktree.sync` commits the worktree's work and merges its base in (or
  rebases); on conflicts it aborts, names the files and changes nothing.
- `worktree.merge` lands it in the shared tree (or another worktree) as
  uncommitted edits: its changes since the fork, commits and edits alike,
  become a version of the capsule named after it, merged three-way;
  `check` reports each file first. Landed and unchanged since, with nobody
  working in it, a worktree is pruned; work never landed is never removed
  but by `worktree.discard`.
- `agent.spawn {worktree: true}` gives a helper its own; `{worktree: name}`
  starts one in a worktree of the thread's.

A named capsule (`capsulework.go`) is a unit of change kept in versions:
`capsule.create` makes one from a worktree, a range of commits or the
uncommitted diff, reading files straight from git's objects. Its base is a
snapshot of just the files it touches. `capsule.land` and `capsule.revert`
apply and undo it, `capsule.rebase` redoes it on the shared tree, a
worktree or another capsule (stacked: it then chains onto it), and
`capsule.review` keeps comments and test status. The desktop lists its
files a page at a time and fetches each file's diff when it opens.

## Between Decks

`sub.capsule.publish` is the consent: a paired peer can then run
`sub.closure {id}` (the object ids the capsule needs) and `sub.objects {ids}`
(a zstd pack of them, at most 512 KiB of objects a reply). Only objects in
the closure of a published capsule are served, whatever ids are asked for.
`sub.capsule.fetch {peer, id}` asks for the closure, keeps what the local
store lacks, and pulls just that; every object is stored under its own hash,
so a peer cannot place bytes under an id they do not hash to.

## Garbage collection

Once a day, in the background, objects that no thread's checkpoints or
reverts, task capsules, named capsules, worktree bases or published capsules reach, and that
are older than two weeks, are deleted.
