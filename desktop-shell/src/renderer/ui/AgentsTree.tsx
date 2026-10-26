import type { AgentLine, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { CornerDownRight, GitBranch, Loader2, Lock, Network } from "./icons";
import { agentColour, agentRole } from "../agents";
import type { AgentNode, Crew, RunningTool } from "../crew";
import { useRoomCrew } from "../crewState";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { formatDuration } from "../work";
import { AgentRole } from "./AgentRole";
import { EmptyState } from "./kit/empty";

// The Agents tab of a room, after the terminal UI's Agents pane: the agents
// as a tree, each with its model, effort, context, cost, leases and the
// tool it runs right now, in one card of hairline rows.

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
  exited: "border border-muted-foreground/50",
};

const STATUS_WORD: Record<string, string> = { tool: "running a tool", approval: "needs you" };

// One agent: its glyph in its hue, its name, its role and a status dot, its
// model, and in smaller type what it runs now, its branch, leases, context
// and cost.
function AgentRow({ a, running, theme, now, role }: { a: AgentNode; running: RunningTool[]; theme: Theme | null; now: number; role: string }) {
  const pct = a.contextWindow > 0 ? Math.round((a.contextTokens / a.contextWindow) * 100) : null;
  const busy = a.status === "working" || a.status === "tool";
  return (
    <li className="crew-agent flex flex-col gap-1.5 px-3.5 py-2.5" data-agent={a.name}>
      <div className="flex min-w-0 items-center gap-2 text-sm" style={{ paddingLeft: `${a.depth * 18}px` }}>
        {a.depth > 0 && <CornerDownRight className="-ml-[18px] size-3 shrink-0 text-faint" />}
        <span className="w-3.5 shrink-0 text-center font-mono text-xs" style={{ color: agentColour(a.name, a.kind) }}>
          {kindMark(theme, a.kind) || "●"}
        </span>
        <span className={cn("font-medium", a.status === "exited" && "text-muted-foreground")}>{a.name}</span>
        <AgentRole role={role} className="max-w-40 text-xs" />
        <span className={cn("size-1.5 shrink-0 rounded-full", DOT[a.status] ?? "bg-muted-foreground/40")} />
        <span className="min-w-0 truncate text-2xs text-muted-foreground">
          {a.model || "default"}
          {a.effort !== "" ? ` · ${a.effort}` : ""}
        </span>
        <span className={cn("agent-status ml-auto shrink-0 text-2xs", a.status === "approval" ? "text-warning-foreground" : "text-faint")}>
          {STATUS_WORD[a.status] ?? a.status}
          {a.since > 0 && busy ? ` · ${formatDuration(now - a.since)}` : ""}
        </span>
      </div>
      <div className="flex flex-col gap-1 pl-[22px]" style={{ marginLeft: `${a.depth * 18}px` }}>
        {running.map((t, i) => (
          <div key={i} className="flex min-w-0 items-center gap-1.5 font-mono text-2xs">
            <span className="shrink-0 text-foreground">{t.subagent ? "subagent" : t.name}</span>
            <span className="min-w-0 truncate text-muted-foreground">{t.summary}</span>
            {t.started > 0 && <span className="ml-auto shrink-0 font-sans text-faint tabular-nums">{formatDuration(now - t.started)}</span>}
          </div>
        ))}
        {(a.branch !== "" || a.leases !== "" || pct !== null || a.costUsd > 0) && (
          <div className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-1 text-2xs text-faint">
            {pct !== null && (
              <span className="flex items-center gap-1.5 tabular-nums" title={`${a.contextTokens} of ${a.contextWindow} tokens`}>
                <span className="relative h-[3px] w-10 overflow-hidden rounded-full bg-hairline-strong">
                  <span
                    className={cn("absolute inset-y-0 left-0 rounded-full", pct >= 85 ? "bg-warning" : "bg-muted-foreground")}
                    style={{ width: `${Math.min(100, pct)}%` }}
                  />
                </span>
                {pct}% context
              </span>
            )}
            {a.costUsd > 0 && <span className="tabular-nums">${a.costUsd.toFixed(2)}</span>}
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
          </div>
        )}
      </div>
    </li>
  );
}

export function AgentsTree({ crew, at, theme, roster = [] }: { crew: Crew | null; at: number; theme: Theme | null; roster?: AgentLine[] | undefined }) {
  const running = (name: string) => crew?.running.filter((t) => t.agent === name) ?? [];
  return (
    <section aria-label="Agents" className="flex flex-col gap-2 overflow-y-auto px-3 pt-1 pb-3">
      {crew === null ? (
        <p className="flex items-center gap-2 px-1 text-muted-foreground text-xs">
          <Loader2 className="size-3 animate-spin" /> Loading…
        </p>
      ) : crew.agents.length === 0 ? (
        <EmptyState icon={Network} title="No agents yet." hint="Send a message to start one." />
      ) : (
        <ul className="crew divide-y divide-hairline overflow-hidden rounded-xl bg-surface-1 ring-1 ring-hairline">
          {crew.agents.map((a) => (
            <AgentRow key={a.name} a={a} running={running(a.name)} theme={theme} now={at} role={agentRole(a.name, roster, [a])} />
          ))}
        </ul>
      )}
    </section>
  );
}
