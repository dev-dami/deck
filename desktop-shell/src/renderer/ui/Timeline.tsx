import type { AgentLine, Message, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { groupParts, isNearBottom } from "@web/transcript";
import {
  ArrowDown,
  Brain,
  Check,
  ChevronRight,
  Eye,
  FilePen,
  Globe,
  ImageIcon,
  Loader2,
  type LucideIcon,
  Search,
  SquareTerminal,
  Wrench,
  X,
} from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { agentColour, agentKind } from "../agents";
import { cn } from "../lib/cn";
import { DiffView } from "./DiffView";
import { Button } from "./kit/button";
import { Markdown } from "./Markdown";

function clock(t: string): string {
  const d = new Date(t);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

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

function Body({ m }: { m: Message }) {
  return (
    <>
      {groupParts(m.parts).map((c, i) => {
        switch (c.k) {
          case "text":
            return m.role === "user" ? (
              <p key={i} className="user-text whitespace-pre-wrap break-words">
                {c.text}
              </p>
            ) : (
              <Markdown key={i} text={c.text} />
            );
          case "reasoning":
            return (
              <details key={i} className="fold reasoning group/fold">
                <summary className="flex w-fit cursor-pointer list-none items-center gap-1.5 rounded-md py-0.5 text-muted-foreground text-xs hover:text-foreground [&::-webkit-details-marker]:hidden">
                  <Brain className="size-3.5" />
                  Thinking
                  <ChevronRight className="size-3 transition-transform group-open/fold:rotate-90" />
                </summary>
                <p className="reasoning-text mt-1.5 whitespace-pre-wrap border-l-2 pl-3 text-muted-foreground text-xs leading-relaxed">
                  {c.text}
                </p>
              </details>
            );
          case "tool": {
            const name = c.call?.name ?? c.result?.name ?? "tool";
            const failed = c.result?.is_error === true;
            const running = c.result === null;
            const Icon = toolIcon(name);
            const output = c.result?.output ?? "";
            const diff = c.result?.diff ?? "";
            const hasMore = output !== "" || diff !== "";
            return (
              <details
                key={i}
                className={cn(
                  "fold tool group/fold overflow-hidden rounded-lg border bg-card/40",
                  failed && "failed border-destructive/30",
                )}
              >
                <summary
                  className={cn(
                    "flex min-w-0 list-none items-center gap-2 px-2.5 py-1.5 text-xs [&::-webkit-details-marker]:hidden",
                    hasMore ? "cursor-pointer hover:bg-accent/60" : "cursor-default",
                  )}
                >
                  <Icon className={cn("size-3.5 shrink-0", failed ? "text-destructive-foreground" : "text-muted-foreground")} />
                  <span className="tool-name shrink-0 font-medium text-foreground/90">{name}</span>
                  <span className="tool-summary min-w-0 flex-1 truncate font-mono text-2xs text-muted-foreground">
                    {c.call?.summary ?? ""}
                  </span>
                  <span
                    className={cn(
                      "tool-state flex shrink-0 items-center gap-1 text-2xs",
                      failed ? "text-destructive-foreground" : "text-muted-foreground",
                    )}
                  >
                    {running ? (
                      <Loader2 className="size-3 animate-spin" />
                    ) : failed ? (
                      <X className="size-3" />
                    ) : (
                      <Check className="size-3 text-success-foreground" />
                    )}
                    <span className="sr-only">{running ? "running" : failed ? "failed" : "done"}</span>
                  </span>
                  {hasMore && (
                    <ChevronRight className="size-3 shrink-0 text-muted-foreground transition-transform group-open/fold:rotate-90" />
                  )}
                </summary>
                {output !== "" && (
                  <pre className="tool-output max-h-80 overflow-auto border-t bg-code px-3 py-2 font-mono text-2xs leading-relaxed text-foreground/85">
                    {output}
                  </pre>
                )}
                {diff !== "" && (
                  <div className="border-t">
                    <DiffView diff={diff} truncated={c.result?.diff_truncated} />
                  </div>
                )}
              </details>
            );
          }
          case "image":
            return (
              <span
                key={i}
                className="chip inline-flex w-fit items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-muted-foreground text-xs"
              >
                <ImageIcon className="size-3.5" />
                {c.name}
              </span>
            );
        }
      })}
    </>
  );
}

export function MessageView({ m, roster, theme }: { m: Message; roster: AgentLine[]; theme: Theme | null }) {
  if (m.role === "user") {
    return (
      <article className="msg msg-user group/msg flex flex-col items-end gap-1" data-role="user">
        <div className="max-w-[80%] rounded-2xl bg-message px-4 py-2.5 text-[14px] leading-relaxed">
          <div className="flex flex-col gap-2">
            <Body m={m} />
          </div>
        </div>
        <header className="msg-head flex items-center gap-2 pr-1 text-2xs text-muted-foreground opacity-0 transition-opacity group-hover/msg:opacity-100">
          <span className="msg-who sr-only">You</span>
          {m.edited === true && <span>edited</span>}
          <time>{clock(m.created_at)}</time>
        </header>
      </article>
    );
  }
  if (m.role === "system") {
    return (
      <article
        className="msg msg-system mx-auto max-w-[90%] text-center text-muted-foreground text-xs [&_.md]:leading-normal"
        data-role="system"
      >
        <Body m={m} />
      </article>
    );
  }
  const agent = m.agent ?? "agent";
  const kind = agentKind(agent, roster);
  const colour = agentColour(agent, kind);
  return (
    <article className="msg msg-agent flex flex-col gap-2 px-1" data-role="assistant" data-agent={agent}>
      <header className="msg-head flex items-center gap-1.5 text-xs">
        <span className="msg-mark grid size-4 place-items-center font-mono text-[11px]" style={{ color: colour }}>
          {kindMark(theme, kind) || "●"}
        </span>
        <span className="msg-who font-medium" style={{ color: colour }}>
          {agent}
        </span>
        <time className="text-2xs text-muted-foreground">{clock(m.created_at)}</time>
      </header>
      <div className="flex flex-col gap-2 text-[14px] text-foreground/95">
        <Body m={m} />
      </div>
      {!m.finished && (
        <div className="streaming flex items-center gap-2 text-xs" aria-label="writing">
          <span className="size-1.5 animate-deck-pulse rounded-full" style={{ background: colour }} />
          <span className="shimmer font-medium">Working…</span>
        </div>
      )}
    </article>
  );
}

export function Timeline({
  messages,
  roster,
  theme,
  more,
  onOlder,
}: {
  messages: Message[];
  roster: AgentLine[];
  theme: Theme | null;
  more: boolean;
  onOlder: () => Promise<unknown>;
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
  }, [messages]);

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
        <div className="timeline-inner mx-auto flex w-full max-w-(--chat-max) flex-col gap-6 px-6 pt-6 pb-10">
          {more && (
            <button
              type="button"
              className="older w-full py-1.5 text-muted-foreground text-xs hover:text-foreground disabled:cursor-default"
              onClick={() => void older()}
              disabled={loading}
            >
              {loading ? "Loading…" : "Load older messages"}
            </button>
          )}
          {messages.length === 0 && (
            <p className="empty py-16 text-center text-muted-foreground text-sm">
              No messages yet. Write to an agent below.
            </p>
          )}
          {messages.map((m) => (
            <MessageView key={m.id} m={m} roster={roster} theme={theme} />
          ))}
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
