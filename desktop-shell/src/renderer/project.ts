import type { DeckBridge, RpcMethod, TermEvent } from "../shared/bridge";
import { type Board, type Crew, type SharedMirror, type Team, toBoard, toCrew, toMirror, toTeam } from "./crew";

// What a local Deck adds to the shared DeckClient: the project's git work
// tree, the usage record and terminals (proto/deck/v1, Git*, Usage,
// Terminal*). A paired Deck (protocol v2) has none of it.

export interface GitBranch {
  name: string;
  upstream: string;
  committedAt: number;
  subject: string;
}

export interface GitCheck {
  name: string;
  bucket: string;
  link: string;
}

export interface PullRequest {
  number: number;
  title: string;
  state: string;
  url: string;
  draft: boolean;
  reviewDecision: string;
  mergeable: string;
  base: string;
  checks: GitCheck[];
}

export interface GitStatus {
  repo: boolean;
  branch: string;
  upstream: string;
  ahead: number;
  behind: number;
  dirty: number;
  detached: boolean;
  head: string;
  defaultBranch: string;
  branches: GitBranch[];
  gh: boolean;
  pullRequest: PullRequest | null;
}

export interface UsageOverview {
  turns: number;
  allTurns: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  cacheWriteTokens: number;
  reasoningTokens: number;
  costUsd: number;
  durationMs: number;
  ttftAvgMs: number;
  ttftP95Ms: number;
  cacheHitPct: number;
  done: number;
  interrupted: number;
  failed: number;
}

export interface UsageDay {
  day: number;
  turns: number;
  tokens: number;
  costUsd: number;
}

export interface UsageGroup {
  kind: string;
  model: string;
  turns: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  costUsd: number;
}

export interface UsageSession {
  sessionId: string;
  title: string;
  turns: number;
  tokens: number;
  costUsd: number;
}

export interface UsageLimit {
  agentKind: string;
  windowId: string;
  kind: string;
  label: string;
  usedPct: number;
  resetsAt: number;
  durationMins: number;
  plan: string;
  at: number;
}

export interface UsageHabits {
  activeDays: number;
  streak: number;
  hours: number[];
  reviewMedianMs: number;
  reviewP90Ms: number;
  approvalMedianMs: number;
  approvalFastPct: number;
  userTurns: number;
  redirects: number;
  followUps: number;
  promptMedian: number;
  editTurns: number;
  editFiles: number;
  linesAdded: number;
  linesRemoved: number;
  reverts: number;
  fit: { kind: string; class: string; calls: number }[];
}

export interface Usage {
  available: boolean;
  overview: UsageOverview;
  days: UsageDay[];
  kinds: UsageGroup[];
  models: UsageGroup[];
  sessions: UsageSession[];
  limits: UsageLimit[];
  heat: UsageDay[];
  habits: UsageHabits | null;
}

// Readers for the plain objects the proto loader makes: snake_case keys,
// int64 as numbers, defaults filled in. Anything else reads as empty.
type Obj = Record<string, unknown>;
const obj = (v: unknown): Obj => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Obj) : {});
const str = (o: Obj, k: string): string => (typeof o[k] === "string" ? (o[k] as string) : "");
const num = (o: Obj, k: string): number => (typeof o[k] === "number" && Number.isFinite(o[k]) ? (o[k] as number) : 0);
const bool = (o: Obj, k: string): boolean => o[k] === true;
const list = <T>(o: Obj, k: string, f: (v: Obj) => T): T[] => (Array.isArray(o[k]) ? (o[k] as unknown[]).map((v) => f(obj(v))) : []);

function toPR(o: Obj): PullRequest {
  return {
    number: num(o, "number"),
    title: str(o, "title"),
    state: str(o, "state"),
    url: str(o, "url"),
    draft: bool(o, "draft"),
    reviewDecision: str(o, "review_decision"),
    mergeable: str(o, "mergeable"),
    base: str(o, "base"),
    checks: list(o, "checks", (c) => ({ name: str(c, "name"), bucket: str(c, "bucket"), link: str(c, "link") })),
  };
}

