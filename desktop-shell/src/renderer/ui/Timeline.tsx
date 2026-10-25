import type { AgentLine, ChangedFile, Message, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type Chunk, groupParts, isNearBottom } from "@web/transcript";
import { toMillis } from "@web/time";
import {
  ArrowDown,
  Brain,
  ChevronDown,
  ChevronRight,
  CircleAlert,
  Eye,
  FileDiff,
  FilePen,
  FileText,
  Globe,
  ImageIcon,
  type LucideIcon,
  Search,
  SquareTerminal,
  Wrench,
} from "lucide-react";
import { memo, useEffect, useLayoutEffect, useRef, useState } from "react";
import { agentColour, agentKind } from "../agents";
import { cn } from "../lib/cn";
import { formatClock, usePrefs } from "../prefs";
import { formatDuration, splitWork } from "../work";
import { DiffView } from "./DiffView";
import { Button } from "./kit/button";
import { Markdown } from "./Markdown";

// An icon for a tool by its name, as agents name them.
function toolIcon(name: string): LucideIcon {
  const n = name.toLowerCase();
  if (/bash|shell|exec|command|terminal|run/.test(n)) return SquareTerminal;
  if (/edit|write|patch|apply|create|multiedit/.test(n)) return FilePen;
  if (/grep|glob|search|find|ls/.test(n)) return Search;
  if (/read|view|cat|open/.test(n)) return Eye;
  if (/fetch|web|http|browse/.test(n)) return Globe;
  return Wrench;
}

