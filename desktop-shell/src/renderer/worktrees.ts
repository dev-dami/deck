import { bool, list, num, type Obj, obj, str, strs } from "./guards";

// A room's worktrees and named capsules (deck.v1 Work): the lines of work
// its agents run on branches of their own, and the units of change that
// come out of them. Pure, so it is tested; workState.ts polls the Deck and
// the Work tab, the lanes and the Work Graph read it.

// kind: "thread" (the room works on it), "agent" (one agent's own) or
// "tree" (the room's, nobody's yet). ahead and behind count commits
// against baseRef; dirty the uncommitted files; files what it changed
// since it forked. landed: nothing changed since it was last landed.
export interface Worktree {
  name: string;
  branch: string;
  path: string;
  kind: string;
  owner: string;
  ownerName: string;
  task: string;
  working: boolean;
  created: number;
  fork: string;
  baseRef: string;
  ahead: number;
  behind: number;
  dirty: number;
  files: string[];
  landedAt: number;
  landed: boolean;
  error: string;
}

export interface CapsuleCommit {
  sha: string;
  subject: string;
  author: string;
}

export interface CapsuleVersion {
  id: string;
  fork: string;
  head: string;
  commits: CapsuleCommit[];
  commitsTotal: number;
  onto: string;
  files: number;
  add: number;
  mod: number;
  del: number;
  by: string;
  note: string;
  created: number;
}

export interface CapsuleComment {
  by: string;
  path: string;
  text: string;
  at: number;
}

export interface CapsuleLanding {
  version: number;
  into: string;
  at: number;
  by: string;
  reverted: boolean;
}

// state: "open", "landed" or "reverted"; tests: "pass", "fail" or "".
export interface Capsule {
  name: string;
  source: string;
  worktree: string;
  authors: string[];
  note: string;
  state: string;
  versions: CapsuleVersion[];
  tests: string;
  testNote: string;
  comments: CapsuleComment[];
  landings: CapsuleLanding[];
  created: number;
  updated: number;
}

export interface Work {
  worktrees: Worktree[];
  capsules: Capsule[];
}

export type LandState = "fits" | "applied" | "merge" | "conflict";

export interface LandFile {
  path: string;
  kind: string;
  state: LandState;
  reason: string;
}

// What a worktree or capsule action answered. conflicts: nothing was
// written. checked: it was only a check.
export interface WorkAction {
  text: string;
  files: LandFile[];
  conflicts: string[];
  capsule: string;
  version: number;
  worktree: Worktree | null;
  checked: boolean;
  agentId: string;
}

export interface CapsuleFile {
  path: string;
  kind: string;
  added: number;
  removed: number;
  binary: boolean;
}

export const EMPTY_WORK: Work = { worktrees: [], capsules: [] };

export function toWorktree(o: Obj): Worktree {
  return {
    name: str(o, "name"),
    branch: str(o, "branch"),
    path: str(o, "path"),
    kind: str(o, "kind"),
    owner: str(o, "owner"),
    ownerName: str(o, "owner_name"),
    task: str(o, "task"),
    working: bool(o, "working"),
    created: num(o, "created"),
    fork: str(o, "fork"),
    baseRef: str(o, "base_ref"),
    ahead: num(o, "ahead"),
    behind: num(o, "behind"),
    dirty: num(o, "dirty"),
    files: strs(o, "files"),
    landedAt: num(o, "landed_at"),
    landed: bool(o, "landed"),
    error: str(o, "error"),
  };
}

function toVersion(o: Obj): CapsuleVersion {
  return {
    id: str(o, "id"),
    fork: str(o, "fork"),
    head: str(o, "head"),
    commits: list(o, "commits", (c) => ({ sha: str(c, "sha"), subject: str(c, "subject"), author: str(c, "author") })),
    commitsTotal: num(o, "commits_total"),
    onto: str(o, "onto"),
    files: num(o, "files"),
    add: num(o, "add"),
    mod: num(o, "mod"),
    del: num(o, "del"),
    by: str(o, "by"),
    note: str(o, "note"),
    created: num(o, "created"),
  };
}