export function toGitStatus(v: unknown): GitStatus {
  const o = obj(v);
  const pr = o["pull_request"];
  return {
    repo: bool(o, "repo"),
    branch: str(o, "branch"),
    upstream: str(o, "upstream"),
    ahead: num(o, "ahead"),
    behind: num(o, "behind"),
    dirty: num(o, "dirty"),
    detached: bool(o, "detached"),
    head: str(o, "head"),
    defaultBranch: str(o, "default_branch"),
    branches: list(o, "branches", (b) => ({
      name: str(b, "name"),
      upstream: str(b, "upstream"),
      committedAt: num(b, "committed_at"),
      subject: str(b, "subject"),
    })),
    gh: bool(o, "gh"),
    pullRequest: pr === null || pr === undefined ? null : toPR(obj(pr)),
  };
}

const toDay = (d: Obj): UsageDay => ({
  day: num(d, "day"),
  turns: num(d, "turns"),
  tokens: num(d, "tokens"),
  costUsd: num(d, "cost_usd"),
});

const toGroup = (g: Obj): UsageGroup => ({
  kind: str(g, "kind"),
  model: str(g, "model"),
  turns: num(g, "turns"),
  inputTokens: num(g, "input_tokens"),
  outputTokens: num(g, "output_tokens"),
  cachedTokens: num(g, "cached_tokens"),
  costUsd: num(g, "cost_usd"),
});

export function toUsage(v: unknown): Usage {
  const o = obj(v);
  const ov = obj(o["overview"]);
  const h = o["habits"];
  const hb = obj(h);
  return {
    available: bool(o, "available"),
    overview: {
      turns: num(ov, "turns"),
      allTurns: num(ov, "all_turns"),
      inputTokens: num(ov, "input_tokens"),
      outputTokens: num(ov, "output_tokens"),
      cachedTokens: num(ov, "cached_tokens"),
      cacheWriteTokens: num(ov, "cache_write_tokens"),
      reasoningTokens: num(ov, "reasoning_tokens"),
      costUsd: num(ov, "cost_usd"),
      durationMs: num(ov, "duration_ms"),
      ttftAvgMs: num(ov, "ttft_avg_ms"),
      ttftP95Ms: num(ov, "ttft_p95_ms"),
      cacheHitPct: num(ov, "cache_hit_pct"),
      done: num(ov, "done"),
      interrupted: num(ov, "interrupted"),
      failed: num(ov, "failed"),
    },
    days: list(o, "days", toDay),
    kinds: list(o, "kinds", toGroup),
    models: list(o, "models", toGroup),
    sessions: list(o, "sessions", (x) => ({
      sessionId: str(x, "session_id"),
      title: str(x, "title"),
      turns: num(x, "turns"),
      tokens: num(x, "tokens"),
      costUsd: num(x, "cost_usd"),
    })),
    limits: list(o, "limits", (l) => ({
      agentKind: str(l, "agent_kind"),
      windowId: str(l, "window_id"),
      kind: str(l, "kind"),
      label: str(l, "label"),
      usedPct: num(l, "used_pct"),
      resetsAt: num(l, "resets_at"),
      durationMins: num(l, "duration_mins"),
      plan: str(l, "plan"),
      at: num(l, "at"),
    })),
    heat: list(o, "heat", toDay),
    habits:
      h === null || h === undefined
        ? null
        : {
            activeDays: num(hb, "active_days"),
            streak: num(hb, "streak"),
            hours: Array.isArray(hb["hours"]) ? (hb["hours"] as unknown[]).map((n) => (typeof n === "number" ? n : 0)) : [],
            reviewMedianMs: num(hb, "review_median_ms"),
            reviewP90Ms: num(hb, "review_p90_ms"),
            approvalMedianMs: num(hb, "approval_median_ms"),
            approvalFastPct: num(hb, "approval_fast_pct"),
            userTurns: num(hb, "user_turns"),
            redirects: num(hb, "redirects"),
            followUps: num(hb, "follow_ups"),
            promptMedian: num(hb, "prompt_median"),
            editTurns: num(hb, "edit_turns"),
            editFiles: num(hb, "edit_files"),
            linesAdded: num(hb, "lines_added"),
            linesRemoved: num(hb, "lines_removed"),
            reverts: num(hb, "reverts"),
            fit: list(hb, "fit", (f) => ({ kind: str(f, "kind"), class: str(f, "class"), calls: num(f, "calls") })),
          },
  };
}

