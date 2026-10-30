import type { AgentLine } from "@web/protocol";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { refreshWork, useRoomWork } from "../workState";
import {
  type Capsule,
  type CapsuleFile,
  capsuleOfTree,
  FILE_PAGE,
  landCounts,
  type LandFile,
  type LandState,
  latest,
  shortRef,
  splitPath,
  TREE_STATE_LABEL,
  treeState,
  type TreeState,
  type WorkAction,
  type Worktree,
} from "../worktrees";
import { DiffView } from "./DiffView";
import {
  ArrowLeft,
  Check,
  ChevronDown,
  ChevronRight,
  CircleAlert,
  FileText,
  GitBranch,
  GitCommitHorizontal,
  GitMerge,
  Loader2,
  Package,
  Play,
  Plus,
  RefreshCw,
  Send,
  Trash2,
  Undo2,
  X,
} from "./icons";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { CARD } from "./kit/card";
import { Confirm, Dialog, fieldClass } from "./kit/dialog";
import { EmptyState } from "./kit/empty";

// The Work tab of a room: its worktrees (the branches its agents, or the
// room itself, work on apart from the shared tree) and its named capsules
// (big changes as versioned units). A worktree shows its owner, base,
// commits ahead and behind, files and state; it syncs with its base, lands
// in the shared tree after a check of every file, starts an agent, or goes.
// A capsule opens to its versions, commits, tests, review and its files a
// page at a time, each file's diff loaded when it opens.


const TREE_TONE: Record<TreeState, "muted" | "warning" | "success" | "error" | "outline"> = {
  gone: "error",
  working: "outline",
  landed: "success",
  behind: "warning",
  changed: "muted",
  clean: "muted",
};

const LAND_TONE: Record<LandState, "muted" | "warning" | "success" | "error"> = {
  fits: "success",
  applied: "muted",
  merge: "warning",
  conflict: "error",
};

const LAND_WORD: Record<LandState, string> = {
  fits: "applies",
  applied: "already there",
  merge: "merges",
  conflict: "conflict",
};

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

function Section({ title, count, action, children }: { title: string; count: number; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2" aria-label={title}>
      <div className="flex min-h-6 items-center gap-2 px-1">
        <h3 className="font-medium text-muted-foreground text-xs">{title}</h3>
        <span className="text-2xs text-faint tabular-nums">{count}</span>
        <div className="ml-auto flex items-center gap-1">{action}</div>
      </div>
      {children}
    </section>
  );
}

// Ahead and behind its base, as git prints them.
function Divergence({ w }: { w: Worktree }) {
  return (
    <span className="tree-divergence font-mono text-2xs text-muted-foreground tabular-nums" title={`${w.ahead} commits ahead of ${shortRef(w.baseRef)}, ${w.behind} behind`}>
      <span className={w.ahead > 0 ? "text-foreground" : undefined}>↑{w.ahead}</span>{" "}
      <span className={w.behind > 0 ? "text-warning-foreground" : undefined}>↓{w.behind}</span>
    </span>
  );
}

// What a check or land says, file by file: the counts, then the files,
// conflicts first.
export function LandReport({ files, limit = 200 }: { files: readonly LandFile[]; limit?: number }) {
  const counts = landCounts(files);
  const sorted = [...files].sort((a, b) => Number(b.state === "conflict") - Number(a.state === "conflict") || a.path.localeCompare(b.path));
  return (
    <div className="land-report flex flex-col gap-2">
      <div className="flex flex-wrap gap-1.5">
        {(Object.keys(counts) as LandState[])
          .filter((s) => counts[s] > 0)
          .map((s) => (
            <Badge key={s} variant={LAND_TONE[s]} data-land-count={s}>
              {counts[s]} {LAND_WORD[s]}
            </Badge>
          ))}
      </div>
      <ul className="max-h-56 overflow-y-auto rounded-lg ring-1 ring-hairline" aria-label="Files">
        {sorted.slice(0, limit).map((f) => (
          <li key={f.path} className="flex items-center gap-2 border-hairline border-b px-2.5 py-1.5 last:border-b-0" data-land={f.state}>
            <span className={cn("size-1.5 shrink-0 rounded-full", f.state === "conflict" ? "bg-destructive" : f.state === "merge" ? "bg-warning" : f.state === "fits" ? "bg-success" : "bg-muted-foreground/40")} />
            <span className="min-w-0 flex-1 truncate font-mono text-2xs" title={f.path}>
              {f.path}
            </span>
            <span className={cn("shrink-0 text-2xs", f.state === "conflict" ? "text-destructive-foreground" : "text-faint")} title={f.reason || undefined}>
              {f.state === "conflict" ? f.reason || "conflict" : LAND_WORD[f.state]}
            </span>
          </li>
        ))}
        {sorted.length > limit && <li className="px-2.5 py-1.5 text-2xs text-faint">… and {sorted.length - limit} more</li>}
      </ul>
    </div>
  );
}

