import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import {
  Check,
  ChevronDown,
  ChevronRight,
  CornerDownRight,
  Flag,
  GitBranch,
  Loader2,
  Lock,
  Minus,
  Plus,
  RotateCcw,
  Trash2,
  Trophy,
  X,
} from "lucide-react";
import { type ReactNode, useCallback, useState } from "react";
import { type AgentNode, type Board, type BoardTask, boardRows, type Crew, raceMessage, type RunningTool } from "../crew";
import { refreshCrew, useCrew } from "../crewState";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { formatDuration } from "../work";
import { Button } from "./kit/button";
import { fieldClass } from "./kit/dialog";

// What the agents of a thread are and do, after the terminal UI's Agents
// and Tasks panes: the agents as a tree with what each runs right now, the
// task board they split the work into (races and their entries, reviews),
// and a way to race agents on a task.

const DOT: Record<string, string> = {
  working: "bg-info animate-deck-pulse",
  tool: "bg-info animate-deck-pulse",
  approval: "bg-warning",
  error: "bg-destructive",
  exited: "bg-muted-foreground/30",
};

function AgentRow({ a, running, theme, now }: { a: AgentNode; running: RunningTool[]; theme: Theme | null; now: number }) {
  const pct = a.contextWindow > 0 ? Math.round((a.contextTokens / a.contextWindow) * 100) : null;
  return (
    <li className="crew-agent flex flex-col gap-1 py-2" data-agent={a.name} style={{ paddingLeft: `${a.depth * 16}px` }}>
      <div className="flex min-w-0 items-center gap-2 text-sm">
        {a.depth > 0 && <CornerDownRight className="size-3.5 shrink-0 text-muted-foreground/50" />}
        <span className={cn("size-2 shrink-0 rounded-full", DOT[a.status] ?? "bg-muted-foreground/50")} />
        <span className="font-mono text-xs" style={{ color: kindColour(a.kind) }}>
          {kindMark(theme, a.kind) || "●"}
        </span>
        <span className="font-medium">{a.name}</span>
        <span className="min-w-0 truncate text-muted-foreground text-xs">
          {a.model || "default"}
          {a.effort !== "" ? ` · ${a.effort}` : ""}
        </span>
        <span className={cn("agent-status ml-auto shrink-0 text-xs", a.status === "approval" ? "text-warning-foreground" : "text-muted-foreground")}>
          {a.status === "tool" ? "running a tool" : a.status}
          {a.since > 0 && (a.status === "working" || a.status === "tool") ? ` · ${formatDuration(now - a.since)}` : ""}
        </span>
      </div>
      {running.map((t, i) => (
        <div key={i} className="ml-6 flex min-w-0 items-center gap-1.5 text-xs">
          <Loader2 className="size-3 shrink-0 animate-spin text-muted-foreground" />
          <span className="shrink-0 text-foreground/80">{t.subagent ? "subagent" : t.name}</span>
          <span className="min-w-0 truncate font-mono text-muted-foreground">{t.summary}</span>
          {t.started > 0 && <span className="ml-auto shrink-0 text-muted-foreground/70 tabular-nums">{formatDuration(now - t.started)}</span>}
        </div>
      ))}
      {(a.branch !== "" || a.leases !== "" || pct !== null) && (
        <div className="ml-6 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-2xs text-muted-foreground">
          {a.branch !== "" && (
            <span className="flex items-center gap-1">
              <GitBranch className="size-3" />
              {a.branch}
            </span>
          )}
          {a.leases !== "" && (
            <span className="flex min-w-0 items-center gap-1" title="Files it claimed">
              <Lock className="size-3 shrink-0" />
              <span className="truncate">{a.leases}</span>
            </span>
          )}
          {pct !== null && (
            <span className="flex items-center gap-1.5" title={`${a.contextTokens} of ${a.contextWindow} tokens`}>
              <span className="h-1 w-12 overflow-hidden rounded-full bg-muted">
                <span className={cn("block h-full rounded-full", pct >= 85 ? "bg-warning" : "bg-primary")} style={{ width: `${Math.min(100, pct)}%` }} />
              </span>
              {pct}% context
            </span>
          )}
          {a.costUsd > 0 && <span>${a.costUsd.toFixed(2)}</span>}
        </div>
      )}
    </li>
  );
}

