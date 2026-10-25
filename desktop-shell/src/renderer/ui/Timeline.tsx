import type { AgentLine, ChangedFile, Message, Mode, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type Chunk, groupParts, isNearBottom } from "@web/transcript";
import { toMillis } from "@web/time";
import {
  ArrowDown,
  Brain,
  Check,
  ChevronDown,
  ChevronRight,
  CircleAlert,
  Copy,
  Eye,
  FileDiff,
  FilePen,
  FileText,
  Globe,
  ImageIcon,
  type LucideIcon,
  Pencil,
  Play,
  RotateCcw,
  Search,
  SquareTerminal,
  Wrench,
} from "lucide-react";
import { createContext, memo, type ReactElement, type ReactNode, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { agentColour, agentKind } from "../agents";
import type { RoomNotice } from "../attention";
import { shortId } from "../lanes";
import { cn } from "../lib/cn";
import { lastReplyId, messageText } from "../messages";
import { detectPlan, findPlan } from "../plans";
import { formatClock, usePrefs } from "../prefs";
import type { ImageBytes } from "../rpcMap";
import { capLines, formatDuration, splitWork } from "../work";
import { DiffView } from "./DiffView";
import { Button } from "./kit/button";
import { Dialog } from "./kit/dialog";
import { VIRTUAL_AFTER, VirtualList } from "./kit/virtual";
import { Markdown } from "./Markdown";
import { type PlanActions, PlanCard, wasApproved } from "./PlanCard";

// A row of the timeline: a message, or the shell's own note in the room
// (a revert), in time order.
type Row = Message | RoomNotice;

const rowKey = (r: Row) => ("role" in r ? r.id : `note:${r.at}:${r.text}`);

function withNotes(messages: Message[], notes: readonly RoomNotice[]): Row[] {
  if (notes.length === 0) return messages;
  const out: Row[] = [];
  let i = 0;
  for (const m of messages) {
    const at = toMillis(m.created_at);
    while (i < notes.length && notes[i]!.at < at) out.push(notes[i++]!);
    out.push(m);
  }
  return [...out, ...notes.slice(i)];
}

// What the open room lets a person do from its timeline. A read-only room
// gets none of it.
export interface RoomActions extends PlanActions {
  editMessage(m: Message): void;
  retry(replyId: string): void;
  continueRun(): void;
}

type TextChunk = Extract<Chunk, { k: "text" }>;

const COMMAND = /bash|shell|exec|command|terminal|run/;

// An icon for a tool by its name, as agents name them.
function toolIcon(name: string): LucideIcon {
  const n = name.toLowerCase();
  if (COMMAND.test(n)) return SquareTerminal;
  if (/edit|write|patch|apply|create|multiedit/.test(n)) return FilePen;
  if (/grep|glob|search|find|ls/.test(n)) return Search;
  if (/read|view|cat|open/.test(n)) return Eye;
  if (/fetch|web|http|browse/.test(n)) return Globe;
  return Wrench;
}

// A tool's output shows this many lines until the person asks for all.
const OUTPUT_LINES = 40;

function ToolOutput({ output }: { output: string }) {
  const [all, setAll] = useState(false);
  const capped = useMemo(() => capLines(output, OUTPUT_LINES), [output]);
  const cut = !all && capped.cut;
  return (
    <div className="mt-1 mb-2 overflow-hidden rounded-lg border border-hairline bg-code">
      <pre className="tool-output max-h-72 overflow-auto px-3 py-2 font-mono text-code text-foreground/85">{cut ? capped.text : output}</pre>
      {cut && (
        <button
          type="button"
          className="tool-more flex w-full cursor-pointer items-center gap-1.5 border-hairline border-t px-3 py-1 text-left font-mono text-2xs text-muted-foreground outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          onClick={() => setAll(true)}
        >
          Show all <span className="tabular-nums">({capped.count} lines)</span>
        </button>
      )}
    </div>
  );
}

// One line of the work log: thinking or a tool call, folded to a row.
function WorkRow({ c, running }: { c: Chunk; running: boolean }) {
  if (c.k === "reasoning") {
    return (
      <details className="fold reasoning group/fold">
        <summary className="flex min-w-0 cursor-pointer list-none items-center gap-2 rounded-md py-1 text-muted-foreground text-sm outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
          <Brain className="size-3.5 shrink-0" />
          <span>Thought</span>
          <ChevronRight className="size-3 shrink-0 transition-transform group-open/fold:rotate-90" />
        </summary>
        <p className="reasoning-text mt-1 mb-2 ml-1.5 whitespace-pre-wrap border-hairline-strong border-l pl-4 text-muted-foreground text-xs leading-relaxed">
          {c.text}
        </p>
      </details>
    );
  }
  if (c.k !== "tool") return null;
  const name = c.call?.name ?? c.result?.name ?? "tool";
  const failed = c.result?.is_error === true;
  const pending = c.result === null && running;
  const Icon = failed ? CircleAlert : toolIcon(name);
  const output = c.result?.output ?? "";
  const diff = c.result?.diff ?? "";
  const summary = c.call?.summary ?? "";
  // A command reads as a shell line: a muted $, then the command.
  const command = COMMAND.test(name.toLowerCase()) && summary !== "";
  return (
    <details className={cn("fold tool group/fold", failed && "failed")}>
      <summary className="flex min-w-0 cursor-pointer list-none items-center gap-2 rounded-md py-0.5 font-mono text-code outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
        <Icon className={cn("size-3.5 shrink-0", failed ? "text-destructive-foreground" : "text-faint")} />
        {command ? (
          <>
            <span className="tool-name sr-only">{name}</span>
            <span aria-hidden className="shrink-0 text-faint">
              $
            </span>
            <span className={cn("tool-summary min-w-0 flex-1 truncate text-foreground/80", pending && "shimmer")}>{summary}</span>
          </>
        ) : (
          <>
            <span className={cn("tool-name shrink-0 text-foreground/80", pending && "shimmer")}>{name}</span>
            <span className="tool-summary min-w-0 flex-1 truncate text-muted-foreground">{summary}</span>
          </>
        )}
        <span className="tool-state sr-only">{pending ? "running" : failed ? "failed" : "done"}</span>
        {(output !== "" || diff !== "") && (
          <ChevronDown className="size-3 shrink-0 text-faint transition-transform group-open/fold:rotate-180" />
        )}
      </summary>
      {output !== "" && <ToolOutput output={output} />}
      {diff !== "" && (
        <div className="mt-1 mb-2 overflow-hidden rounded-lg border border-hairline">
          <DiffView diff={diff} truncated={c.result?.diff_truncated} />
        </div>
      )}
    </details>
  );
}

// How the timeline reads an image part's bytes; without it, image parts
// show as their names.
export type ImageReader = (messageId: string, index: number) => Promise<ImageBytes | null>;
const Images = createContext<ImageReader | null>(null);

type ImageChunk = Extract<Chunk, { k: "image" }>;

// Each image chunk's message and its index among that message's image parts.
const imageRefs = new WeakMap<Chunk, { mid: string; index: number }>();

// A message's parts as chunks, with the place of each image noted.
function chunksOf(m: Message): Chunk[] {
  const chunks = groupParts(m.parts);
  let n = 0;
  for (const c of chunks) if (c.k === "image") imageRefs.set(c, { mid: m.id, index: n++ });
  return chunks;
}

// An image part as a thumbnail that opens larger, or as chip until (or
// unless) its bytes arrive. Its blob: URL lives as long as it shows.
function ImagePart({ c, chip }: { c: ImageChunk; chip: ReactElement }) {
  const read = useContext(Images);
  const ref = imageRefs.get(c);
  const mid = ref?.mid;
  const index = ref?.index;
  const [url, setUrl] = useState<string | null>(null);
  const [big, setBig] = useState(false);
  useEffect(() => {
    if (read === null || mid === undefined || index === undefined) return;
    let live = true;
    let made: string | null = null;
    void read(mid, index).then((img) => {
      if (!live || img === null) return;
      made = URL.createObjectURL(new Blob([img.data as Uint8Array<ArrayBuffer>], { type: img.mime }));
      setUrl(made);
    });
    return () => {
      live = false;
      if (made !== null) URL.revokeObjectURL(made);
    };
  }, [read, mid, index]);
  if (url === null) return chip;
  return (
    <>
      <button
        type="button"
        className="image-part w-fit cursor-zoom-in rounded-[12px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Open ${c.name}`}
        title={c.name}
        onClick={() => setBig(true)}
      >
        <img src={url} alt={c.name} className="block max-h-60 max-w-[240px] rounded-[12px] border border-hairline" />
      </button>
      {big &&
        createPortal(
          <Dialog title={c.name} onClose={() => setBig(false)} className="image-lightbox w-auto max-w-[min(90vw,1200px)]">
            <img src={url} alt={c.name} className="mb-3 max-h-[70vh] max-w-full rounded-[12px] border border-hairline object-contain" />
          </Dialog>,
          document.body,
        )}
    </>
  );
}

// Scrolls a message into view and lights its row once.
function reveal(el: HTMLElement): void {
  el.scrollIntoView({ block: "center" });
  if (document.documentElement.dataset["motion"] === "reduced") return;
  const selected = getComputedStyle(el).getPropertyValue("--selected").trim();
  if (selected !== "") el.animate([{ backgroundColor: selected }, { backgroundColor: "transparent" }], { duration: 1200, easing: "ease-out" });
}

function Prose({ chunks, text }: { chunks: Chunk[]; text?: (c: TextChunk, key: number) => ReactNode }) {
  return (
    <>
      {chunks.map((c, i) =>
        c.k === "text" ? (
          text !== undefined ? (
            text(c, i)
          ) : (
            <Markdown key={i} text={c.text} />
          )
        ) : c.k === "image" ? (
          <ImagePart
            key={i}
            c={c}
            chip={
              <span className="chip inline-flex w-fit items-center gap-1.5 rounded-md bg-secondary px-2 py-1 text-xs">
                <ImageIcon className="size-3.5" />
                {c.name}
              </span>
            }
          />
        ) : (
          <WorkRow key={i} c={c} running={false} />
        ),
      )}
    </>
  );
}

// Copy, and the room's verbs for one message, shown on hover.
function MessageActions({ m, actions, last }: { m: Message; actions: RoomActions; last: boolean }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  const icon = "text-faint hover:text-foreground";
  const reply = m.role === "assistant" && last && m.finished;
  return (
    <span className="msg-actions flex items-center gap-0.5 font-sans opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
      <Button
        variant="ghost-muted"
        size="icon-xs"
        className={icon}
        aria-label={copied ? "Copied" : "Copy"}
        title="Copy"
        onClick={() => {
          void navigator.clipboard
            .writeText(messageText(m))
            .then(() => setCopied(true))
            .catch(() => {});
        }}
      >
        {copied ? <Check /> : <Copy />}
      </Button>
      {m.role === "user" && m.editable !== false && (
        <Button
          variant="ghost-muted"
          size="icon-xs"
          className={icon}
          aria-label="Edit and resend"
          title="Edit and resend"
          onClick={() => actions.editMessage(m)}
        >
          <Pencil />
        </Button>
      )}
      {reply && (
        <>
          <Button
            variant="ghost-muted"
            size="icon-xs"
            className={icon}
            aria-label="Retry"
            title="Retry: send your message again"
            onClick={() => actions.retry(m.id)}
          >
            <RotateCcw />
          </Button>
          <Button variant="ghost-muted" size="icon-xs" className={icon} aria-label="Continue" title="Continue" onClick={() => actions.continueRun()}>
            <Play />
          </Button>
        </>
      )}
    </span>
  );
}

function UserMessage({ m, actions }: { m: Message; actions: RoomActions | null }) {
  const prefs = usePrefs();
  const chunks = chunksOf(m);
  return (
    <article className="msg msg-user group flex animate-enter flex-col gap-1" data-role="user" data-mid={m.id}>
      {/* A played card: the person's turn on a surface, with a faint rail. */}
      <div className="relative overflow-hidden rounded-lg border border-hairline bg-surface-1 py-3 pr-4 pl-5 text-message-foreground inset-shadow-hl before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-faint">
        <div className="flex flex-col gap-2">
          {chunks.map((c, i) =>
            c.k === "text" ? (
              <p key={i} className="user-text whitespace-pre-wrap break-words text-prose">
                {c.text}
              </p>
            ) : c.k === "image" ? (
              <ImagePart
                key={i}
                c={c}
                chip={
                  <span className="chip inline-flex w-fit items-center gap-1.5 rounded-md border border-hairline bg-background/60 px-2 py-1 text-xs">
                    <ImageIcon className="size-3.5" />
                    {c.name}
                  </span>
                }
              />
            ) : null,
          )}
        </div>
      </div>
      <header className="msg-head flex items-center gap-2 ps-1 font-mono text-2xs text-faint opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
        <span className="msg-who sr-only">You</span>
        {m.edited === true && <span>edited</span>}
        <time>{formatClock(m.created_at, prefs.clock)}</time>
        {actions !== null && <MessageActions m={m} actions={actions} last={false} />}
      </header>
    </article>
  );
}

function AgentMessage({
  m,
  roster,
  theme,
  mode,
  actions,
  last,
}: {
  m: Message;
  roster: AgentLine[];
  theme: Theme | null;
  mode: Mode | null;
  actions: RoomActions | null;
  last: boolean;
}) {
  const prefs = usePrefs();
  const agent = m.agent ?? "agent";
  const kind = agentKind(agent, roster);
  const colour = agentColour(agent, kind);
  const chunks = chunksOf(m);
  const { work, answer } = splitWork(chunks);
  const [open, setOpen] = useState(false);
  const folds = m.finished && prefs.foldWork && work.length > 0;
  // A finished reply in plan mode whose last text holds a step list is a
  // plan: that text renders as a card.
  const planChunk = m.finished ? [...answer].reverse().find((c): c is TextChunk => c.k === "text") : undefined;
  const plan = useMemo(() => {
    if (planChunk === undefined) return null;
    return detectPlan(planChunk.text, { mode }) ?? (wasApproved(m.id) ? findPlan(planChunk.text) : null);
  }, [planChunk, mode, m.id]);
  const text = (c: TextChunk, key: number): ReactNode =>
    plan !== null && c === planChunk ? (
      <div key={key} className="flex flex-col gap-3">
        {plan.before !== "" && <Markdown text={plan.before} />}
        <PlanCard
          id={m.id}
          plan={plan}
          agent={agent}
          colour={colour}
          index={`${kindMark(theme, kind) || "●"} ${shortId(m.session_id)}`}
          actions={last ? actions : null}
        />
        {plan.after !== "" && <Markdown text={plan.after} />}
      </div>
    ) : (
      <Markdown key={key} text={c.text} />
    );
  const ms = toMillis(m.updated_at) - toMillis(m.created_at);
  const took = ms >= 1000 ? `Worked for ${formatDuration(ms)}` : "Worked";
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    if (m.finished) return;
    const t = setInterval(() => setTick(Date.now()), 1000);
    return () => clearInterval(t);
  }, [m.finished]);
  return (
    <article className="msg msg-agent group flex min-w-0 animate-enter flex-col gap-1 px-1" data-role="assistant" data-agent={agent} data-mid={m.id}>
      {/* The corner index: the agent's glyph and id in its hue, like the
          rank in the corner of a playing card. */}
      <header className="msg-head flex items-center gap-2 font-mono text-2xs">
        <span className="msg-index inline-flex items-center gap-1.5" style={{ color: colour }}>
          <span className="msg-mark">{kindMark(theme, kind) || "●"}</span>
          <span className="msg-who">{agent}</span>
        </span>
        <time className="text-faint opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          {formatClock(m.created_at, prefs.clock)}
        </time>
      </header>
      {folds ? (
        <>
          <div className="flex items-center gap-1 border-hairline border-b pt-1 pb-2">
            <button
              type="button"
              className="worked -ml-1 flex cursor-pointer select-none items-center gap-1 rounded-md px-1 text-muted-foreground text-sm outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              aria-expanded={open}
              onClick={() => setOpen((o) => !o)}
            >
              {took}
              {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
            </button>
          </div>
          {open && (
            <div className="flex flex-col gap-0.5 border-hairline border-b pb-2">
              {work.map((c, i) =>
                c.k === "text" ? (
                  <div key={i} className="text-muted-foreground [&_.md]:text-sm">
                    <Markdown text={c.text} />
                  </div>
                ) : (
                  <WorkRow key={i} c={c} running={false} />
                ),
              )}
            </div>
          )}
          <div className="mt-2 flex flex-col gap-3 text-foreground text-prose">
            <Prose chunks={answer} text={text} />
          </div>
        </>
      ) : (
        <div className="flex flex-col gap-2 text-foreground text-prose">
          {chunks.map((c, i) =>
            c.k === "text" ? (
              text(c, i)
            ) : c.k === "image" ? (
              <Prose key={i} chunks={[c]} />
            ) : (
              <WorkRow key={i} c={c} running={!m.finished} />
            ),
          )}
        </div>
      )}
      {!m.finished && (
        <div className="streaming flex items-center gap-2 pt-1 text-sm" aria-label="writing">
          <span aria-hidden className="size-1.5 shrink-0 animate-deck-pulse rounded-full bg-signal" />
          <span className="shimmer">Working for {formatDuration(tick - toMillis(m.created_at))}</span>
        </div>
      )}
      {m.finished && actions !== null && (
        <footer className="-ml-1 flex items-center">
          <MessageActions m={m} actions={actions} last={last} />
        </footer>
      )}
    </article>
  );
}