// What to land: a worktree, or a capsule's latest version.
type Landing = { kind: "tree"; name: string; title: string } | { kind: "capsule"; name: string; title: string };

// The merge: a check of every file first, then Merge. A conflict says
// which files and why, and Merge waits for the person to pick whose
// version of them to keep (or to sync the worktree first).
function MergeDialog({
  api,
  sid,
  what,
  onDone,
  onClose,
}: {
  api: ProjectApi;
  sid: string;
  what: Landing;
  onDone: (text: string) => void;
  onClose: () => void;
}) {
  const [check, setCheck] = useState<WorkAction | string | null>(null);
  const [strategy, setStrategy] = useState<"" | "theirs" | "ours">("");
  const [busy, setBusy] = useState(false);
  const run = useCallback(
    (action: "check" | "merge" | "land", strat = "") =>
      what.kind === "tree"
        ? api.worktreeAction(sid, what.name, action === "land" ? "merge" : action, { strategy: strat })
        : api.capsuleAction(sid, what.name, action === "merge" ? "land" : action, { strategy: strat }),
    [api, sid, what],
  );
  useEffect(() => {
    void run("check").then(setCheck);
  }, [run]);
  const conflicts = typeof check === "object" && check !== null ? check.conflicts : [];
  const ready = typeof check === "object" && check !== null && (conflicts.length === 0 || strategy !== "");
  return (
    <Dialog
      title={`Merge ${what.title} into the shared tree`}
      description="Deck checks every file against the shared tree first. The changes land as uncommitted edits, merged three-way where both sides changed; your branch and index stay as they are."
      onClose={onClose}
      className="merge-dialog w-[560px]"
      footer={
        <>
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            className="merge-go"
            disabled={!ready || busy}
            onClick={async () => {
              setBusy(true);
              const r = await run("merge", strategy);
              setBusy(false);
              if (typeof r === "string") {
                setCheck(r);
                return;
              }
              if (r.conflicts.length > 0) {
                setCheck(r);
                return;
              }
              onDone(`Merged ${what.title}: ${plural(r.files.length, "file")} into the shared tree.`);
            }}
          >
            {busy ? <Loader2 className="animate-spin" /> : <GitMerge />} Merge
          </Button>
        </>
      }
    >
      {check === null ? (
        <p className="flex items-center gap-2 text-muted-foreground text-xs">
          <Loader2 className="size-3.5 animate-spin" /> Checking each file…
        </p>
      ) : typeof check === "string" ? (
        <p role="alert" className="text-destructive-foreground text-xs">
          {check}
        </p>
      ) : (
        <>
          {conflicts.length > 0 ? (
            <div role="alert" className="merge-conflicts flex flex-col gap-2 rounded-lg bg-destructive/8 p-3 ring-1 ring-destructive/30">
              <p className="flex items-center gap-1.5 font-medium text-destructive-foreground text-xs">
                <CircleAlert className="size-3.5" /> {plural(conflicts.length, "file")} changed on both sides
              </p>
              <p className="text-muted-foreground text-xs leading-relaxed">
                Nothing is written while they conflict. Sync the worktree with its base and settle them there, or keep one side of each:
              </p>
              <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Conflicting files">
                {(
                  [
                    ["theirs", what.kind === "tree" ? "Take the worktree's" : "Take the capsule's"],
                    ["ours", "Keep the shared tree's"],
                  ] as const
                ).map(([s, label]) => (
                  <Button key={s} variant="outline" size="xs" role="radio" aria-checked={strategy === s} aria-pressed={strategy === s} onClick={() => setStrategy(s)}>
                    {strategy === s && <Check />} {label}
                  </Button>
                ))}
              </div>
            </div>
          ) : (
            <p className="merge-clean flex items-center gap-1.5 text-success-foreground text-xs">
              <Check className="size-3.5" /> Every file lands cleanly.
            </p>
          )}
          <LandReport files={check.files} />
        </>
      )}
    </Dialog>
  );
}

