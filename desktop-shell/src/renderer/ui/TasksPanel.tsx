import type { ChangedFile, QueuedItem, Theme } from "@web/protocol";
import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronDown,
  ChevronRight,
  CornerDownLeft,
  CornerDownRight,
  FileText,
  Flag,
  GitBranch,
  ListChecks,
  Loader2,
  Pencil,
  RotateCcw,
  Trash2,
  Trophy,
  X,
} from "./icons";
import { type ReactNode, useCallback, useState } from "react";
import type { DeckClient } from "../client";
import type { ProfileRef } from "../completion";
import {
  asks,
  type Board,
  type BoardTask,
  boardRows,
  type Decision,
  decisionText,
  type PreviewFile,
  taskPreview,
} from "../crew";
import { refreshCrew, useRoomCrew } from "../crewState";
import { cn } from "../lib/cn";
import { useLimits } from "../limitsState";
import { schedules } from "../orchestration";
import type { ProjectApi } from "../project";
import { moveQueued, queuedText } from "../queue";
import type { RaceDraft } from "../race";
import type { PickerSource } from "./AgentPicker";
import { DiffView } from "./DiffView";
import { ScheduledList } from "./Limits";
import { RaceBuilder } from "./RaceBuilder";
import { Button } from "./kit/button";
import { PathTail } from "./kit/path";
import { CARD } from "./kit/card";
import { EmptyState } from "./kit/empty";
import { Dialog, fieldClass } from "./kit/dialog";
import { TaskMark } from "./graph/Nodes";
import { RoomLevel } from "./LevelPicker";

// The Tasks tab of a room: what waits for a busy agent, the task board the
// agents split the work into (races and their entries, reviews), the
// orchestration level (the composer's picker too), and the race builder /race opens too (RaceBuilder).

const STATUS_TEXT: Record<string, string> = {
  running: "text-foreground",
  blocked: "text-warning-foreground",
  done: "text-success-foreground",
  failed: "text-destructive-foreground",
};