// A message renders again only when it changes: a token re-renders the row
// it lands in, not the whole timeline.
export const MessageView = memo(function MessageView({
  m,
  roster,
  theme,
  mode = null,
  actions = null,
  last = false,
}: {
  m: Message;
  roster: AgentLine[];
  theme: Theme | null;
  mode?: Mode | null;
  actions?: RoomActions | null;
  last?: boolean;
}) {
  if (m.role === "user") return <UserMessage m={m} actions={actions} />;
  if (m.role === "system") {
    return (
      <article className="msg msg-system mx-auto max-w-[90%] text-center text-muted-foreground text-xs" data-role="system" data-mid={m.id}>
        <Prose chunks={chunksOf(m)} />
      </article>
    );
  }
  return <AgentMessage m={m} roster={roster} theme={theme} mode={mode} actions={actions} last={last} />;
});

// "N changed files +a −r … Open diff", under the last reply, as T3 Code
// shows a run's changes.
export function ChangedFilesCard({ files, onOpen }: { files: ChangedFile[]; onOpen: (path?: string) => void }) {
  const [open, setOpen] = useState(false);
  const added = files.reduce((n, f) => n + f.added, 0);
  const removed = files.reduce((n, f) => n + f.removed, 0);
  return (
    <div className="changed-files rounded-lg border border-hairline bg-surface-1 inset-shadow-hl" data-changed-files="">
      <div className="flex items-center justify-between gap-2 px-3 py-2">
        <button
          type="button"
          className="flex min-w-0 cursor-pointer items-center gap-x-3 rounded-md font-medium text-foreground text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          {open ? <ChevronDown className="size-3.5 text-muted-foreground" /> : <ChevronRight className="size-3.5 text-muted-foreground" />}
          <span>
            {files.length} changed file{files.length === 1 ? "" : "s"}
          </span>
          <span className="font-mono">
            <span className="text-diff-addition-foreground">+{added}</span>{" "}
            <span className="text-diff-deletion-foreground">−{removed}</span>
          </span>
          <span className="font-normal text-muted-foreground">{open ? "Hide files" : "Show files"}</span>
        </button>
        <Button size="xs" variant="outline" aria-label="Open diff" onClick={() => onOpen(files[0]?.path)}>
          <FileDiff className="size-3" />
          Open diff
        </Button>
      </div>
      {open && (
        <ul className="flex flex-col gap-px px-1.5 pb-1.5">
          {files.map((f) => (
            <li key={f.path}>
              <button
                type="button"
                className="flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-left text-xs outline-none hover:bg-hover focus-visible:bg-selected"
                onClick={() => onOpen(f.path)}
              >
                <FileText className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate font-mono text-code">{f.path}</span>
                <span className="font-mono text-diff-addition-foreground">+{f.added}</span>
                <span className="font-mono text-diff-deletion-foreground">−{f.removed}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function Timeline({
  messages,
  roster,
  theme,
  more,
  onOlder,
  changes,
  onOpenDiff,
  mode = null,
  readOnly = false,
  actions,
  notes = [],
  readImage,
  focusMessage = null,
  onFocused,
}: {
  messages: Message[];
  roster: AgentLine[];
  theme: Theme | null;
  more: boolean;
  onOlder: () => Promise<unknown>;
  changes: ChangedFile[] | null;
  onOpenDiff: (path?: string) => void;
  // The room's mode, for plan cards.
  mode?: Mode | null;
  readOnly?: boolean;
  actions?: RoomActions;
  // The shell's own notes in the room, oldest first.
  notes?: readonly RoomNotice[];
  // Image parts show as thumbnails when the Deck hands over their bytes.
  readImage?: ImageReader;
  // A message to open the room at (a palette hit): older pages load until
  // it is there; onFocused says it showed or cannot.
  focusMessage?: string | null;
  onFocused?: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const rows = useMemo(() => withNotes(messages, notes), [messages, notes]);
  const lastId = useMemo(() => lastReplyId(messages), [messages]);
  const verbs = readOnly || actions === undefined ? null : actions;
  const stick = useRef(true);
  // Distance from the bottom before an older page loads, so the view stays put.
  const keep = useRef<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [away, setAway] = useState(false);

  useLayoutEffect(() => {
    const el = ref.current;
    if (el === null) return;
    if (keep.current !== null) {
      el.scrollTop = el.scrollHeight - keep.current;
      keep.current = null;
    } else if (stick.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [rows, changes]);

  const older = async () => {
    const el = ref.current;
    if (el === null || loading) return;
    setLoading(true);
    keep.current = el.scrollHeight - el.scrollTop;
    await onOlder();
    setLoading(false);
  };

  // The row of focusMessage, once it is loaded, for the virtual list.
  const [shown, setShown] = useState<number | null>(null);
  useEffect(() => {
    if (focusMessage === null) return;
    const i = rows.findIndex((r) => "role" in r && r.id === focusMessage);
    if (i >= 0) {
      stick.current = false;
      setShown(i);
    } else if (more) {
      if (!loading) void older();
    } else if (rows.length > 0) {
      onFocused?.();
    }
    // older and onFocused change with each render; the rows say when to look again.
  }, [focusMessage, rows, more, loading]);
  useEffect(() => {
    if (shown === null || focusMessage === null) return;
    // A virtual row renders a frame or two after the list scrolls to it.
    let frames = 0;
    let raf = 0;
    const find = () => {
      const el = ref.current?.querySelector<HTMLElement>(`[data-mid="${CSS.escape(focusMessage)}"]`);
      if (el === null || el === undefined) {
        if (frames++ < 30) raf = requestAnimationFrame(find);
        return;
      }
      reveal(el);
      setShown(null);
      onFocused?.();
    };
    raf = requestAnimationFrame(find);
    return () => cancelAnimationFrame(raf);
  }, [shown, focusMessage]);

  const toBottom = () => {
    const el = ref.current;
    if (el === null) return;
    stick.current = true;
    setAway(false);
    // A long list measures its rows on the way down, so it jumps there.
    el.scrollTo({ top: el.scrollHeight, behavior: messages.length > VIRTUAL_AFTER ? "instant" : "smooth" });
  };
  // Rows measured after a render change the height: stay at the bottom.
  const pin = useCallback(() => {
    const el = ref.current;
    if (el !== null && stick.current && keep.current === null) el.scrollTop = el.scrollHeight;
  }, []);

  return (
    <Images.Provider value={readImage ?? null}>
      <div className="relative min-h-0 flex-1" data-yolo-region="">
        <div
          className="timeline h-full overflow-y-auto"
          ref={ref}
          onScroll={(e) => {
            const near = isNearBottom(e.currentTarget);
            stick.current = near;
            setAway(!near);
          }}
        >
          <div className="timeline-inner mx-auto flex w-full max-w-(--chat-max) flex-col gap-6 px-5 pt-4 pb-8">
            {more && (
              <button
                type="button"
                className="older w-full rounded-md py-1.5 text-faint text-xs outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default"
                onClick={() => void older()}
                disabled={loading}
              >
                {loading ? "Loading…" : "Load older messages"}
              </button>
            )}
            {messages.length === 0 && (
              <p className="empty py-24 text-center text-faint text-sm">
                Send a message to start the conversation.
              </p>
            )}
            <VirtualList
              items={rows}
              keyOf={rowKey}
              scroller={ref}
              estimate={120}
              gap={24}
              className="flex flex-col gap-6"
              onResize={pin}
              reveal={shown}
              render={(m) =>
                "role" in m ? (
                  <MessageView m={m} roster={roster} theme={theme} mode={mode} actions={verbs} last={m.id === lastId} />
                ) : (
                  <p className="room-note text-center text-faint text-xs">{m.text}</p>
                )
              }
            />
            {changes !== null && changes.length > 0 && <ChangedFilesCard files={changes} onOpen={onOpenDiff} />}
          </div>
        </div>
        {away && (
          <button
            type="button"
            className="jump -translate-x-1/2 absolute bottom-3 left-1/2 flex h-7 cursor-pointer items-center gap-1.5 rounded-full border border-hairline-strong bg-surface-2 px-3 text-muted-foreground text-xs shadow-popover outline-none inset-shadow-hl hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            onClick={toBottom}
          >
            <ArrowDown className="size-3.5" />
            Jump to latest
          </button>
        )}
      </div>
    </Images.Provider>
  );
}