// A worktree's row: its branch, owner, state, ahead and behind; open, its
// files and actions.
function TreeRow({
  w,
  capsule,
  open,
  onToggle,
  onAct,
  onMerge,
  onDiff,
  onSpawn,
  onDiscard,
  note,
}: {
  w: Worktree;
  capsule: Capsule | undefined;
  open: boolean;
  onToggle: () => void;
  onAct: (action: "sync" | "rebase") => void;
  onMerge: () => void;
  onDiff: () => void;
  onSpawn: () => void;
  onDiscard: () => void;
  // What the last action on it said: a sync's conflicts.
  note: { conflicts: string[]; text: string } | undefined;
}) {
  const st = treeState(w);
  const owner = w.ownerName || (w.kind === "thread" ? "this room" : "nobody yet");
  return (
    <li className={cn(CARD, "tree-row overflow-hidden")} data-tree={w.name} data-state={st}>
      <button
        type="button"
        className="flex w-full cursor-pointer items-center gap-2 px-3 py-2.5 text-left outline-none hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
        aria-expanded={open}
        onClick={onToggle}
      >
        {open ? <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" /> : <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />}
        <GitBranch className="size-3.5 shrink-0 text-info-foreground" />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-mono text-xs" title={w.branch}>
            {w.name}
          </span>
          <span className="truncate text-2xs text-faint">
            {owner}
            {w.task !== "" ? ` · ${w.task}` : ""} · from {shortRef(w.baseRef)}
          </span>
        </span>
        <Divergence w={w} />
        <span className="tree-files font-mono text-2xs text-muted-foreground tabular-nums" title={`${w.files.length} files changed, ${w.dirty} not committed`}>
          {w.files.length} files{w.dirty > 0 ? ` · ${w.dirty} dirty` : ""}
        </span>
        <Badge variant={TREE_TONE[st]} className="tree-state">
          {st === "working" && <Loader2 className="size-2.5 animate-spin" />}
          {TREE_STATE_LABEL[st]}
        </Badge>
      </button>
      {note !== undefined && note.conflicts.length > 0 && (
        <div role="alert" className="tree-conflict mx-3 mb-2 rounded-lg bg-destructive/8 px-2.5 py-2 text-xs ring-1 ring-destructive/30">
          <p className="flex items-center gap-1.5 font-medium text-destructive-foreground">
            <CircleAlert className="size-3.5" /> Sync stopped: {plural(note.conflicts.length, "file")} conflict with {shortRef(w.baseRef)}
          </p>
          <p className="mt-1 font-mono text-2xs text-muted-foreground">{note.conflicts.join(", ")}</p>
          <p className="mt-1 text-2xs text-faint">Nothing changed. Settle them in {w.path}, or ask its agent to.</p>
        </div>
      )}
      {open && (
        <div className="flex flex-col gap-2 border-hairline border-t px-3 py-2.5">
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-2xs">
            <dt className="text-faint">Path</dt>
            <dd className="truncate font-mono" title={w.path}>
              {w.path}
            </dd>
            <dt className="text-faint">Forked at</dt>
            <dd className="font-mono">{shortRef(w.fork)}</dd>
            {capsule !== undefined && (
              <>
                <dt className="text-faint">Capsule</dt>
                <dd className="font-mono">
                  {capsule.name} v{capsule.versions.length}
                </dd>
              </>
            )}
            {w.error !== "" && (
              <>
                <dt className="text-faint">Problem</dt>
                <dd className="text-destructive-foreground">{w.error}</dd>
              </>
            )}
          </dl>
          {w.files.length > 0 && (
            <ul className="tree-file-list max-h-32 overflow-y-auto font-mono text-2xs text-muted-foreground" aria-label={`Files ${w.name} changed`}>
              {w.files.slice(0, 50).map((f) => (
                <li key={f} className="truncate py-0.5" title={f}>
                  {f}
                </li>
              ))}
              {w.files.length > 50 && <li className="text-faint">… and {w.files.length - 50} more</li>}
            </ul>
          )}
          <div className="flex flex-wrap items-center gap-1">
            <Button variant="outline" size="xs" onClick={onDiff} disabled={w.files.length === 0}>
              <FileText /> Open diff
            </Button>
            <Button variant="outline" size="xs" onClick={() => onAct("sync")} disabled={w.working} title={`Merge ${shortRef(w.baseRef)} into it`}>
              <RefreshCw /> Sync
            </Button>
            <Button variant="ghost-muted" size="xs" onClick={() => onAct("rebase")} disabled={w.working} title={`Rebase it onto ${shortRef(w.baseRef)}`}>
              Rebase
            </Button>
            <Button size="xs" className="tree-merge" onClick={onMerge} disabled={w.files.length === 0}>
              <GitMerge /> Merge into main
            </Button>
            {w.kind === "tree" && (
              <Button variant="ghost-muted" size="xs" onClick={onSpawn}>
                <Play /> Start an agent here
              </Button>
            )}
            <Button variant="ghost-muted" size="xs" className="ml-auto text-destructive-foreground" onClick={onDiscard} aria-label={`Discard ${w.name}`}>
              <Trash2 /> Discard
            </Button>
          </div>
        </div>
      )}
    </li>
  );
}

function CapsuleRow({ c, onOpen }: { c: Capsule; onOpen: () => void }) {
  const v = latest(c);
  return (
    <li className={cn(CARD, "capsule-row")} data-capsule={c.name} data-state={c.state}>
      <button
        type="button"
        className="flex w-full cursor-pointer items-center gap-2 rounded-xl px-3 py-2.5 text-left outline-none hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring"
        onClick={onOpen}
      >
        <Package className="size-3.5 shrink-0 text-info-foreground" />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-mono text-xs">
            {c.name} <span className="text-faint">v{c.versions.length}</span>
          </span>
          <span className="truncate text-2xs text-faint">
            {c.source}
            {c.authors.length > 0 ? ` · ${c.authors.join(", ")}` : ""}
            {v !== undefined && v.commitsTotal > 0 ? ` · ${plural(v.commitsTotal, "commit")}` : ""}
          </span>
        </span>
        {v !== undefined && (
          <span className="font-mono text-2xs tabular-nums" title={`${v.files} files: ${v.add} added, ${v.mod} changed, ${v.del} deleted`}>
            <span className="text-diff-addition-foreground">+{v.add}</span> <span className="text-muted-foreground">~{v.mod}</span>{" "}
            <span className="text-diff-deletion-foreground">−{v.del}</span>
          </span>
        )}
        {c.tests !== "" && (
          <Badge variant={c.tests === "pass" ? "success" : "error"} className="capsule-tests">
            tests {c.tests}
          </Badge>
        )}
        <Badge variant={c.state === "landed" ? "success" : c.state === "reverted" ? "warning" : "muted"}>{c.state}</Badge>
      </button>
    </li>
  );
}

// One file of a capsule: its line counts; open, its diff, fetched then.
function CapsuleFileRow({ api, sid, capsule, f }: { api: ProjectApi; sid: string; capsule: string; f: CapsuleFile }) {
  const [diff, setDiff] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const { dir, name } = splitPath(f.path);
  return (
    <div className="capsule-file border-hairline border-b last:border-b-0" data-path={f.path}>
      <button
        type="button"
        className="sticky top-0 z-10 flex h-8 w-full cursor-pointer items-center gap-2 bg-background px-3 text-left outline-none hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
        aria-expanded={open}
        onClick={() => {
          setOpen((o) => !o);
          if (diff === null) void api.capsuleDiff(sid, capsule, f.path).then((d) => setDiff(typeof d === "string" ? d : `--- ${d.error}`));
        }}
      >
        {open ? <ChevronDown className="size-3 shrink-0 text-muted-foreground" /> : <ChevronRight className="size-3 shrink-0 text-muted-foreground" />}
        <span className="min-w-0 flex-1 truncate font-mono text-2xs" title={f.path}>
          <span className="text-faint">{dir}</span>
          {name}
        </span>
        <span className="text-3xs text-faint">{f.kind}</span>
        {f.binary ? (
          <span className="text-3xs text-faint">binary</span>
        ) : (
          <span className="font-mono text-2xs tabular-nums">
            <span className="text-diff-deletion-foreground">−{f.removed}</span> <span className="text-diff-addition-foreground">+{f.added}</span>
          </span>
        )}
      </button>
      {open &&
        (diff === null ? (
          <p className="flex items-center gap-2 px-3 py-2 text-2xs text-faint">
            <Loader2 className="size-3 animate-spin" /> Loading the diff…
          </p>
        ) : (
          <DiffView diff={diff} numbered path={f.path} />
        ))}
    </div>
  );
}

// A capsule open: what it is, its versions and commits, its review and
// tests, its actions, and its files a page at a time.
function CapsuleDetail({
  api,
  sid,
  c,
  roster,
  onBack,
  onLand,
  onInfo,
  onError,
}: {
  api: ProjectApi;
  sid: string;
  c: Capsule;
  roster: readonly AgentLine[];
  onBack: () => void;
  onLand: () => void;
  onInfo: (t: string) => void;
  onError: (t: string) => void;
}) {
  const [files, setFiles] = useState<CapsuleFile[]>([]);
  const [total, setTotal] = useState<number | null>(null);
  const [handTo, setHandTo] = useState<string>("");
  const [comment, setComment] = useState("");
  const [askRevert, setAskRevert] = useState(false);
  const v = latest(c);
  const ref = `${c.name}@${c.versions.length}`;
  const more = useCallback(
    async (from: number) => {
      const r = await api.capsuleFiles(sid, ref, from, FILE_PAGE);
      if (typeof r === "string") return onError(r);
      setTotal(r.total);
      setFiles((cur) => (from === 0 ? r.files : [...cur, ...r.files]));
    },
    [api, sid, ref, onError],
  );
  useEffect(() => {
    void more(0);
  }, [more]);
  const act = async (action: string, extra: Record<string, string>, done: string) => {
    const r = await api.capsuleAction(sid, c.name, action, extra);
    if (typeof r === "string") return onError(r);
    if (r.conflicts.length > 0) return onError(`${plural(r.conflicts.length, "file")} conflict: ${r.conflicts.join(", ")}. Nothing changed.`);
    onInfo(done);
    void refreshWork(api, sid);
  };
  return (
    <section className="capsule-detail flex min-h-0 flex-1 flex-col" aria-label={`Capsule ${c.name}`}>
      <div className="flex h-10 shrink-0 items-center gap-2 border-hairline border-b px-2">
        <Button variant="ghost-muted" size="icon-xs" aria-label="Back to the work" onClick={onBack}>
          <ArrowLeft />
        </Button>
        <Package className="size-3.5 text-info-foreground" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">
          {c.name} <span className="text-faint">v{c.versions.length}</span>
        </span>
        <Badge variant={c.state === "landed" ? "success" : "muted"}>{c.state}</Badge>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="flex flex-col gap-3 px-3.5 py-3">
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-2xs">
            <dt className="text-faint">From</dt>
            <dd>
              {c.source}
              {c.worktree !== "" ? <span className="font-mono"> {c.worktree}</span> : null}
            </dd>
            <dt className="text-faint">Authors</dt>
            <dd>{c.authors.join(", ") || "you"}</dd>
            {v !== undefined && (
              <>
                <dt className="text-faint">Version</dt>
                <dd>
                  v{c.versions.length} · {plural(v.files, "file")} (+{v.add} ~{v.mod} −{v.del}){v.onto !== "" ? ` · rebased onto ${v.onto}` : ""}
                  {v.fork !== "" ? <span className="font-mono text-faint"> · base {shortRef(v.fork)}</span> : null}
                </dd>
              </>
            )}
            <dt className="text-faint">Tests</dt>
            <dd className={c.tests === "fail" ? "text-destructive-foreground" : c.tests === "pass" ? "text-success-foreground" : "text-faint"}>
              {c.tests === "" ? "not run" : c.tests}
              {c.testNote !== "" ? ` · ${c.testNote}` : ""}
            </dd>
            {c.note !== "" && (
              <>
                <dt className="text-faint">Note</dt>
                <dd>{c.note}</dd>
              </>
            )}
          </dl>
          {v !== undefined && v.commits.length > 0 && (
            <ul className="capsule-commits flex flex-col gap-0.5" aria-label="Commits">
              {v.commits.slice(0, 8).map((cm) => (
                <li key={cm.sha} className="flex items-center gap-2 text-2xs">
                  <GitCommitHorizontal className="size-3 shrink-0 text-faint" />
                  <span className="font-mono text-faint">{cm.sha.slice(0, 7)}</span>
                  <span className="min-w-0 flex-1 truncate">{cm.subject}</span>
                  <span className="shrink-0 text-faint">{cm.author}</span>
                </li>
              ))}
              {v.commitsTotal > 8 && <li className="text-2xs text-faint">… and {v.commitsTotal - 8} more commits</li>}
            </ul>
          )}
          <div className="flex flex-wrap items-center gap-1">
            <Button size="xs" className="capsule-land" onClick={onLand}>
              <GitMerge /> Merge into main
            </Button>
            <Button variant="outline" size="xs" onClick={() => void act("rebase", { onto: "main" }, `Rebased ${c.name} onto the shared tree.`)}>
              <RefreshCw /> Rebase onto main
            </Button>
            {c.state === "landed" && (
              <Button variant="ghost-muted" size="xs" onClick={() => setAskRevert(true)}>
                <Undo2 /> Revert
              </Button>
            )}
            <span className="ml-auto flex items-center gap-1">
              <select
                className={cn(fieldClass, "h-6 w-auto py-0 text-2xs")}
                aria-label="Hand to agent"
                value={handTo}
                onChange={(e) => setHandTo(e.target.value)}
              >
                <option value="">Hand to…</option>
                {roster.map((a) => (
                  <option key={a.id} value={a.name || a.id}>
                    {a.name || a.id}
                  </option>
                ))}
              </select>
              <Button
                variant="outline"
                size="icon-xs"
                aria-label="Hand it over"
                disabled={handTo === ""}
                onClick={() => void act("hand", { agent: handTo }, `Handed ${c.name} to ${handTo}.`).then(() => setHandTo(""))}
              >
                <Send />
              </Button>
            </span>
          </div>
          <div className="flex flex-col gap-1.5">
            {c.comments.map((cm, i) => (
              <p key={i} className="capsule-comment rounded-lg bg-hover px-2.5 py-1.5 text-xs">
                <span className="font-medium">{cm.by}</span>
                {cm.path !== "" ? <span className="font-mono text-faint"> {cm.path}</span> : null}: {cm.text}
              </p>
            ))}
            <form
              className="flex items-center gap-1"
              onSubmit={(e) => {
                e.preventDefault();
                if (comment.trim() === "") return;
                void act("review", { text: comment }, "Comment added.").then(() => setComment(""));
              }}
            >
              <input className={cn(fieldClass, "h-7 py-0 text-xs")} placeholder="Review comment" aria-label="Review comment" value={comment} onChange={(e) => setComment(e.target.value)} />
              <Button variant="ghost-muted" size="xs" aria-label="Tests pass" onClick={() => void act("review", { tests: "pass" }, "Marked: tests pass.")}>
                <Check /> Pass
              </Button>
              <Button variant="ghost-muted" size="xs" aria-label="Tests fail" onClick={() => void act("review", { tests: "fail" }, "Marked: tests fail.")}>
                <X /> Fail
              </Button>
            </form>
          </div>
        </div>
        <div className="flex h-8 items-center border-hairline border-y px-3.5 text-2xs text-muted-foreground">
          {total === null ? "Loading the files…" : `${files.length} of ${plural(total, "file")}`}
        </div>
        {files.map((f) => (
          <CapsuleFileRow key={f.path} api={api} sid={sid} capsule={ref} f={f} />
        ))}
        {total !== null && files.length < total && (
          <div className="p-2 text-center">
            <Button variant="ghost-muted" size="xs" onClick={() => void more(files.length)}>
              Show {Math.min(FILE_PAGE, total - files.length)} more files
            </Button>
          </div>
        )}
      </div>
      {askRevert && (
        <Confirm
          title={`Revert ${c.name}?`}
          description="Its files go back in the shared tree to how they were before it landed. A file changed there since stops the revert."
          action="Revert"
          onClose={() => setAskRevert(false)}
          onConfirm={() => void act("revert", {}, `Reverted ${c.name}.`)}
        />
      )}
    </section>
  );
}

export function WorkPanel({
  api,
  sid,
  roster,
  kinds,
  onInfo,
  onError,
}: {
  api: ProjectApi;
  sid: string;
  roster: readonly AgentLine[];
  // The agent kinds a worktree can start.
  kinds: readonly string[];
  onInfo: (t: string) => void;
  onError: (t: string) => void;
}) {
  const work = useRoomWork(api, sid);
  const [open, setOpen] = useState<string | null>(null);
  const [capsule, setCapsule] = useState<string | null>(null);
  const [landing, setLanding] = useState<Landing | null>(null);
  const [discard, setDiscard] = useState<Worktree | null>(null);
  const [notes, setNotes] = useState<Record<string, { conflicts: string[]; text: string }>>({});
  const [making, setMaking] = useState(false);
  const [spawnIn, setSpawnIn] = useState<Worktree | null>(null);
  const refresh = () => void refreshWork(api, sid);

  const act = async (w: Worktree, action: "sync" | "rebase") => {
    const r = await api.worktreeAction(sid, w.name, action);
    if (typeof r === "string") return onError(r);
    setNotes((n) => ({ ...n, [w.name]: { conflicts: r.conflicts, text: r.text } }));
    if (r.conflicts.length === 0) onInfo(`${w.name} is up to date with ${shortRef(w.baseRef)}.`);
    refresh();
  };
  // Open diff: the worktree as a capsule (checked against the shared
  // tree, so it is current), opened.
  const diff = async (w: Worktree) => {
    const r = await api.worktreeAction(sid, w.name, "check");
    if (typeof r === "string") return onError(r);
    await refreshWork(api, sid);
    setCapsule(r.capsule || w.name);
  };

  const shown = capsule === null ? undefined : work?.capsules.find((c) => c.name === capsule);
  if (shown !== undefined) {
    return (
      <>
        <CapsuleDetail
          api={api}
          sid={sid}
          c={shown}
          roster={roster}
          onBack={() => setCapsule(null)}
          onLand={() => setLanding({ kind: "capsule", name: shown.name, title: shown.name })}
          onInfo={onInfo}
          onError={onError}
        />
        {landing !== null && (
          <MergeDialog
            api={api}
            sid={sid}
            what={landing}
            onClose={() => setLanding(null)}
            onDone={(t) => {
              setLanding(null);
              onInfo(t);
              refresh();
            }}
          />
        )}
      </>
    );
  }

  const trees = work?.worktrees ?? [];
  const caps = work?.capsules ?? [];
  return (
    <section aria-label="Work" className="work-panel flex min-h-0 flex-1 flex-col">
      <div className="panel-tools flex h-10 shrink-0 items-center gap-2 border-hairline border-b px-3.5 text-xs">
        <span className="font-medium text-foreground">Work</span>
        <span className="text-faint">{work === null ? "Loading…" : `${plural(trees.length, "worktree")} · ${plural(caps.length, "capsule")}`}</span>
        <div className="ml-auto flex items-center gap-0.5">
          <Button variant="ghost-muted" size="xs" className="font-normal" onClick={() => setMaking(true)}>
            <Plus /> Worktree
          </Button>
          <Button
            variant="ghost-muted"
            size="xs"
            className="font-normal"
            title="Bundle the shared tree's uncommitted changes as a capsule"
            onClick={async () => {
              const r = await api.capsuleAction(sid, "", "create", { source: "diff" });
              if (typeof r === "string") return onError(r);
              onInfo(`Capsule ${r.capsule} v${r.version} made from your changes.`);
              refresh();
            }}
          >
            <Package /> Capsule
          </Button>
          <Button variant="ghost-muted" size="icon-xs" aria-label="Refresh" title="Refresh" onClick={refresh}>
            <RefreshCw />
          </Button>
        </div>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-3 py-3">
        <Section title="Worktrees" count={trees.length}>
          {trees.length === 0 ? (
            <EmptyState
              icon={GitBranch}
              title="No worktrees."
              hint="Agents started with a worktree, races and long tasks work on branches of their own; they show here with how far they are from their base."
              className="py-6"
            />
          ) : (
            <ul className="flex flex-col gap-1.5">
              {trees.map((w) => (
                <TreeRow
                  key={w.name}
                  w={w}
                  capsule={work === null ? undefined : capsuleOfTree(work, w)}
                  open={open === w.name}
                  note={notes[w.name]}
                  onToggle={() => setOpen((o) => (o === w.name ? null : w.name))}
                  onAct={(a) => void act(w, a)}
                  onMerge={() => setLanding({ kind: "tree", name: w.name, title: w.name })}
                  onDiff={() => void diff(w)}
                  onSpawn={() => setSpawnIn(w)}
                  onDiscard={() => setDiscard(w)}
                />
              ))}
            </ul>
          )}
        </Section>
        <Section title="Capsules" count={caps.length}>
          {caps.length === 0 ? (
            <EmptyState
              icon={Package}
              title="No capsules."
              hint="A capsule bundles a worktree, a range of commits or your changes as one versioned unit to hand over, review and merge."
              className="py-6"
            />
          ) : (
            <ul className="flex flex-col gap-1.5">
              {caps.map((c) => (
                <CapsuleRow key={c.name} c={c} onOpen={() => setCapsule(c.name)} />
              ))}
            </ul>
          )}
        </Section>
      </div>
      {landing !== null && (
        <MergeDialog
          api={api}
          sid={sid}
          what={landing}
          onClose={() => setLanding(null)}
          onDone={(t) => {
            setLanding(null);
            onInfo(t);
            refresh();
          }}
        />
      )}
      {discard !== null && (
        <Confirm
          title={`Discard ${discard.name}?`}
          description={`The worktree at ${discard.path} and its branch ${discard.branch} go, with ${plural(discard.files.length, "changed file")} never merged.${discard.ownerName !== "" ? ` ${discard.ownerName} stops working there.` : ""}`}
          action="Discard"
          onClose={() => setDiscard(null)}
          onConfirm={async () => {
            const r = await api.worktreeAction(sid, discard.name, "discard");
            if (typeof r === "string") onError(r);
            refresh();
          }}
        />
      )}
      {making && (
        <NewTree
          onClose={() => setMaking(false)}
          onMake={async (name, base) => {
            const r = await api.worktreeAction(sid, name, "create", { base });
            if (typeof r === "string") return r;
            onInfo(`Made worktree ${r.worktree?.name ?? name}.`);
            refresh();
            return null;
          }}
        />
      )}
      {spawnIn !== null && (
        <SpawnInTree
          w={spawnIn}
          kinds={kinds}
          onClose={() => setSpawnIn(null)}
          onStart={async (kind, brief) => {
            const r = await api.worktreeAction(sid, spawnIn.name, "spawn", { kind, brief });
            if (typeof r === "string") return r;
            onInfo(`Started ${r.agentId} in ${spawnIn.name}.`);
            refresh();
            return null;
          }}
        />
      )}
    </section>
  );
}

// A new worktree: a name and the branch or commit it starts from.
function NewTree({ onClose, onMake }: { onClose: () => void; onMake: (name: string, base: string) => Promise<string | null> }) {
  const [name, setName] = useState("");
  const [base, setBase] = useState("");
  const [err, setErr] = useState<string | null>(null);
  return (
    <Dialog
      title="New worktree"
      description="A branch of its own, apart from the shared tree, for long or big work. Start an agent in it from its row."
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={async () => {
              const e = await onMake(name.trim(), base.trim());
              if (e === null) onClose();
              else setErr(e);
            }}
          >
            <GitBranch /> Make it
          </Button>
        </>
      }
    >
      <input className={fieldClass} placeholder="Name, e.g. store-refactor" aria-label="Worktree name" value={name} onChange={(e) => setName(e.target.value)} />
      <input className={fieldClass} placeholder="Start from (branch or commit; your HEAD if empty)" aria-label="Base" value={base} onChange={(e) => setBase(e.target.value)} />
      {err !== null && (
        <p role="alert" className="text-destructive-foreground text-xs">
          {err}
        </p>
      )}
    </Dialog>
  );
}

