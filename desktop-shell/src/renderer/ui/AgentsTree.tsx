import type { AgentLine, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { CornerDownRight, GitBranch, Loader2, Lock, Network } from "./icons";
import { agentRole } from "../agents";
import type { AgentNode, Crew, RunningTool } from "../crew";
import { useRoomCrew } from "../crewState";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { formatDuration } from "../work";
import { AgentRole } from "./AgentRole";
import { EmptyState } from "./kit/empty";

// The Agents tab of a room, after the terminal UI's Agents pane: the agents
// as a tree, each with its model, effort, context, cost, leases and the
// tool it runs right now.

// The room's agents tree, from the project's poller (crewState.ts). The
// lane strip and the Agents tab share it.
export function useCrew(api: ProjectApi | undefined, sessionId: string | null): { crew: Crew | null; at: number } {
  const room = useRoomCrew(api, sessionId);
  return { crew: room?.crew ?? null, at: room?.at ?? 0 };
}

const DOT: Record<string, string> = {
  working: "bg-signal animate-deck-pulse",
  tool: "bg-signal animate-deck-pulse",
  approval: "bg-warning",
  error: "bg-destructive",
  exited: "bg-muted-foreground/30",
};

function AgentRow({ a, running, theme, now, role }: { a: AgentNode; running: RunningTool[]; theme: Theme | null; now: number; role: string }) {
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
        <AgentRole role={role} className="max-w-40 text-xs" />
        <span className="min-w-0 truncate font-mono text-2xs text-muted-foreground">
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
      {(a.branch !== "" || a.leases !== "" || pct !== null || a.costUsd > 0) && (
        <div className="ml-6 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-2xs text-muted-foreground">
          {a.branch !== "" && (
            <span className="flex items-center gap-1 font-mono">
              <GitBranch className="size-3" />
              {a.branch}
            </span>
          )}
          {a.leases !== "" && (
            <span className="flex min-w-0 items-center gap-1" title="Files it claimed">
              <Lock className="size-3 shrink-0" />
              <span className="truncate font-mono">{a.leases}</span>
            </span>
          )}
          {pct !== null && (
            <span className="flex items-center gap-1.5 tabular-nums" title={`${a.contextTokens} of ${a.contextWindow} tokens`}>
              <span className="h-1 w-12 overflow-hidden rounded-full bg-muted">
                <span className={cn("block h-full rounded-full", pct >= 85 ? "bg-warning" : "bg-muted-foreground")} style={{ width: `${Math.min(100, pct)}%` }} />
              </span>
              {pct}% context
            </span>
          )}
          {a.costUsd > 0 && <span className="tabular-nums">${a.costUsd.toFixed(2)}</span>}
        </div>
      )}
    </li>
  );
}

export function AgentsTree({ crew, at, theme, roster = [] }: { crew: Crew | null; at: number; theme: Theme | null; roster?: AgentLine[] | undefined }) {
  const running = (name: string) => crew?.running.filter((t) => t.agent === name) ?? [];
  return (
    <section aria-label="Agents" className="flex flex-col gap-2 overflow-y-auto p-3">
      {crew === null ? (
        <p className="flex items-center gap-2 px-1 text-muted-foreground text-xs">
          <Loader2 className="size-3 animate-spin" /> Loading…
        </p>
      ) : crew.agents.length === 0 ? (
        <EmptyState icon={Network} title="No agents yet." hint="Send a message to start one." />
      ) : (
        <ul className="crew divide-y divide-border/50 rounded-xl border bg-card/40 px-3">
          {crew.agents.map((a) => (
            <AgentRow key={a.name} a={a} running={running(a.name)} theme={theme} now={at} role={agentRole(a.name, roster)} />
          ))}
        </ul>
      )}
    </section>
  );
}