const STATUS_STYLE: Record<string, string> = {
  pending: "bg-muted text-muted-foreground",
  ready: "bg-info/12 text-info-foreground",
  running: "bg-info/12 text-info-foreground",
  review: "bg-[#a371f7]/14 text-[#a371f7]",
  blocked: "bg-warning/14 text-warning-foreground",
  done: "bg-success/12 text-success-foreground",
  failed: "bg-destructive/12 text-destructive-foreground",
  cancelled: "bg-muted text-muted-foreground",
};

function StatusPill({ status }: { status: string }) {
  return (
    <span className={cn("task-status shrink-0 rounded-full px-1.5 py-0.5 font-medium text-2xs", STATUS_STYLE[status] ?? "bg-muted")}>{status}</span>
  );
}

function TaskCard({
  t,
  entries,
  act,
}: {
  t: BoardTask;
  entries: BoardTask[];
  act: (task: BoardTask, action: string, text?: string) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [steering, setSteering] = useState(false);
  const [steer, setSteer] = useState("");
  const live = t.status === "running" || t.status === "review" || t.status === "blocked";
  const actions: ReactNode[] = [];
  if (live) {
    actions.push(
      <Button key="steer" size="xs" variant="ghost-muted" onClick={() => setSteering((v) => !v)}>
        <CornerDownRight /> Steer
      </Button>,
    );
  }
  if (t.status === "failed" || t.status === "blocked") {
    actions.push(
      <Button key="retry" size="xs" variant="ghost-muted" onClick={() => void act(t, "retry")}>
        <RotateCcw /> Retry
      </Button>,
    );
  }
  if (t.branch !== "" && !t.entry && (t.status === "done" || t.status === "failed" || t.status === "blocked")) {
    actions.push(
      <Button key="apply" size="xs" variant="outline" onClick={() => void act(t, "apply")}>
        <Check /> Apply
      </Button>,
      <Button key="discard" size="xs" variant="ghost-muted" onClick={() => void act(t, "discard")}>
        <Trash2 /> Discard
      </Button>,
    );
  }
  if (live || t.status === "pending" || t.status === "ready") {
    actions.push(
      <Button key="cancel" size="xs" variant="ghost-muted" className="hover:text-destructive-foreground" onClick={() => void act(t, "cancel")}>
        <X /> Cancel
      </Button>,
    );
  }
  return (
    <li className="board-task rounded-xl border bg-card/40" data-task={t.id}>
      <button type="button" className="flex w-full cursor-pointer items-start gap-2 px-3 py-2 text-left" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {open ? <ChevronDown className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" /> : <ChevronRight className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />}
        {t.race && <Flag className="mt-0.5 size-3.5 shrink-0 text-warning-foreground" aria-label="Race" />}
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex min-w-0 items-center gap-2">
            <span className="min-w-0 truncate font-medium text-sm">{t.title || t.id}</span>
            <StatusPill status={t.status} />
          </span>
          <span className="truncate text-2xs text-muted-foreground">
            {t.id}
            {t.owner !== "" ? ` · ${t.owner}` : ""}
            {t.tries > 1 ? ` · attempt ${Math.max(1, t.attempts)}/${t.tries}` : ""}
            {t.reviewer !== "" ? ` · reviewed by ${t.reviewer}` : ""}
            {t.deps.length > 0 ? ` · after ${t.deps.join(", ")}` : ""}
          </span>
        </span>
      </button>
      {open && (
        <div className="flex flex-col gap-2 border-t px-3 py-2 text-xs">
          {t.brief !== "" && <p className="whitespace-pre-wrap text-muted-foreground">{t.brief}</p>}
          {t.result !== "" && <p className="whitespace-pre-wrap">{t.result}</p>}
          {t.error !== "" && <p className="whitespace-pre-wrap text-destructive-foreground">{t.error}</p>}
          {t.branch !== "" && (
            <span className="flex items-center gap-1 text-muted-foreground">
              <GitBranch className="size-3" /> {t.branch}
            </span>
          )}
        </div>
      )}
      {entries.length > 0 && (
        <ul className="flex flex-col gap-1 border-t px-3 py-2">
          {entries.map((e) => (
            <li key={e.id} className="race-entry flex items-center gap-2 text-xs" data-task={e.id}>
              <Trophy className="size-3.5 shrink-0 text-muted-foreground/60" />
              <span className="shrink-0 font-medium">{e.owner || e.id}</span>
              <StatusPill status={e.status} />
              <span className="min-w-0 flex-1 truncate text-muted-foreground" title={e.files.join("\n")}>
                {e.stats}
              </span>
              {(e.status === "done" || e.status === "failed") && t.status !== "done" && (
                <Button size="xs" variant="outline" onClick={() => void act(e, "apply")}>
                  Keep
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      {steering && (
        <div className="flex items-center gap-2 border-t px-3 py-2">
          <input
            autoFocus
            aria-label={`Steer ${t.id}`}
            placeholder="Tell its agent what to do instead…"
            value={steer}
            className={cn(fieldClass, "h-8 py-1 text-xs")}
            onChange={(e) => setSteer(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && steer.trim() !== "") {
                void act(t, "steer", steer).then(() => {
                  setSteer("");
                  setSteering(false);
                });
              }
            }}
          />
        </div>
      )}
      {actions.length > 0 && <div className="flex flex-wrap justify-end gap-1 border-t px-2 py-1.5">{actions}</div>}
    </li>
  );
}

function RaceLauncher({ kinds, theme, onRace }: { kinds: string[]; theme: Theme | null; onRace: (text: string) => Promise<boolean> }) {
  const [brief, setBrief] = useState("");
  const [counts, setCounts] = useState<Record<string, number>>(() => (kinds[0] === undefined ? {} : { [kinds[0]]: 2 }));
  const total = Object.values(counts).reduce((n, c) => n + c, 0);
  const ok = brief.trim() !== "" && total >= 2 && total <= 6;
  return (
    <div className="race-launcher flex flex-col gap-2">
      <p className="text-muted-foreground text-xs leading-relaxed">
        Several agents do the same task, each on a worktree of its own; you compare what each changed and keep one. Needs a git
        repository.
      </p>
      <textarea
        aria-label="Race brief"
        placeholder="What should they all do?"
        rows={3}
        value={brief}
        className={cn(fieldClass, "resize-none text-xs")}
        onChange={(e) => setBrief(e.target.value)}
      />
      <div className="flex flex-wrap items-center gap-2">
        {kinds.map((k) => (
          <span key={k} className="flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-xs">
            <span className="font-mono" style={{ color: kindColour(k) }}>
              {kindMark(theme, k) || "●"}
            </span>
            {k}
            <button type="button" aria-label={`Fewer ${k}`} className="grid size-5 cursor-pointer place-items-center rounded hover:bg-accent" onClick={() => setCounts((c) => ({ ...c, [k]: Math.max(0, (c[k] ?? 0) - 1) }))}>
              <Minus className="size-3" />
            </button>
            <span className="w-3 text-center tabular-nums" aria-label={`${k} count`}>
              {counts[k] ?? 0}
            </span>
            <button type="button" aria-label={`More ${k}`} className="grid size-5 cursor-pointer place-items-center rounded hover:bg-accent" onClick={() => setCounts((c) => ({ ...c, [k]: Math.min(6, (c[k] ?? 0) + 1) }))}>
              <Plus className="size-3" />
            </button>
          </span>
        ))}
        <Button
          size="xs"
          className="ml-auto"
          disabled={!ok}
          onClick={() =>
            void onRace(raceMessage(counts, brief)).then((sent) => {
              if (sent) setBrief("");
            })
          }
        >
          <Flag /> Start race ({total})
        </Button>
      </div>
    </div>
  );
}

function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex min-h-6 items-center justify-between px-1">
        <h3 className="font-medium text-muted-foreground text-xs">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  );
}

export function AgentsPanel({
  api,
  sessionId,
  kinds,
  theme,
  onRace,
  onError,
  onInfo,
}: {
  api: ProjectApi;
  sessionId: string;
  busy?: boolean;
  kinds: string[];
  theme: Theme | null;
  onRace: (text: string) => Promise<boolean>;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
}) {
  // The project polls the board and agents once for the whole window.
  const room = useCrew(api, sessionId);
  const board: Board | null = room?.board ?? null;
  const crew: Crew | null = room?.crew ?? null;
  const now = room?.at ?? Date.now();
  const [racing, setRacing] = useState(false);
  const load = useCallback(() => refreshCrew(api, sessionId), [api, sessionId]);

  const act = async (task: BoardTask, action: string, text = "") => {
    const r = await api.taskAction(sessionId, task.id, action, text);
    if (typeof r === "string") onError(r);
    else if (action === "apply") onInfo(r.length === 0 ? `Applied ${task.id}.` : `Applied ${r.length} ${r.length === 1 ? "file" : "files"} from ${task.id}.`);
    await load();
  };

  const toggle = async () => {
    if (board === null) return;
    const err = await api.setOrchestrate(sessionId, !board.orchestrate);
    if (err !== null) onError(err);
    await load();
  };

  const running = (name: string) => crew?.running.filter((t) => t.agent === name) ?? [];
  const rows = board === null ? [] : boardRows(board);

  return (
    <section aria-label="Agents" className="flex flex-col gap-6 overflow-y-auto p-3">
      <Section title="Agents">
        {crew === null ? (
          <p className="flex items-center gap-2 px-1 text-muted-foreground text-xs">
            <Loader2 className="size-3 animate-spin" /> Loading…
          </p>
        ) : crew.agents.length === 0 ? (
          <p className="px-1 text-muted-foreground text-xs">No agents yet. The first message starts one.</p>
        ) : (
          <ul className="crew divide-y divide-border/50 rounded-xl border bg-card/40 px-3">
            {crew.agents.map((a) => (
              <AgentRow key={a.name} a={a} running={running(a.name)} theme={theme} now={now} />
            ))}
          </ul>
        )}
      </Section>
      <Section
        title="Tasks"
        action={
          board !== null && (
            <label className="flex cursor-pointer items-center gap-2 text-muted-foreground text-xs" title="Let the scheduler run ready tasks and agents start subagents">
              Orchestrate
              <button
                type="button"
                role="switch"
                aria-checked={board.orchestrate}
                aria-label="Orchestrate"
                onClick={() => void toggle()}
                className={cn("relative inline-flex h-4 w-7 shrink-0 items-center rounded-full transition-colors", board.orchestrate ? "bg-primary" : "bg-input")}
              >
                <span className={cn("inline-block size-3 rounded-full bg-white transition-transform", board.orchestrate ? "translate-x-3.5" : "translate-x-0.5")} />
              </button>
            </label>
          )
        }
      >
        {rows.length === 0 ? (
          <p className="px-1 text-muted-foreground text-xs leading-relaxed">
            No tasks. Agents split work into tasks and subagents when orchestration is on; a race shows here too.
          </p>
        ) : (
          <ul className="flex flex-col gap-2">
            {rows.map((r) => (
              <TaskCard key={r.task.id} t={r.task} entries={r.entries} act={act} />
            ))}
          </ul>
        )}
      </Section>
      <Section
        title="Race agents on a task"
        action={
          <Button size="xs" variant="ghost-muted" aria-expanded={racing} onClick={() => setRacing((v) => !v)}>
            {racing ? <ChevronDown /> : <ChevronRight />}
            {racing ? "Hide" : "New race"}
          </Button>
        }
      >
        {racing && <RaceLauncher kinds={kinds} theme={theme} onRace={onRace} />}
      </Section>
    </section>
  );
}
