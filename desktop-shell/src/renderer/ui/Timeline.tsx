import type { AgentLine, ChangedFile, Message, Mode, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type Chunk, groupParts, isNearBottom } from "@web/transcript";
import { toMillis } from "@web/time";
import {
  ArrowDown,
  Brain,
  Check,
  ChevronRight,
  CircleAlert,
  Copy,
  Eye,
  FilePen,
  FileText,
  Globe,
  ImageIcon,
  type IconType,
  Pencil,
  Play,
  RotateCcw,
  Search,
  SquareTerminal,
  Wrench,
} from "./icons";
import {
  createContext,
  type CSSProperties,
  memo,
  type ReactElement,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
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

// Rows between the messages, so a reply reads as its own block. The list's
// class says the same (gap-6).
const ROW_GAP = 24;

// An icon for a tool by its name, as agents name them.
function toolIcon(name: string): IconType {
  const n = name.toLowerCase();
  if (COMMAND.test(n)) return SquareTerminal;
  if (/edit|write|patch|apply|create|multiedit/.test(n)) return FilePen;
  if (/grep|glob|search|find|ls/.test(n)) return Search;
  if (/read|view|cat|open/.test(n)) return Eye;
  if (/fetch|web|http|browse/.test(n)) return Globe;
  return Wrench;
}

// An agent's hue as a custom property, for the classes that tint with it.
const hue = (colour: string) => ({ "--hue": colour }) as CSSProperties;

// A tool's output shows this many lines until the person asks for all.
const OUTPUT_LINES = 40;

function ToolOutput({ output }: { output: string }) {
  const [all, setAll] = useState(false);
  const capped = useMemo(() => capLines(output, OUTPUT_LINES), [output]);
  const cut = !all && capped.cut;
  return (
    <div className="mt-1 mb-2 ml-6 overflow-hidden rounded-lg bg-surface-1">
      <pre className="tool-output max-h-72 overflow-auto px-3.5 py-2.5 font-mono text-code text-foreground/85">{cut ? capped.text : output}</pre>
      {cut && (
        <button
          type="button"
          className="tool-more flex w-full cursor-pointer items-center gap-1.5 border-hairline border-t px-3.5 py-1.5 text-left text-2xs text-muted-foreground outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          onClick={() => setAll(true)}
        >
          Show all <span className="tabular-nums">{capped.count} lines</span>
        </button>
      )}
    </div>
  );
}

// The quiet row a piece of work folds to: an icon, then mono text, with a
// caret that shows on hover and turns as it opens.
const WORK_ROW =
  "-mx-2 flex h-7 min-w-0 cursor-pointer list-none items-center gap-2.5 rounded-lg px-2 outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden";

function Caret({ shown = true }: { shown?: boolean }) {
  return (
    <ChevronRight
      className={cn(
        "size-3 shrink-0 text-faint transition-[rotate,opacity] duration-200 group-open/fold:rotate-90",
        shown ? "opacity-0 group-hover/fold:opacity-100 group-open/fold:opacity-100" : "invisible",
      )}
    />
  );
}

// One line of the work log: thinking or a tool call, folded to a row.
function WorkRow({ c, running }: { c: Chunk; running: boolean }) {
  if (c.k === "reasoning") {
    return (
      <details className="fold reasoning group/fold">
        <summary className={cn(WORK_ROW, "text-muted-foreground text-sm")}>
          <Brain className="size-3.5 shrink-0 text-faint" />
          <span>Thought</span>
          <Caret />
        </summary>
        <p className="reasoning-text mt-0.5 mb-2 ml-[7px] whitespace-pre-wrap text-pretty border-hairline-strong border-l pl-[22px] text-muted-foreground text-xs leading-relaxed">
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
  const opens = output !== "" || diff !== "";
  return (
    <details className={cn("fold tool group/fold", failed && "failed")}>
      <summary className={cn(WORK_ROW, "font-mono text-code")}>
        <Icon className={cn("size-3.5 shrink-0", failed ? "text-destructive-foreground" : "text-faint")} />
        {command ? (
          <>
            <span className="tool-name sr-only">{name}</span>
            <span aria-hidden className="-mr-1 shrink-0 text-faint">
              $
            </span>
            <span className={cn("tool-summary min-w-0 truncate text-foreground/80", pending && "shimmer")}>{summary}</span>
          </>
        ) : (
          <>
            <span className={cn("tool-name shrink-0 text-foreground/80", pending && "shimmer")}>{name}</span>
            <span className="tool-summary min-w-0 truncate text-muted-foreground">{summary}</span>
          </>
        )}
        <span className="tool-state sr-only">{pending ? "running" : failed ? "failed" : "done"}</span>
        <span className="ml-auto" />
        {failed && <span className="shrink-0 font-sans text-2xs text-destructive-foreground">failed</span>}
        <Caret shown={opens} />
      </summary>
      {output !== "" && <ToolOutput output={output} />}
      {diff !== "" && (
        <div className="mt-1 mb-2 ml-6 overflow-hidden rounded-lg bg-surface-1">
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
        className="image-part w-fit cursor-zoom-in rounded-[14px] outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Open ${c.name}`}
        title={c.name}
        onClick={() => setBig(true)}
      >
        <img src={url} alt={c.name} className="block max-h-60 max-w-[240px] rounded-[14px] ring-1 ring-hairline" />
      </button>
      {big &&
        createPortal(
          <Dialog title={c.name} onClose={() => setBig(false)} className="image-lightbox w-auto max-w-[min(90vw,1200px)]">
            <img src={url} alt={c.name} className="mb-3 max-h-[70vh] max-w-full rounded-[12px] ring-1 ring-hairline object-contain" />
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

function ImageChip({ name }: { name: string }) {
  return (
    <span className="chip inline-flex w-fit items-center gap-1.5 text-muted-foreground text-xs">
      <ImageIcon className="size-3.5 text-faint" />
      {name}
    </span>
  );
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
          <ImagePart key={i} c={c} chip={<ImageChip name={c.name} />} />
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
      {/* The person's turn: a bubble of the accent, on the right. */}
      <div className="user-card text-message-foreground">
        <div className="flex flex-col gap-2">
          {chunks.map((c, i) =>
            c.k === "text" ? (
              <p key={i} className="user-text whitespace-pre-wrap text-pretty break-words text-prose">
                {c.text}
              </p>
            ) : c.k === "image" ? (
              <ImagePart key={i} c={c} chip={<ImageChip name={c.name} />} />
            ) : null,
          )}
        </div>
      </div>
      <header className="msg-head flex h-6 items-center gap-1.5 pe-1 text-2xs text-faint opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
        <span className="msg-who sr-only">You</span>
        {actions !== null && <MessageActions m={m} actions={actions} last={false} />}
        {m.edited === true && <span>edited ·</span>}
        <time className="tabular-nums">{formatClock(m.created_at, prefs.clock)}</time>
      </header>
    </article>
  );
}

// The agent's glyph, set in type in its hue. While the agent works, the
// glyph's brightness steps.
function AgentMark({ mark, working }: { mark: string; working: boolean }) {
  return (
    <span aria-hidden className={cn("msg-mark min-w-4 shrink-0 font-mono text-(--hue) text-[12.5px]", working && "animate-deck-pulse")}>
      {mark}
    </span>
  );
}

// "Worked for 12s · 3 steps ›": a finished reply's work, folded to one line
// of type.
function WorkFold({ work, took, open, onToggle }: { work: Chunk[]; took: string; open: boolean; onToggle: () => void }) {
  const steps = work.filter((c) => c.k !== "text").length;
  return (
    <button
      type="button"
      className="worked group/worked flex h-6 w-fit cursor-pointer select-none items-center gap-1.5 rounded-md text-muted-foreground text-sm outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:text-foreground"
      aria-expanded={open}
      onClick={onToggle}
    >
      <span>{took}</span>
      <span aria-hidden className="text-faint">
        ·
      </span>
      <span className="text-faint tabular-nums">
        {steps} step{steps === 1 ? "" : "s"}
      </span>
      <ChevronRight className={cn("size-3 text-faint transition-transform duration-200", open && "rotate-90")} />
    </button>
  );
}

// "Working 12s": the word shimmers in the agent's hue.
function Working({ since }: { since: string }) {
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setTick(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return (
    <div className="streaming flex h-6 items-baseline gap-2 text-sm" aria-label="writing">
      <span className="shimmer shimmer-hue">Working</span>
      <span className="text-faint text-xs tabular-nums">{formatDuration(tick - toMillis(since))}</span>
    </div>
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
  const mark = kindMark(theme, kind) || "●";
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
        <PlanCard id={m.id} plan={plan} agent={agent} colour={colour} index={`${mark} ${shortId(m.session_id)}`} actions={last ? actions : null} />
        {plan.after !== "" && <Markdown text={plan.after} />}
      </div>
    ) : (
      <Markdown key={key} text={c.text} />
    );
  const ms = toMillis(m.updated_at) - toMillis(m.created_at);
  const took = ms >= 1000 ? `Worked for ${formatDuration(ms)}` : "Worked";
  return (
    <article
      className="msg msg-agent group flex min-w-0 animate-enter flex-col gap-2"
      data-role="assistant"
      data-agent={agent}
      data-mid={m.id}
      style={hue(colour)}
    >
      {/* The agent's glyph and name in its hue: no card, the reply reads
          as prose on the sheet. */}
      <header className="msg-head flex h-6 items-center gap-2">
        <span className="msg-index inline-flex min-w-0 items-baseline gap-1.5">
          <AgentMark mark={mark} working={!m.finished} />
          <span className="msg-who truncate font-semibold text-(--hue) text-[13px]">{agent}</span>
        </span>
        <time className="text-2xs text-faint tabular-nums opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          {formatClock(m.created_at, prefs.clock)}
        </time>
        {/* Copy, retry and continue show on hover at the line's end, so a
            reply leaves no empty row under it. */}
        {m.finished && actions !== null && (
          <span className="-my-1 ml-auto flex items-center">
            <MessageActions m={m} actions={actions} last={last} />
          </span>
        )}
      </header>
      {folds ? (
        <>
          <WorkFold work={work} took={took} open={open} onToggle={() => setOpen((o) => !o)} />
          {open && (
            <div className="work-log ml-[5px] flex animate-enter flex-col gap-px border-hairline-strong border-l py-1 pl-[18px]">
              {work.map((c, i) =>
                c.k === "text" ? (
                  <div key={i} className="py-1 text-muted-foreground [&_.md]:text-sm">
                    <Markdown text={c.text} />
                  </div>
                ) : (
                  <WorkRow key={i} c={c} running={false} />
                ),
              )}
            </div>
          )}
          {answer.length > 0 && (
            <div className="flex flex-col gap-3 text-foreground text-prose">
              <Prose chunks={answer} text={text} />
            </div>
          )}
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
      {!m.finished && <Working since={m.created_at} />}
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
      <article className="msg msg-system mx-auto max-w-[90%] text-pretty text-center text-muted-foreground text-xs" data-role="system" data-mid={m.id}>
        <Prose chunks={chunksOf(m)} />
      </article>
    );
  }
  return <AgentMessage m={m} roster={roster} theme={theme} mode={mode} actions={actions} last={last} />;
});

// A path as its folder, faint, and its name.
function PathName({ path }: { path: string }) {
  const cut = path.lastIndexOf("/") + 1;
  return (
    <span className="min-w-0 flex-1 truncate font-mono text-code">
      <span className="text-faint">{path.slice(0, cut)}</span>
      <span className="text-foreground">{path.slice(cut)}</span>
    </span>
  );
}

// "N changed files +a −r … Open diff", under the last reply, as T3 Code
// shows a run's changes.
export function ChangedFilesCard({ files, onOpen }: { files: ChangedFile[]; onOpen: (path?: string) => void }) {
  const [open, setOpen] = useState(false);
  const added = files.reduce((n, f) => n + f.added, 0);
  const removed = files.reduce((n, f) => n + f.removed, 0);
  return (
    // One of the two boxed things in the timeline (with a tool's output): a
    // fill and a hairline, nothing more.
    <div className="changed-files animate-enter overflow-hidden rounded-xl bg-surface-1 ring-1 ring-hairline" data-changed-files="">
      <div className="flex h-10 items-center gap-3 pr-1.5 pl-3">
        <button
          type="button"
          className="flex min-w-0 cursor-pointer items-baseline gap-2.5 rounded-md text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          <ChevronRight className={cn("size-3 shrink-0 self-center text-faint transition-transform duration-200", open && "rotate-90")} />
          <span className="font-medium text-foreground text-sm tabular-nums">
            {files.length} changed file{files.length === 1 ? "" : "s"}
          </span>
          <span className="font-mono text-2xs tabular-nums">
            <span className="text-diff-addition-foreground">+{added}</span>{" "}
            <span className="text-diff-deletion-foreground">−{removed}</span>
          </span>
          <span className="text-faint text-xs">{open ? "Hide files" : "Show files"}</span>
        </button>
        <Button size="sm" variant="ghost-muted" className="ml-auto" aria-label="Open diff" onClick={() => onOpen(files[0]?.path)}>
          Open diff
        </Button>
      </div>
      {open && (
        <ul className="flex flex-col gap-px border-hairline border-t p-1.5">
          {files.map((f) => (
            <li key={f.path}>
              <button
                type="button"
                className="flex h-7 w-full cursor-pointer items-center gap-2.5 rounded-lg px-2 text-left text-xs outline-none transition-colors hover:bg-hover focus-visible:bg-selected"
                onClick={() => onOpen(f.path)}
              >
                <FileText className="size-3.5 shrink-0 text-faint" />
                <PathName path={f.path} />
                <span className="font-mono text-2xs text-diff-addition-foreground tabular-nums">+{f.added}</span>
                <span className="font-mono text-2xs text-diff-deletion-foreground tabular-nums">−{f.removed}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// A thread with no messages yet, in type: who is on it, and how to write
// to them.
function EmptyRoom({ roster, theme }: { roster: AgentLine[]; theme: Theme | null }) {
  return (
    <div className="empty flex animate-enter flex-col gap-2 pt-[8vh]">
      <p className="font-medium text-foreground text-sm">Nothing said here yet.</p>
      {roster.length > 0 && (
        <p className="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-sm">
          {roster.map((l) => (
            <span key={l.id} className="inline-flex items-baseline gap-1.5" style={hue(agentColour(l.id, l.kind))}>
              <span aria-hidden className="font-mono text-(--hue) text-[12.5px]">
                {kindMark(theme, l.kind) || "●"}
              </span>
              <span className="text-(--hue)">{l.name}</span>
            </span>
          ))}
        </p>
      )}
      <p className="text-pretty text-faint text-xs leading-relaxed">
        Send a message to start the conversation. <span className="font-mono text-muted-foreground">$name</span> addresses an agent,{" "}
        <span className="font-mono text-muted-foreground">@</span> a file, <span className="font-mono text-muted-foreground">/</span> a command.
      </p>
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
          {/* The end pads past the floating composer (--composer-h, set by
              the room), so the last line rests above it. */}
          <div className="timeline-inner mx-auto flex w-full max-w-(--chat-max) flex-col gap-6 px-6 pt-6 pb-[calc(var(--composer-h,0px)+1.5rem)]">
            {more && (
              <button
                type="button"
                className="older mx-auto h-7 rounded-full px-3 text-faint text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default"
                onClick={() => void older()}
                disabled={loading}
              >
                {loading ? "Loading…" : "Load older messages"}
              </button>
            )}
            {messages.length === 0 && <EmptyRoom roster={roster} theme={theme} />}
            <VirtualList
              items={rows}
              keyOf={rowKey}
              scroller={ref}
              estimate={120}
              gap={ROW_GAP}
              className="flex flex-col gap-6"
              onResize={pin}
              reveal={shown}
              render={(m) =>
                "role" in m ? (
                  <MessageView m={m} roster={roster} theme={theme} mode={mode} actions={verbs} last={m.id === lastId} />
                ) : (
                  <p className="room-note flex items-center gap-3 text-faint text-xs before:h-px before:flex-1 before:bg-hairline after:h-px after:flex-1 after:bg-hairline">
                    {m.text}
                  </p>
                )
              }
            />
            {changes !== null && changes.length > 0 && <ChangedFilesCard files={changes} onOpen={onOpenDiff} />}
          </div>
        </div>
        {away && (
          <button
            type="button"
            className="jump liquid-thick absolute inset-x-0 bottom-[calc(var(--composer-h,0px)+0.75rem)] z-10 mx-auto flex w-fit h-8 animate-enter cursor-pointer items-center gap-1.5 rounded-full pr-3.5 pl-3 text-foreground text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onClick={toBottom}
          >
            <ArrowDown className="size-3.5" weight="bold" />
            Jump to latest
          </button>
        )}
      </div>
    </Images.Provider>
  );
}
