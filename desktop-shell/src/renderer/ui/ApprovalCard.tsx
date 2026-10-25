import type { Approval, Decision } from "@web/protocol";
import { approvalSummary, DECISIONS } from "../approvals";
import { cn } from "../lib/cn";
import { DiffView } from "./DiffView";
import { Button } from "./kit/button";

// "You may answer.", "You and vps may answer.", "You, vps and lab may answer."
export function mayAnswer(who: readonly string[]): string {
  if (who.length === 0) return "";
  const names = who.length === 1 ? who[0] : `${who.slice(0, -1).join(", ")} and ${who[who.length - 1]}`;
  return `${names} may answer.`;
}

function kindLabel(a: Approval): string {
  return a.kind === "command" ? "Command approval" : a.kind === "edit" ? "File change approval" : "Approval";
}

// A request from an agent, after T3 Code's pending approval panel: the
// kind and agent on top, what it wants to run or change, then the answers.
// Attached above the composer for the open thread; in the side panel for
// every thread.
export function ApprovalCard({
  a,
  onAnswer,
  index,
  count,
  answerers,
  className,
}: {
  a: Approval;
  onAnswer: (d: Decision) => void;
  index?: number;
  count?: number;
  // Who may answer: you, and the teammates the thread is shared with as editors.
  answerers?: readonly string[];
  className?: string;
}) {
  return (
    <div
      role="group"
      aria-label={kindLabel(a)}
      className={cn("approval flex flex-col gap-2 rounded-2xl border bg-card px-4 py-3", className)}
      data-approval={a.id}
    >
      <span className="approval-head flex w-full min-w-0 items-center gap-2 text-2xs text-muted-foreground">
        <span className="shrink-0 font-medium text-warning">{kindLabel(a)}</span>
        <span className="approval-agent min-w-0 truncate">
          {a.agent}
          {a.description !== "" && a.description !== approvalSummary(a) ? ` · ${a.description}` : ""}
        </span>
        {count !== undefined && count > 1 && (
          <span className="ml-auto shrink-0 tabular-nums">
            {(index ?? 0) + 1}/{count}
          </span>
        )}
      </span>
      <code className="approval-what block max-h-40 w-full min-w-0 overflow-auto whitespace-pre-wrap break-all font-mono text-foreground text-xs">
        {approvalSummary(a)}
      </code>
      {a.diff !== undefined && a.diff !== "" && (
        <div className="max-h-60 overflow-auto rounded-lg border">
          <DiffView diff={a.diff} truncated={a.diff_truncated} numbered />
        </div>
      )}
      <div className="approval-actions flex flex-wrap items-center justify-end gap-1.5">
        {answerers !== undefined && answerers.length > 0 && (
          <span className="approval-who mr-auto min-w-0 truncate text-2xs text-faint">{mayAnswer(answerers)}</span>
        )}
        {[...DECISIONS].reverse().map((d) => (
          <Button
            key={d.decision}
            size="xs"
            variant={d.decision === "allow" ? "default" : d.decision === "deny" ? "ghost-muted" : "outline"}
            className={`decide decide-${d.decision}`}
            onClick={() => onAnswer(d.decision)}
          >
            {d.label}
          </Button>
        ))}
      </div>
    </div>
  );
}