export function toCapsule(o: Obj): Capsule {
  return {
    name: str(o, "name"),
    source: str(o, "source"),
    worktree: str(o, "worktree"),
    authors: strs(o, "authors"),
    note: str(o, "note"),
    state: str(o, "state") || "open",
    versions: list(o, "versions", toVersion),
    tests: str(o, "tests"),
    testNote: str(o, "test_note"),
    comments: list(o, "comments", (c) => ({ by: str(c, "by"), path: str(c, "path"), text: str(c, "text"), at: num(c, "at") })),
    landings: list(o, "landings", (l) => ({ version: num(l, "version"), into: str(l, "into"), at: num(l, "at"), by: str(l, "by"), reverted: bool(l, "reverted") })),
    created: num(o, "created"),
    updated: num(o, "updated"),
  };
}

export function toWork(v: unknown): Work {
  const o = obj(v);
  return { worktrees: list(o, "worktrees", toWorktree), capsules: list(o, "capsules", toCapsule) };
}

const STATES: readonly LandState[] = ["fits", "applied", "merge", "conflict"];

export function toWorkAction(v: unknown): WorkAction {
  const o = obj(v);
  const wt = o["worktree"];
  return {
    text: str(o, "text"),
    files: list(o, "files", (f) => {
      const st = str(f, "state") as LandState;
      return { path: str(f, "path"), kind: str(f, "kind"), state: STATES.includes(st) ? st : "conflict", reason: str(f, "reason") };
    }),
    conflicts: strs(o, "conflicts"),
    capsule: str(o, "capsule"),
    version: num(o, "version"),
    worktree: typeof wt === "object" && wt !== null ? toWorktree(wt as Obj) : null,
    checked: bool(o, "checked"),
    agentId: str(o, "agent_id"),
  };
}

export const toCapsuleFile = (o: Obj): CapsuleFile => ({
  path: str(o, "path"),
  kind: str(o, "kind"),
  added: num(o, "added"),
  removed: num(o, "removed"),
  binary: bool(o, "binary"),
});

// ---- reading it --------------------------------------------------------

// A commit as people read it; a branch name stays as it is.
export const shortRef = (ref: string): string => (/^[0-9a-f]{40}$/.test(ref) ? ref.slice(0, 8) : ref);

// The latest version of a capsule.
export const latest = (c: Capsule): CapsuleVersion | undefined => c.versions.at(-1);

// How many files of a land or check end up each way.
export function landCounts(files: readonly LandFile[]): Record<LandState, number> {
  const out: Record<LandState, number> = { fits: 0, applied: 0, merge: 0, conflict: 0 };
  for (const f of files) out[f.state]++;
  return out;
}

// One word on where a worktree stands, for its row and the graph: its
// error, working, landed, behind its base, changed, or clean.
export type TreeState = "gone" | "working" | "landed" | "behind" | "changed" | "clean";

export function treeState(w: Worktree): TreeState {
  if (w.error !== "") return "gone";
  if (w.working) return "working";
  if (w.landed) return "landed";
  if (w.behind > 0) return "behind";
  return w.files.length > 0 || w.dirty > 0 ? "changed" : "clean";
}

export const TREE_STATE_LABEL: Record<TreeState, string> = {
  gone: "Gone",
  working: "Working",
  landed: "Landed",
  behind: "Behind",
  changed: "Changed",
  clean: "Clean",
};

// The worktree each agent works on, by agent name: the lanes' branch badge.
export function branchesByAgent(work: Work | null): Map<string, string> {
  const out = new Map<string, string>();
  for (const w of work?.worktrees ?? []) {
    if (w.kind === "agent" && w.ownerName !== "") out.set(w.ownerName, w.branch);
    if (w.kind === "agent" && w.owner !== "" && !out.has(w.owner)) out.set(w.owner, w.branch);
  }
  return out;
}

// The capsule a worktree became when it was checked or landed (named after
// it), if any.
export const capsuleOfTree = (work: Work, w: Worktree): Capsule | undefined =>
  work.capsules.find((c) => c.source === "worktree" && (c.worktree === w.branch || c.name === w.name));

// How many files a capsule's list asks for at a time, so a capsule of
// thousands of files never loads whole.
export const FILE_PAGE = 100;

// "a.go" from "pkg/x/a.go", with its folder apart.
export function splitPath(p: string): { dir: string; name: string } {
  const i = p.lastIndexOf("/");
  return i < 0 ? { dir: "", name: p } : { dir: p.slice(0, i + 1), name: p.slice(i + 1) };
}
