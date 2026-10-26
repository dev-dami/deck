import type { Theme } from "@web/protocol";
import type { AgentGraphNode } from "../../graph";
import { agentLane } from "../../lanes";
import { cn } from "../../lib/cn";
import { formatDuration } from "../../work";
import { Coins, CornerDownRight, GitBranch, Lock, MessageSquarePlus, Square, Wrench, X } from "../icons";
import { Button } from "../kit/button";
import { AgentMark, hueOf, STATUS_LABEL } from "./Nodes";

// The card of the agent picked on the graph: its status, model, context,
// cost, branch, what it runs and holds, and what to do with it: show its
// messages alone, steer it, or stop it.

const STATUS_TEXT: Record<AgentGraphNode["status"], string> = {
  working: "text-signal",
  waiting: "text-warning-foreground",
  error: "text-destructive-foreground",
  idle: "text-muted-foreground",
  exited: "text-muted-foreground",
};

const tokens = (n: number) => (n >= 1000 ? `${Math.round(n / 1000)}k` : String(n));

// Context use as a ring big enough to read, with its share in the middle.
function ContextRing({ pct }: { pct: number }) {
  const r = 15;
  const c = 2 * Math.PI * r;
  return (
    <span className="relative grid size-10 shrink-0 place-items-center">
      <svg aria-hidden viewBox="0 0 36 36" className="absolute inset-0 -rotate-90">
        <circle cx="18" cy="18" r={r} fill="none" strokeWidth="3" className="stroke-hairline-strong" />
        <circle
          cx="18"
          cy="18"
          r={r}
          fill="none"
          strokeWidth="3"
          strokeLinecap="round"
          strokeDasharray={`${(c * pct) / 100} ${c}`}
          className={pct >= 85 ? "stroke-warning" : "stroke-(--hue)"}
        />
      </svg>
      <span className="font-medium text-2xs tabular-nums">{pct}%</span>
    </span>
  );
}

function Stat({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-0.5", className)}>
      <span className="text-3xs text-faint">{label}</span>
      <span className="min-w-0 truncate text-xs tabular-nums">{children}</span>
    </div>
  );
}

export function AgentDetail({
  a,
  parent,
  theme,
  lane,
  now,
  compact,
  onLane,
  onSteer,
  onStop,
  onClose,
}: {
  a: AgentGraphNode;
  // The name of the agent that started it.
  parent: string | null;
  theme: Theme | null;
  lane: string | null;
  now: number;
  compact: boolean;
  onLane: (lane: string | null) => void;
  onSteer: (agent: string) => void;
  onStop: (agent: string) => void;
  onClose: () => void;
}) {
  const shown = lane === agentLane(a.name);
  const busy = a.status === "working" || a.status === "waiting";
  return (
    <section
      data-graph-control=""
      aria-label={`${a.name} details`}
      className={cn(
        "graph-detail material absolute z-20 flex animate-enter flex-col gap-3 rounded-[18px] p-3.5",
        compact ? "inset-x-2 bottom-2" : "top-3 right-3 w-[296px]",
      )}
      style={hueOf(a.name, a.agentKind)}
    >
      <header className="flex items-center gap-3">
        <AgentMark kind={a.agentKind} theme={theme} status={a.status} live />
        <div className="flex min-w-0 flex-1 flex-col">
          <h3 className="truncate font-medium text-sm">{a.name}</h3>
          <p className="flex min-w-0 items-center gap-1.5 text-2xs">
            <span className={cn("font-medium", STATUS_TEXT[a.status])}>{STATUS_LABEL[a.status]}</span>
            {a.since > 0 && a.status === "working" && <span className="text-faint tabular-nums">for {formatDuration(now - a.since)}</span>}
            {parent !== null && <span className="truncate text-faint">· started by {parent}</span>}
          </p>
        </div>
        <Button variant="ghost-muted" size="icon-xs" aria-label="Close" onClick={onClose}>
          <X />
        </Button>
      </header>

      <div className="flex items-center gap-3 rounded-xl bg-surface-1 px-3 py-2.5 ring-1 ring-hairline">
        {a.contextPct !== null && <ContextRing pct={a.contextPct} />}
        <div className="grid min-w-0 flex-1 grid-cols-2 gap-x-3 gap-y-2">
          <Stat label="Model">
            {a.model || "default"}
            {a.effort !== "" && <span className="text-muted-foreground"> · {a.effort}</span>}
          </Stat>
          <Stat label="Cost">
            <span className="inline-flex items-center gap-1">
              <Coins className="size-3 text-faint" />
              {a.costUsd > 0 ? `$${a.costUsd.toFixed(2)}` : "—"}
            </span>
          </Stat>
          {a.contextPct !== null && <Stat label="Context">{tokens(a.contextTokens)} tokens</Stat>}
          {a.branch !== "" && (
            <Stat label="Branch">
              <span className="inline-flex min-w-0 items-center gap-1 font-mono">
                <GitBranch className="size-3 shrink-0 text-faint" />
                <span className="truncate">{a.branch}</span>
              </span>
            </Stat>
          )}
        </div>
      </div>

      {a.tools.length > 0 && (
        <ul aria-label="Running" className="flex flex-col gap-1">
          {a.tools.map((t, i) => (
            <li key={i} className="flex min-w-0 items-center gap-2 text-xs">
              <Wrench className="size-3.5 shrink-0 text-(--hue)" />
              <span className="shrink-0 font-medium">{t.subagent ? "subagent" : t.name}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">{t.summary}</span>
              {t.started > 0 && <span className="shrink-0 text-faint tabular-nums">{formatDuration(now - t.started)}</span>}
            </li>
          ))}
        </ul>
      )}

      {a.leases.length > 0 && (
        <ul aria-label={`Paths ${a.name} holds`} className="flex flex-wrap gap-1">
          {a.leases.map((p) => (
            <li key={p} className="flex h-5 max-w-full items-center gap-1 rounded-full bg-hover px-2 font-mono text-2xs text-muted-foreground">
              <Lock className="size-3 shrink-0" />
              <span className="truncate">{p}</span>
            </li>
          ))}
        </ul>
      )}

      <footer className="flex items-center gap-1.5">
        <Button variant="outline" size="xs" aria-pressed={shown} onClick={() => onLane(shown ? null : agentLane(a.name))}>
          <MessageSquarePlus />
          {shown ? "Show all messages" : "Show its messages"}
        </Button>
        <Button variant="ghost-muted" size="xs" onClick={() => onSteer(a.name)}>
          <CornerDownRight />
          Steer
        </Button>
        <Button
          variant="ghost-muted"
          size="xs"
          className="ml-auto hover:text-destructive-foreground"
          disabled={!busy}
          aria-label={`Stop ${a.name}`}
          onClick={() => onStop(a.name)}
        >
          <Square weight="fill" className="size-3" />
          Stop
        </Button>
      </footer>
    </section>
  );
}