// A task's state: the Work Graph's small mark, and the word.
function Status({ status }: { status: string }) {
  return (
    <span className="flex shrink-0 items-center gap-1">
      <TaskMark status={status} />
      <span className={cn("task-status text-2xs", STATUS_TEXT[status] ?? "text-muted-foreground")}>{status}</span>
    </span>
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

function Queue({
  c,
  sessionId,
  items,
  onEdit,
}: {
  c: DeckClient;
  sessionId: string;
  items: QueuedItem[];
  // Puts an item's text in the composer.
  onEdit?: (text: string) => void;
}) {
  const [moving, setMoving] = useState(false);
  if (items.length === 0) return <p className="px-1 text-muted-foreground text-xs">Nothing waits for a busy agent.</p>;
  const move = async (i: number, dir: -1 | 1) => {
    const plan = moveQueued(items, i, dir);
    if (plan === null) return;
    setMoving(true);
    for (const q of plan.cancel) await c.cancelQueued(sessionId, q.id);
    for (const q of plan.resend) await c.send(sessionId, queuedText(q), []);
    setMoving(false);
  };
  return (
    <ul aria-label="Queue" className="flex flex-col gap-2">
      {items.map((q, i) => (
        <li key={q.id} className={cn("queued flex flex-col gap-2 p-3", CARD)}>
          <div className="flex items-center gap-1 text-muted-foreground text-xs">
            <span className="flex-1">
              Waiting for <span className="approval-agent font-medium text-foreground">{q.agent}</span>
            </span>
            <Button size="icon-xs" variant="ghost-muted" aria-label="Move up" title="Move up" disabled={moving || i === 0} onClick={() => void move(i, -1)}>
              <ArrowUp />
            </Button>
            <Button
              size="icon-xs"
              variant="ghost-muted"
              aria-label="Move down"
              title="Move down"
              disabled={moving || i === items.length - 1}
              onClick={() => void move(i, 1)}
            >
              <ArrowDown />
            </Button>
          </div>
          <p className="whitespace-pre-wrap text-sm">{q.text}</p>
          <div className="approval-actions flex justify-end gap-1.5">
            {onEdit !== undefined && (
              <Button
                size="xs"
                variant="ghost-muted"
                onClick={() => {
                  onEdit(queuedText(q));
                  void c.cancelQueued(sessionId, q.id);
                }}
              >
                <Pencil />
                Edit
              </Button>
            )}
            <Button size="xs" variant="ghost-muted" onClick={() => void c.cancelQueued(sessionId, q.id)}>
              Cancel
            </Button>
            <Button size="xs" variant="outline" onClick={() => void c.steer(sessionId, q.id)}>
              <CornerDownLeft />
              Send now
            </Button>
          </div>
        </li>
      ))}
    </ul>
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
    <li className={cn("board-task", CARD)} data-task={t.id}>
      <button
        type="button"
        className="flex w-full cursor-pointer items-start gap-2 rounded-xl px-3 py-2.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        {open ? <ChevronDown className="mt-0.5 size-3.5 shrink-0 text-faint" /> : <ChevronRight className="mt-0.5 size-3.5 shrink-0 text-faint" />}
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex min-w-0 items-center gap-2">
            {t.race && <Flag className="size-3.5 shrink-0 text-muted-foreground" aria-label="Race" />}
            <span className="min-w-0 flex-1 truncate font-medium text-sm">{t.title || t.id}</span>
            <Status status={t.status} />
          </span>
          <span className="truncate text-2xs text-faint">
            <span className="font-mono">{t.id}</span>
            {t.owner !== "" ? ` · ${t.owner}` : ""}
            {t.tries > 1 ? ` · attempt ${Math.max(1, t.attempts)}/${t.tries}` : ""}
            {t.reviewer !== "" ? ` · reviewed by ${t.reviewer}` : ""}
            {t.deps.length > 0 ? ` · after ${t.deps.join(", ")}` : ""}
          </span>
        </span>
      </button>
      {open && (
        <div className="flex flex-col gap-2 border-hairline border-t px-3 py-2.5 text-xs">
          {t.brief !== "" && <p className="whitespace-pre-wrap text-muted-foreground">{t.brief}</p>}
          {t.result !== "" && <p className="whitespace-pre-wrap">{t.result}</p>}
          {t.error !== "" && <p className="whitespace-pre-wrap text-destructive-foreground">{t.error}</p>}
          {t.branch !== "" && (
            <span className="flex items-center gap-1 font-mono text-muted-foreground">
              <GitBranch className="size-3" /> {t.branch}
            </span>
          )}
        </div>
      )}
      {entries.length > 0 && (
        <ul className="flex flex-col gap-1.5 border-hairline border-t px-3 py-2.5">
          {entries.map((e) => (
            <li key={e.id} className="race-entry flex items-center gap-2 text-xs" data-task={e.id}>
              <Trophy className="size-3.5 shrink-0 text-faint" />
              <span className="shrink-0 font-medium">{e.owner || e.id}</span>
              <Status status={e.status} />
              <span className="min-w-0 flex-1 truncate font-mono text-2xs text-muted-foreground" title={e.files.join("\n")}>
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
        <div className="flex items-center gap-2 border-hairline border-t px-3 py-2">
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
      {actions.length > 0 && <div className="flex flex-wrap justify-end gap-1 border-hairline border-t px-2 py-1.5">{actions}</div>}
    </li>
  );
}

// Before an apply or a discard: the files it touches, with their diff when
// the work tree holds it, and the question.
function Decide({
  task,
  action,
  files,
  onCancel,
  onGo,
}: {
  task: BoardTask;
  action: Decision;
  files: PreviewFile[] | null;
  onCancel: () => void;
  onGo: () => void;
}) {
  const text = decisionText(action, task, files?.length ?? task.files.length);
  return (
    <Dialog
      title={text.title}
      description={text.description}
      onClose={onCancel}
      className="w-[640px]"
      footer={
        <>
          <Button variant="ghost-muted" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant={action === "discard" ? "destructive-outline" : "default"} disabled={files === null} onClick={onGo}>
            {text.button}
          </Button>
        </>
      }
    >
      {files === null ? (
        <p className="flex items-center gap-2 text-muted-foreground text-xs">
          <Loader2 className="size-3 animate-spin" /> Reading the changes…
        </p>
      ) : files.length === 0 ? (
        <p className="text-muted-foreground text-xs">The task names no files.</p>
      ) : (
        <ul className="decide-files flex max-h-[50vh] flex-col gap-2 overflow-y-auto" aria-label="Files">
          {files.map((f) => (
            <li key={f.path} className="overflow-hidden rounded-lg border border-hairline">
              <div className="flex items-center gap-2 px-3 py-1.5 text-xs">
                <FileText className="size-3.5 shrink-0 text-muted-foreground" />
                <PathTail path={f.path} className="flex-1" />
                {f.diff !== null && (
                  <span className="font-mono">
                    <span className="text-diff-addition-foreground">+{f.added}</span>{" "}
                    <span className="text-diff-deletion-foreground">−{f.removed}</span>
                  </span>
                )}
              </div>
              {f.diff !== null && f.diff !== "" && <DiffView diff={f.diff} numbered />}
            </li>
          ))}
        </ul>
      )}
    </Dialog>
  );
}

export function TasksPanel({
  c,
  api,
  sessionId,
  queue,
  kinds,
  named,
  picker,
  theme,
  branch,
  canJudge,
  onRace,
  onEditQueued,
  onError,
  onInfo,
}: {
  c: DeckClient;
  // A paired Deck has no board: the tab shows its queue alone.
  api: ProjectApi | undefined;
  sessionId: string;
  busy?: boolean;
  queue: QueuedItem[];
  kinds: string[];
  // The named agents a race may enter, and the pickers' catalog.
  named: readonly ProfileRef[] | undefined;
  picker: PickerSource;
  theme: Theme | null;
  // The work tree's branch, to tell whether Changes holds a task's diff.
  branch: string | null;
  // The Deck can have one more agent rank a race's entries.
  canJudge: boolean;
  onRace: (d: RaceDraft) => Promise<boolean>;
  // Puts a queued message back in the composer.
  onEditQueued?: (text: string) => void;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
}) {
  // The project polls the board once for the whole window (crewState.ts).
  const board: Board | null = useRoomCrew(api, sessionId)?.board ?? null;
  const [racing, setRacing] = useState(false);
  const [deciding, setDeciding] = useState<{ task: BoardTask; action: Decision; files: PreviewFile[] | null } | null>(null);

  const load = useCallback(async () => {
    if (api !== undefined) await refreshCrew(api, sessionId);
  }, [api, sessionId]);

  const run = async (task: BoardTask, action: string, text = "") => {
    if (api === undefined) return;
    const r = await api.taskAction(sessionId, task.id, action, text);
    if (typeof r === "string") onError(r);
    else if (action === "apply") onInfo(r.length === 0 ? `Applied ${task.id}.` : `Applied ${r.length} ${r.length === 1 ? "file" : "files"} from ${task.id}.`);
    await load();
  };

  const act = async (task: BoardTask, action: string, text = "") => {
    if (!asks(action)) return run(task, action, text);
    setDeciding({ task, action, files: null });
    const onBranch = branch !== null && task.branch !== "" && task.branch === branch;
    const changes: ChangedFile[] | null = onBranch ? await c.changes(sessionId) : null;
    setDeciding((d) => (d !== null && d.task.id === task.id ? { ...d, files: taskPreview(task, branch, changes) } : d));
  };

  const rows = board === null ? [] : boardRows(board);
  // Messages the person scheduled for a plan limit's reset (limits.ts).
  const scheduled = useLimits(api).data?.scheduled.some((s) => s.sessionId === sessionId) === true;

  return (
    <section aria-label="Tasks" className="flex flex-col gap-6 overflow-y-auto px-3 pt-1 pb-3">
      {(queue.length > 0 || api === undefined) && (
        <Section title="Queue">
          <Queue c={c} sessionId={sessionId} items={queue} {...(onEditQueued === undefined ? {} : { onEdit: onEditQueued })} />
        </Section>
      )}
      {scheduled && (
        <Section title="Scheduled">
          <ScheduledList api={api} sessionId={sessionId} onError={onError} />
        </Section>
      )}
      {api === undefined ? (
        <p className="px-1 text-muted-foreground text-xs">The task board shows for a project on this computer.</p>
      ) : (
        <>
          <Section
            title="Tasks"
            action={board !== null && <RoomLevel api={api} sessionId={sessionId} onError={onError} side="bottom" align="end" />}
          >
            {board === null ? (
              <p className="flex items-center gap-2 px-1 text-muted-foreground text-xs">
                <Loader2 className="size-3 animate-spin" /> Loading…
              </p>
            ) : rows.length === 0 ? (
              <EmptyState
                icon={ListChecks}
                title="No tasks."
                hint={
                  schedules(board.level)
                    ? "Ask for a plan: agents post their tasks here, and ready ones run by themselves."
                    : "Agents post tasks here. At orchestration Auto or Full, ready ones run by themselves."
                }
                className="py-6"
              />
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
            {racing && (
              <RaceBuilder
                kinds={kinds}
                named={named}
                picker={picker}
                theme={theme}
                canJudge={canJudge}
                side="bottom"
                className="rounded-[14px] border border-hairline p-2.5"
                onStart={onRace}
              />
            )}
          </Section>
        </>
      )}
      {deciding !== null && (
        <Decide
          task={deciding.task}
          action={deciding.action}
          files={deciding.files}
          onCancel={() => setDeciding(null)}
          onGo={() => {
            const d = deciding;
            setDeciding(null);
            void run(d.task, d.action);
          }}
        />
      )}
    </section>
  );
}
