import type { Theme } from "@web/protocol";
import type { ReactNode } from "react";
import type { AgentGraphNode } from "../../graph";
import { agentLane } from "../../lanes";
import { cn } from "../../lib/cn";
import { formatDuration } from "../../work";
import { AgentRole } from "../AgentRole";
import { CornerDownRight, MessageSquarePlus, Square, X } from "../icons";
import { Button } from "../kit/button";
import { Glyph, hueOf, STATUS_LABEL, StatusDot } from "./Nodes";

// The card of the agent picked on the graph, as an inspector: its status,
// then its model, context, cost and branch, what it runs and holds, as
// labelled rows; and what to do with it: show its messages alone, steer it,
// or stop it.

const STATUS_TEXT: Record<AgentGraphNode["status"], string> = {
  working: "text-foreground",
  waiting: "text-warning-foreground",
  error: "text-destructive-foreground",
  idle: "text-muted-foreground",
  exited: "text-muted-foreground",
};

const tokens = (n: number) => (n >= 1000 ? `${Math.round(n / 1000)}k` : String(n));

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-baseline gap-3">
      <dt className="w-14 shrink-0 text-2xs text-faint">{label}</dt>
      <dd className="min-w-0 flex-1 truncate text-xs tabular-nums">{children}</dd>
    </div>
  );
}

// Context use as a 1px track with the share filled.
function Meter({ pct }: { pct: number }) {
  return (
    <span aria-hidden className="relative inline-block h-[3px] w-14 overflow-hidden rounded-full bg-hairline-strong align-middle">
      <span className={cn("absolute inset-y-0 left-0 rounded-full", pct >= 85 ? "bg-warning" : "bg-muted-foreground")} style={{ width: `${pct}%` }} />
    </span>
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
        "graph-detail material absolute z-20 flex animate-enter flex-col rounded-[14px]",
        compact ? "inset-x-2 bottom-2" : "top-3 right-3 w-[312px]",
      )}
      style={hueOf(a.name, a.agentKind)}
    >
      <header className="flex items-start gap-2 px-3.5 pt-3 pb-2.5">
        <Glyph kind={a.agentKind} theme={theme} className="mt-px text-sm" />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h3 className="flex min-w-0 items-baseline gap-1.5 font-medium text-sm leading-tight">
            <span className="truncate">{a.name}</span>
            <AgentRole role={a.role} className="text-xs" />
          </h3>
          <p className="flex min-w-0 items-center gap-1.5 text-2xs">
            <StatusDot status={a.status} />
            <span className={STATUS_TEXT[a.status]}>{STATUS_LABEL[a.status]}</span>
            {a.since > 0 && a.status === "working" && <span className="text-faint tabular-nums">{formatDuration(now - a.since)}</span>}
            {parent !== null && <span className="min-w-0 truncate text-faint">· started by {parent}</span>}
          </p>
        </div>
        <Button variant="ghost-muted" size="icon-xs" className="-mt-0.5 -mr-1.5" aria-label="Close" onClick={onClose}>
          <X />
        </Button>
      </header>

      <dl className="flex flex-col gap-1.5 border-hairline border-t px-3.5 py-2.5">
        <Row label="Model">
          {a.model || "default"}
          {a.effort !== "" && <span className="text-muted-foreground"> · {a.effort}</span>}
        </Row>
        {a.contextPct !== null && (
          <Row label="Context">
            <span className="inline-flex items-center gap-2">
              <Meter pct={a.contextPct} />
              {a.contextPct}%<span className="text-faint">{tokens(a.contextTokens)} tokens</span>
            </span>
          </Row>
        )}
        <Row label="Cost">{a.costUsd > 0 ? `$${a.costUsd.toFixed(2)}` : "—"}</Row>
        {a.branch !== "" && (
          <Row label="Branch">
            <span className="font-mono">{a.branch}</span>
          </Row>
        )}
        {a.tools.map((t, i) => (
          <Row key={i} label={i === 0 ? "Running" : ""}>
            {/* The path gives way to the time, which always shows. */}
            <span className="flex min-w-0 items-baseline gap-1.5">
              <span className="min-w-0 truncate font-mono text-2xs" title={t.summary}>
                {t.subagent ? "subagent" : t.name} <span className="text-muted-foreground">{t.summary}</span>
              </span>
              {t.started > 0 && <span className="shrink-0 text-faint">{formatDuration(now - t.started)}</span>}
            </span>
          </Row>
        ))}
        {a.leases.length > 0 && (
          <div className="flex min-w-0 items-baseline gap-3">
            <dt className="w-14 shrink-0 text-2xs text-faint">Holds</dt>
            <dd className="min-w-0 flex-1">
              <ul aria-label={`Paths ${a.name} holds`} className="flex flex-col gap-0.5 font-mono text-2xs">
                {a.leases.map((p) => (
                  <li key={p} className="truncate">
                    {p}
                  </li>
                ))}
              </ul>
            </dd>
          </div>
        )}
      </dl>

      <footer className="flex items-center gap-0.5 border-hairline border-t px-1.5 py-1.5">
        <Button variant="ghost" size="xs" aria-pressed={shown} onClick={() => onLane(shown ? null : agentLane(a.name))}>
          <MessageSquarePlus />
          {shown ? "Show all messages" : "Show its messages"}
        </Button>
        <Button variant="ghost" size="xs" onClick={() => onSteer(a.name)}>
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
          <Square weight="fill" className="size-2.5" />
          Stop
        </Button>
      </footer>
    </section>
  );
}