// Base64 of a string's UTF-8 bytes, for a bytes field.
export function utf8Base64(s: string): string {
  let bin = "";
  for (const b of new TextEncoder().encode(s)) bin += String.fromCharCode(b);
  return btoa(bin);
}

export function base64Bytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export type TermListener = (ev: TermEvent) => void;

// The gRPC status of a stream that fell behind.
const RESOURCE_EXHAUSTED = 8;

// The project calls of one local Deck. Failures come back as their message
// so the caller can show it where it happened.
export class ProjectApi {
  private readonly listeners = new Map<string, Set<TermListener>>();
  private readonly off: () => void;

  constructor(
    private readonly bridge: DeckBridge,
    private readonly deckId: string,
    // Loads a thread's messages without opening it.
    readonly loadThread: (id: string) => Promise<void> = async () => {},
  ) {
    this.off = bridge.onRpc((id, ev) => {
      if (id !== deckId || (ev.type !== "term" && ev.type !== "term-exit" && ev.type !== "term-end")) return;
      for (const l of this.listeners.get(ev.term) ?? []) l(ev);
    });
  }

  dispose(): void {
    this.off();
    this.listeners.clear();
  }

  private async call(method: RpcMethod, request: Record<string, unknown>): Promise<{ ok: true; value: unknown } | { ok: false; message: string }> {
    try {
      const r = await this.bridge.rpcCall(this.deckId, method, request);
      return r.ok ? r : { ok: false, message: r.message || `Deck refused ${method}.` };
    } catch (e) {
      return { ok: false, message: e instanceof Error ? e.message : String(e) };
    }
  }

  // The work tree, or the error.
  async gitStatus(): Promise<GitStatus | string> {
    const r = await this.call("GitStatus", {});
    return r.ok ? toGitStatus(r.value) : r.message;
  }

  // null when done, else git's own words.
  async git(method: "GitCheckout" | "GitCommit" | "GitPush" | "GitPull" | "GitMergePR", request: Record<string, unknown> = {}): Promise<string | null> {
    const r = await this.call(method, request);
    return r.ok ? null : r.message;
  }

  async createPR(title: string, body: string, base: string, draft: boolean): Promise<PullRequest | string> {
    const r = await this.call("GitCreatePR", { title, body, base, draft });
    return r.ok ? toPR(obj(r.value)) : r.message;
  }

  async usage(days: number): Promise<Usage | string> {
    const r = await this.call("Usage", { days });
    return r.ok ? toUsage(r.value) : r.message;
  }

  async board(sessionId: string): Promise<Board | string> {
    const r = await this.call("Board", { session_id: sessionId });
    return r.ok ? toBoard(r.value) : r.message;
  }

  // cancel, retry, steer (with text), apply or discard; the files an apply
  // wrote, or the error.
  async taskAction(sessionId: string, taskId: string, action: string, text = ""): Promise<string[] | string> {
    const r = await this.call("TaskAction", { session_id: sessionId, task_id: taskId, action, text });
    if (!r.ok) return r.message;
    const files = (r.value as { files?: unknown }).files;
    return Array.isArray(files) ? files.filter((f): f is string => typeof f === "string") : [];
  }

  async setOrchestrate(sessionId: string, on: boolean): Promise<string | null> {
    const r = await this.call("SetOrchestrate", { session_id: sessionId, on });
    return r.ok ? null : r.message;
  }

  async agents(sessionId: string): Promise<Crew | string> {
    const r = await this.call("Agents", { session_id: sessionId });
    return r.ok ? toCrew(r.value) : r.message;
  }