function SpawnInTree({
  w,
  kinds,
  onClose,
  onStart,
}: {
  w: Worktree;
  kinds: readonly string[];
  onClose: () => void;
  onStart: (kind: string, brief: string) => Promise<string | null>;
}) {
  const [kind, setKind] = useState(kinds[0] ?? "");
  const [brief, setBrief] = useState("");
  const [err, setErr] = useState<string | null>(null);
  return (
    <Dialog
      title={`Start an agent in ${w.name}`}
      description="It works on the worktree's branch, and the worktree becomes its own."
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={brief.trim() === ""}
            onClick={async () => {
              const e = await onStart(kind, brief.trim());
              if (e === null) onClose();
              else setErr(e);
            }}
          >
            <Play /> Start
          </Button>
        </>
      }
    >
      <select className={fieldClass} aria-label="Agent" value={kind} onChange={(e) => setKind(e.target.value)}>
        {kinds.map((k) => (
          <option key={k} value={k}>
            {k}
          </option>
        ))}
      </select>
      <textarea className={cn(fieldClass, "min-h-24")} placeholder="What should it do?" aria-label="Brief" value={brief} onChange={(e) => setBrief(e.target.value)} />
      {err !== null && (
        <p role="alert" className="text-destructive-foreground text-xs">
          {err}
        </p>
      )}
    </Dialog>
  );
}
