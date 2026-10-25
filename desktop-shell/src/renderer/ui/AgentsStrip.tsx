import { activeAgents, toggleAgent } from "@web/composer";
import type { AgentLine, Approval, ModelChoice, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { Check, ChevronRight, Loader2, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { agentColour } from "../agents";
import { agentStatus } from "../approvals";
import { addAgentMention } from "../completion";
import type { AgentNode } from "../crew";
import { contextPct, nodeOf } from "../lanes";
import { cn } from "../lib/cn";
import type { ProjectApi, UsageLimit } from "../project";
import { MenuItem, MenuLabel, Popover } from "./kit/menu";
import { Ring } from "./kit/ring";

export function Models({
  line,
  load,
  choose,
}: {
  line: AgentLine;
  load: Promise<ModelChoice[] | null>;
  choose: (model: string, effort?: string) => void;
}) {
  const [models, setModels] = useState<ModelChoice[] | null | "loading">("loading");
  useEffect(() => {
    let live = true;
    setModels("loading");
    void load.then((m) => {
      if (live) setModels(m);
    });
    return () => {
      live = false;
    };
  }, [load]);
  if (models === "loading") {
    return (
      <p className="flex items-center gap-2 px-2 py-1.5 text-muted-foreground text-xs">
        <Loader2 className="size-3 animate-spin" /> Loading models…
      </p>
    );
  }
  if (models === null || models.length === 0) return <p className="px-2 py-1.5 text-muted-foreground text-xs">No models.</p>;
  return (
    <div className="max-h-56 overflow-y-auto">
      {models.map((m) => (
        <div key={m.id} className="menu-row flex items-center gap-1">
          <MenuItem className={cn("flex-1", m.id === line.model && "on font-medium")} onClick={() => choose(m.id)}>
            <span className="flex size-3.5 items-center justify-center">{m.id === line.model && <Check />}</span>
            <span className="truncate">{m.label}</span>
          </MenuItem>
          {(m.efforts ?? []).map((e) => (
            <button
              key={e}
              type="button"
              className={cn(
                "effort shrink-0 rounded px-1.5 py-0.5 text-2xs text-muted-foreground hover:bg-accent hover:text-foreground",
                m.id === line.model && e === line.effort && "bg-accent text-foreground",
              )}
              onClick={() => choose(m.id, e)}
            >
              {e}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}

function Mark({ kind, theme, className }: { kind: string; theme: Theme | null; className?: string }) {
  return (
    <span className={cn("grid size-4 shrink-0 place-items-center font-mono text-[11px]", className)} style={{ color: kindColour(kind) }}>
      {kindMark(theme, kind) || "●"}
    </span>
  );
}

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
        `agent agent-${status} flex h-6 max-w-56 shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-1.5 text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring`,
        on && "on bg-selected text-foreground",
      )}
      title={`${line.id}: ${line.model ?? "default model"} (${status})`}
      onClick={onToggle}
    >
      <span aria-hidden className="font-mono text-[11px]" style={{ color: agentColour(line.id, line.kind) }}>
        {kindMark(theme, line.kind) || "●"}
      </span>
      <span className="agent-name truncate font-mono text-2xs">{line.name}</span>
      {context !== null && (
        <span className="agent-context flex items-center" title={`${context}% of its context`}>
          <Ring pct={context} />
        </span>
      )}
      <span aria-hidden className={cn("agent-dot size-1.5 shrink-0 rounded-full", DOT[status])} />
      <span className="agent-status sr-only">{status}</span>
      {limit !== null && (
        <span
          className="agent-limit shrink-0 rounded bg-warning/14 px-1 font-mono text-3xs text-warning-foreground tabular-nums"
          title={`${line.kind} ${limit.label || limit.kind} window: ${Math.round(limit.usedPct)}% used${limit.resetsAt > 0 ? `. Resets ${resetTime(limit.resetsAt)}` : ""}`}
        >
          limit {Math.round(limit.usedPct)}%
        </span>
      )}
    </button>
  );
}

// The ghost "+": starts another agent of a kind, with a model and effort
// when the person picks one.
function AddAgent({
  roster,
  kinds,
  theme,
  onAdd,
  listModels,
  setModel,
}: {
  roster: AgentLine[];
  kinds: string[];
  theme: Theme | null;
  onAdd: (kind: string) => void;
  listModels: (kind: string) => Promise<ModelChoice[] | null>;
  setModel: (kind: string, model: string, effort?: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [models, setModels] = useState<{ kind: string; load: Promise<ModelChoice[] | null> } | null>(null);
  const close = () => {
    setOpen(false);
    setModels(null);
  };
  return (
    <Popover
      open={open}
      onClose={close}
      side="top"
      label="Add an agent"
      className="w-64"
      trigger={
        <button
          type="button"
          className="add-agent grid size-6 shrink-0 cursor-pointer place-items-center rounded-md text-muted-foreground outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected aria-expanded:text-foreground"
          aria-label="Add agent"
          title="Add agent"
          aria-expanded={open}
          onClick={() => (open ? close() : setOpen(true))}
        >
          <Plus className="size-3.5" />
        </button>
      }
    >
      <MenuLabel>Start another agent</MenuLabel>
      {kinds.map((k) => (
        <div key={k}>
          <div className="flex items-center">
            <MenuItem
              className="flex-1"
              aria-label={`Start another ${k}`}
              onClick={() => {
                close();
                onAdd(k);
              }}
            >
              <Mark kind={k} theme={theme} />
              <span className="flex-1">{k}</span>
              {roster.some((l) => l.kind === k) && <span className="text-2xs text-muted-foreground">another</span>}
            </MenuItem>
            <button
              type="button"
              className="grid size-7 shrink-0 cursor-pointer place-items-center rounded-md text-muted-foreground outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              aria-label={`Model for another ${k}`}
              title="Model and effort"
              aria-expanded={models?.kind === k}
              onClick={() => setModels(models?.kind === k ? null : { kind: k, load: listModels(k) })}
            >
              <ChevronRight className={cn("size-3.5 transition-transform", models?.kind === k && "rotate-90")} />
            </button>
          </div>
          {models?.kind === k && (
            <div className="ml-6">
              <Models
                line={{ id: k, name: k, kind: k, busy: false }}
                load={models.load}
                choose={(model, effort) => {
                  setModel(k, model, effort);
                  close();
                  onAdd(k);
                }}
              />
            </div>
          )}
        </div>
      ))}
      {kinds.length === 0 && <p className="px-2 py-1.5 text-muted-foreground text-xs">This Deck names no agents.</p>}
    </Popover>
  );
}

// The send-target picker under the composer: one chip per agent in the
// thread, then the ghost "+" to start another. hint says what Enter does
// while an agent works.
export function AgentsStrip({
  sessionId,
  roster,
  kinds,
  approvals,
  theme,
  api,
  agents,
  draft,
  setDraft,
  hint,
  onFocus,
  listModels,
  setModel,
}: {
  sessionId: string;
  roster: AgentLine[];
  kinds: string[];
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
  listModels: (kind: string) => Promise<ModelChoice[] | null>;
  setModel: (kind: string, model: string, effort?: string) => void;
}) {
  const limits = useLimits(api);
  const addressed = activeAgents(draft);
  return (
    <div className="agents flex min-w-0 flex-wrap items-center gap-0.5" role="group" aria-label="Send to">
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
      <AddAgent
        roster={roster}
        kinds={kinds}
        theme={theme}
        onAdd={(k) => {
          setDraft(addAgentMention(draft, k));
          onFocus();
        }}
        listModels={listModels}
        setModel={setModel}
      />
      {hint !== null && <span className="send-hint shrink-0 px-1.5 text-[11px] text-faint">{hint}</span>}
    </div>
  );
}