  async team(): Promise<Team | string> {
    const r = await this.call("Team", {});
    return r.ok ? toTeam(r.value) : r.message;
  }

  // The teammates a post reached, or the error.
  async teamPost(text: string): Promise<string[] | string> {
    const r = await this.call("TeamPost", { text });
    if (!r.ok) return r.message;
    const reached = (r.value as { reached?: unknown }).reached;
    return Array.isArray(reached) ? reached.filter((f): f is string => typeof f === "string") : [];
  }

  async shareThread(sessionId: string, peer: string, role: string): Promise<string | null> {
    const r = await this.call("ShareThread", { session_id: sessionId, peer, role });
    return r.ok ? null : r.message;
  }

  async threadShares(sessionId: string): Promise<Record<string, string> | string> {
    const r = await this.call("ThreadShares", { session_id: sessionId });
    if (!r.ok) return r.message;
    const shares = obj(obj(r.value)["shares"]);
    return Object.fromEntries(Object.entries(shares).filter((e): e is [string, string] => typeof e[1] === "string"));
  }

  async proposeContract(sessionId: string, peer: string, brief: string): Promise<{ taskId: string; queued: boolean } | string> {
    const r = await this.call("ProposeContract", { session_id: sessionId, peer, brief });
    if (!r.ok) return r.message;
    const o = obj(r.value);
    return { taskId: str(o, "task_id"), queued: bool(o, "queued") };
  }

  async answerContract(peer: string, id: string, accept: boolean, agent: string): Promise<string | null> {
    const r = await this.call("AnswerContract", { peer, id, accept, agent });
    return r.ok ? null : r.message;
  }

  async readShared(peer: string, thread: string, after: number): Promise<SharedMirror | string> {
    const r = await this.call("ReadShared", { peer, thread, after });
    return r.ok ? toMirror(r.value) : r.message;
  }

  async postShared(peer: string, thread: string, text: string): Promise<string | null> {
    const r = await this.call("PostShared", { peer, thread, text });
    return r.ok ? null : r.message;
  }

  async answerShared(peer: string, thread: string, approval: string, allow: boolean): Promise<string | null> {
    const r = await this.call("AnswerShared", { peer, thread, approval, allow });
    return r.ok ? null : r.message;
  }

  async terminalOpen(cols: number, rows: number): Promise<string | { error: string }> {
    const r = await this.call("TerminalOpen", { cols, rows });
    if (!r.ok) return { error: r.message };
    const id = str(obj(r.value), "terminal_id");
    return id === "" ? { error: "Deck did not open a terminal." } : id;
  }

  terminalWrite(id: string, data: string): void {
    void this.call("TerminalWrite", { terminal_id: id, data: utf8Base64(data) });
  }

  terminalResize(id: string, cols: number, rows: number): void {
    void this.call("TerminalResize", { terminal_id: id, cols, rows });
  }

  terminalClose(id: string): void {
    void this.bridge.termUnsubscribe(this.deckId, id);
    void this.call("TerminalClose", { terminal_id: id });
  }

  // Watches a terminal's output, from its scrollback. The stream comes back
  // by itself when it ends early; the returned function stops watching.
  watchTerminal(id: string, fn: TermListener): () => void {
    let set = this.listeners.get(id);
    if (set === undefined) {
      set = new Set();
      this.listeners.set(id, set);
    }
    set.add(fn);
    let retry: ReturnType<typeof setTimeout> | null = null;
    const resub: TermListener = (ev) => {
      // Only a stream that fell behind comes back; a closed one stays closed.
      if (ev.type === "term-end" && ev.code === RESOURCE_EXHAUSTED) {
        retry = setTimeout(() => void this.bridge.termSubscribe(this.deckId, id), 200);
      }
    };
    set.add(resub);
    void this.bridge.termSubscribe(this.deckId, id);
    return () => {
      if (retry !== null) clearTimeout(retry);
      set.delete(fn);
      set.delete(resub);
      void this.bridge.termUnsubscribe(this.deckId, id);
    };
  }
}
