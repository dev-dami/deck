import { activeAgents, toggleAgent } from "@web/composer";
import type { AgentLine, Approval, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type CSSProperties, useEffect, useState } from "react";
import { agentColour } from "../agents";
import { tintOf } from "../teamGate";
import { agentStatus } from "../approvals";
import type { AgentNode } from "../crew";
import { contextPct, nodeOf } from "../lanes";
import { cn } from "../lib/cn";
import type { ProjectApi, UsageLimit } from "../project";
import { Ring } from "./kit/ring";

// A plan window past this share shows on the chip, before it stops a turn.
const LIMIT_PCT = 80;
const LIMITS_MS = 60_000;

// The fullest plan window of a kind past LIMIT_PCT, or null.
export function planLimit(kind: string, limits: readonly UsageLimit[]): UsageLimit | null {
  let worst: UsageLimit | null = null;
  for (const l of limits) {
    if (l.agentKind === kind && l.usedPct > LIMIT_PCT && (worst === null || l.usedPct > worst.usedPct)) worst = l;
  }
  return worst;
}

// The plan limits from Usage, fetched at most once a minute per project,
// and only while a strip shows.
const limitsCache = new WeakMap<ProjectApi, { at: number; limits: UsageLimit[] }>();

function useLimits(api: ProjectApi | undefined): UsageLimit[] {
  const [limits, setLimits] = useState<UsageLimit[]>(() => (api === undefined ? [] : (limitsCache.get(api)?.limits ?? [])));
  useEffect(() => {
    if (api === undefined) return;
    let live = true;
    const look = () => {
      const cached = limitsCache.get(api);
      if (cached !== undefined && Date.now() - cached.at < LIMITS_MS) {
        setLimits(cached.limits);
        return;
      }
      // Marked before the answer, so two strips do not both ask.
      limitsCache.set(api, { at: Date.now(), limits: cached?.limits ?? [] });
      void api.usage(1).then((u) => {
        if (typeof u === "string") return;
        limitsCache.set(api, { at: Date.now(), limits: u.limits });
        if (live) setLimits(u.limits);
      });
    };
    look();
    const t = setInterval(look, LIMITS_MS);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [api]);
  return limits;
}

const resetTime = (ms: number) => new Date(ms).toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" });

const DOT = {
  busy: "animate-deck-pulse bg-signal",
  waiting: "bg-warning",
  idle: "bg-muted-foreground/40",
} as const;

// One agent as a send target: its glyph, name, context ring and a status
// dot. A click adds it to the message's addressees or takes it out. The
// status word stays for screen readers; the lane strip shows it.
function Chip({
  line,
  status,
  on,
  theme,
  context,
  limit,
  onToggle,
}: {
  line: AgentLine;
  status: "busy" | "waiting" | "idle";
  on: boolean;
  theme: Theme | null;
  context: number | null;
  limit: UsageLimit | null;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={on}
      aria-label={`Send to ${line.id}`}
      data-agent={line.id}
      className={cn(
        // In type: the glyph in its hue and the name. Addressed, both come
        // up to full brightness; otherwise they rest dimmed.
        `agent agent-${status} group/agent flex h-7 max-w-56 shrink-0 cursor-pointer items-center gap-1.5 rounded-full px-2 text-faint text-xs outline-none transition-colors hover:bg-hover hover:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring`,
        on && "on text-foreground hover:text-foreground",
      )}
      style={{ "--hue": agentColour(line.id, line.kind, tintOf(line.color)) } as CSSProperties}
      title={`${line.id}: ${line.model ?? "default model"} (${status})`}
      onClick={onToggle}
    >
      <span aria-hidden className={cn("font-mono text-(--hue) text-[11px] transition-opacity", !on && "opacity-55 group-hover/agent:opacity-80")}>
        {kindMark(theme, line.kind) || "●"}
      </span>
      <span className={cn("agent-name truncate", on && "font-medium")}>{line.name}</span>
      {context !== null && (
        <span className="agent-context flex items-center" title={`${context}% of its context`}>
          <Ring pct={context} />
        </span>
      )}
      <span aria-hidden className={cn("agent-dot size-1.5 shrink-0 rounded-full", DOT[status])} />
      <span className="agent-status sr-only">{status}</span>
      {limit !== null && (
        <span
          className="agent-limit shrink-0 font-mono text-3xs text-warning-foreground tabular-nums"
          title={`${line.kind} ${limit.label || limit.kind} window: ${Math.round(limit.usedPct)}% used${limit.resetsAt > 0 ? `. Resets ${resetTime(limit.resetsAt)}` : ""}`}
        >
          limit {Math.round(limit.usedPct)}%
        </span>
      )}
    </button>
  );
}

// The send-target picker under the composer: one chip per agent in the
// thread. Agents join by an @ or $ mention, or when an agent spawns them;
// there is no add or remove control. hint says what Enter does while an
// agent works.
export function AgentsStrip({
  sessionId,
  roster,
  approvals,
  theme,
  api,
  agents,
  draft,
  setDraft,
  hint,
  onFocus,
}: {
  sessionId: string;
  roster: AgentLine[];
  approvals: Approval[];
  theme: Theme | null;
  // Usage (plan limits): a local Deck only.
  api: ProjectApi | undefined;
  // The agents tree, for each agent's context; null before it loads.
  agents: AgentNode[] | null;
  draft: string;
  setDraft: (s: string) => void;
  hint: string | null;
  onFocus: () => void;
}) {
  const limits = useLimits(api);
  const addressed = activeAgents(draft);
  return (
    <div className="agents flex min-w-24 flex-1 basis-0 flex-wrap items-center gap-0.5" role="group" aria-label="Send to">
      {roster.map((l) => (
        <Chip
          key={l.id}
          line={l}
          status={agentStatus(l, sessionId, approvals)}
          on={addressed.includes(l.id)}
          theme={theme}
          context={contextPct(nodeOf(l, agents))}
          limit={planLimit(l.kind, limits)}
          onToggle={() => {
            setDraft(toggleAgent(draft, l.id));
            onFocus();
          }}
        />
      ))}
      {roster.length === 0 && <span className="px-1.5 text-muted-foreground/60 text-xs">No agents yet</span>}
      {hint !== null && <span className="send-hint hidden shrink-0 px-1.5 text-[11px] text-faint @[46rem]/bar:inline">{hint}</span>}
    </div>
  );
}