// One line of the work log: thinking or a tool call, folded to a row.
function WorkRow({ c, running }: { c: Chunk; running: boolean }) {
  if (c.k === "reasoning") {
    return (
      <details className="fold reasoning group/fold">
        <summary className="flex min-w-0 cursor-pointer list-none items-center gap-2 py-1 text-muted-foreground text-sm hover:text-foreground [&::-webkit-details-marker]:hidden">
          <Brain className="size-3.5 shrink-0" />
          <span>Thought</span>
          <ChevronRight className="size-3 shrink-0 transition-transform group-open/fold:rotate-90" />
        </summary>
        <p className="reasoning-text mt-1 mb-2 ml-1.5 whitespace-pre-wrap border-l pl-4 text-muted-foreground text-xs leading-relaxed">
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
  return (
    <details className={cn("fold tool group/fold", failed && "failed")}>
      <summary className="flex min-w-0 cursor-pointer list-none items-center gap-2 py-1 text-sm [&::-webkit-details-marker]:hidden">
        <Icon className={cn("size-3.5 shrink-0", failed ? "text-destructive-foreground" : "text-muted-foreground")} />
        <span className={cn("tool-name shrink-0 text-foreground/80", pending && "shimmer")}>{name}</span>
        <span className="tool-summary min-w-0 flex-1 truncate font-mono text-muted-foreground/65 text-xs">
          {c.call?.summary ?? ""}
        </span>
        <span className="tool-state sr-only">{pending ? "running" : failed ? "failed" : "done"}</span>
        {(output !== "" || diff !== "") && (
          <ChevronDown className="size-3 shrink-0 text-muted-foreground/60 transition-transform group-open/fold:rotate-180" />
        )}
      </summary>
      {output !== "" && (
        <pre className="tool-output mt-1 mb-2 max-h-72 overflow-auto rounded-lg border bg-code px-3 py-2 font-mono text-foreground/85 text-xs leading-relaxed">
          {output}
        </pre>
      )}
      {diff !== "" && (
        <div className="mt-1 mb-2 overflow-hidden rounded-lg border">
          <DiffView diff={diff} truncated={c.result?.diff_truncated} />
        </div>
      )}
    </details>
  );
}

function Prose({ chunks }: { chunks: Chunk[] }) {
  return (
    <>
      {chunks.map((c, i) =>
        c.k === "text" ? (
          <Markdown key={i} text={c.text} />
        ) : c.k === "image" ? (
          <span key={i} className="chip inline-flex w-fit items-center gap-1.5 rounded-md bg-secondary px-2 py-1 text-xs">
            <ImageIcon className="size-3.5" />
            {c.name}
          </span>
        ) : (
          <WorkRow key={i} c={c} running={false} />
        ),
      )}
    </>
  );
}

function UserMessage({ m }: { m: Message }) {
  const prefs = usePrefs();
  const chunks = groupParts(m.parts);
  return (
    <article className="msg msg-user group flex flex-col items-end gap-1" data-role="user">
      <div className="relative max-w-[80%] rounded-2xl bg-message p-3 text-message-foreground">
        <div className="flex flex-col gap-2">
          {chunks.map((c, i) =>
            c.k === "text" ? (
              <p key={i} className="user-text whitespace-pre-wrap break-words text-sm leading-relaxed">
                {c.text}
              </p>
            ) : c.k === "image" ? (
              <span key={i} className="chip inline-flex w-fit items-center gap-1.5 rounded-md bg-background/60 px-2 py-1 text-xs">
                <ImageIcon className="size-3.5" />
                {c.name}
              </span>
            ) : null,
          )}
        </div>
      </div>
      <header className="msg-head flex items-center gap-2 pe-1 text-muted-foreground text-xs tabular-nums opacity-0 transition-opacity group-hover:opacity-100">
        <span className="msg-who sr-only">You</span>
        {m.edited === true && <span>edited</span>}
        <time>{formatClock(m.created_at, prefs.clock)}</time>
      </header>
    </article>
  );
}

function AgentMessage({ m, roster, theme }: { m: Message; roster: AgentLine[]; theme: Theme | null }) {
  const prefs = usePrefs();
  const agent = m.agent ?? "agent";
  const kind = agentKind(agent, roster);
  const colour = agentColour(agent, kind);
  const chunks = groupParts(m.parts);
  const { work, answer } = splitWork(chunks);
  const [open, setOpen] = useState(false);
  const folds = m.finished && prefs.foldWork && work.length > 0;
  const ms = toMillis(m.updated_at) - toMillis(m.created_at);
  const took = ms >= 1000 ? `Worked for ${formatDuration(ms)}` : "Worked";
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    if (m.finished) return;
    const t = setInterval(() => setTick(Date.now()), 1000);
    return () => clearInterval(t);
  }, [m.finished]);
  return (
    <article className="msg msg-agent group flex min-w-0 flex-col gap-1 px-1" data-role="assistant" data-agent={agent}>
      <header className="msg-head flex items-center gap-1.5 text-xs">
        <span className="msg-mark grid size-4 place-items-center font-mono text-[11px]" style={{ color: colour }}>
          {kindMark(theme, kind) || "●"}
        </span>
        <span className="msg-who font-medium" style={{ color: colour }}>
          {agent}
        </span>
        <time className="text-muted-foreground/70 tabular-nums opacity-0 transition-opacity group-hover:opacity-100">
          {formatClock(m.created_at, prefs.clock)}
        </time>
      </header>
      {folds ? (
        <>
          <div className="flex items-center gap-1 border-border/60 border-b pt-1 pb-2">
            <button
              type="button"
              className="worked flex cursor-pointer select-none items-center gap-1 rounded-md px-1 text-muted-foreground text-sm tabular-nums transition-colors hover:text-foreground"
              aria-expanded={open}
              onClick={() => setOpen((o) => !o)}
            >
              {took}
              {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
            </button>
          </div>
          {open && (
            <div className="flex flex-col gap-0.5 border-border/60 border-b pb-2">
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
          <div className="mt-2 flex flex-col gap-3 text-[15px] text-foreground">
            <Prose chunks={answer} />
          </div>
        </>
      ) : (
        <div className="flex flex-col gap-2 text-[15px] text-foreground">
          {chunks.map((c, i) =>
            c.k === "text" ? (
              <Markdown key={i} text={c.text} />
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
          <span className="shimmer">Working for {formatDuration(tick - toMillis(m.created_at))}</span>
        </div>
      )}
    </article>
  );
}

// A message renders again only when it changes: a token re-renders the row
// it lands in, not the whole timeline.
export const MessageView = memo(function MessageView({ m, roster, theme }: { m: Message; roster: AgentLine[]; theme: Theme | null }) {
  if (m.role === "user") return <UserMessage m={m} />;
  if (m.role === "system") {
    return (
      <article className="msg msg-system mx-auto max-w-[90%] text-center text-muted-foreground text-xs" data-role="system">
        <Prose chunks={groupParts(m.parts)} />
      </article>
    );
  }
  return <AgentMessage m={m} roster={roster} theme={theme} />;
});

// "N changed files +a −r … Open diff", under the last reply, as T3 Code
// shows a run's changes.
export function ChangedFilesCard({ files, onOpen }: { files: ChangedFile[]; onOpen: (path?: string) => void }) {
  const [open, setOpen] = useState(false);
  const added = files.reduce((n, f) => n + f.added, 0);
  const removed = files.reduce((n, f) => n + f.removed, 0);
  return (
    <div className="changed-files rounded-lg bg-secondary" data-changed-files="">
      <div className="flex items-center justify-between gap-2 px-3 py-2">
        <button
          type="button"
          className="flex min-w-0 cursor-pointer items-center gap-x-3 font-medium text-foreground text-xs"
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
                className="flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-accent"
                onClick={() => onOpen(f.path)}
              >
                <FileText className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate font-mono">{f.path}</span>
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
}: {
  messages: Message[];
  roster: AgentLine[];
  theme: Theme | null;
  more: boolean;
  onOlder: () => Promise<unknown>;
  changes: ChangedFile[] | null;
  onOpenDiff: (path?: string) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
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
  }, [messages, changes]);

  const older = async () => {
    const el = ref.current;
    if (el === null || loading) return;
    setLoading(true);
    keep.current = el.scrollHeight - el.scrollTop;
    await onOlder();
    setLoading(false);
  };

  const toBottom = () => {
    const el = ref.current;
    if (el === null) return;
    stick.current = true;
    setAway(false);
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  };

  return (
    <div className="relative min-h-0 flex-1">
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
              className="older w-full py-1.5 text-muted-foreground/60 text-xs hover:text-foreground disabled:cursor-default"
              onClick={() => void older()}
              disabled={loading}
            >
              {loading ? "Loading…" : "Load older messages"}
            </button>
          )}
          {messages.length === 0 && (
            <p className="empty py-24 text-center text-muted-foreground/40 text-sm">
              Send a message to start the conversation.
            </p>
          )}
          {messages.map((m) => (
            <MessageView key={m.id} m={m} roster={roster} theme={theme} />
          ))}
          {changes !== null && changes.length > 0 && <ChangedFilesCard files={changes} onOpen={onOpenDiff} />}
        </div>
      </div>
      {away && (
        <Button
          variant="outline"
          size="icon-sm"
          className="-translate-x-1/2 absolute bottom-3 left-1/2 rounded-full shadow-lg"
          aria-label="Scroll to the latest message"
          onClick={toBottom}
        >
          <ArrowDown />
        </Button>
      )}
    </div>
  );
}
