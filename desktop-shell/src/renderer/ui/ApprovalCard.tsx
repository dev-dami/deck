import type { Approval, Decision } from "@web/protocol";
import { approvalSummary, DECISIONS } from "../approvals";
import { cn } from "../lib/cn";
import { DiffView } from "./DiffView";
import { ShieldQuestion } from "./icons";
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

// A request from an agent: "needs you" in the warning colour with its
// shield, the agent and what it wants to run or change in a well, then the
// answers. Stacked over the composer for the open thread, as a card of
// thick glass.
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
  const command = a.kind === "command";
  return (
    <div
      role="group"
      aria-label={kindLabel(a)}
      className={cn("approval flex flex-col gap-2.5 rounded-2xl bg-surface-1 px-3.5 pt-3 pb-2.5 ring-1 ring-hairline", className)}
      data-approval={a.id}
    >
      <span className="approval-head flex w-full min-w-0 items-center gap-2.5">
        <span aria-hidden className="grid size-6 shrink-0 place-items-center rounded-full bg-warning/15 text-warning-foreground">
          <ShieldQuestion className="size-3.5" weight="fill" />
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="font-medium text-foreground text-xs">{kindLabel(a)}</span>
          <span className="approval-agent min-w-0 truncate text-2xs text-muted-foreground">
            <span className="font-mono">{a.agent}</span>
            {a.description !== "" && a.description !== approvalSummary(a) ? ` · ${a.description}` : ""}
          </span>
        </span>
        {count !== undefined && count > 1 && (
          <span className="ml-auto shrink-0 rounded-full bg-hover px-2 py-0.5 text-2xs text-muted-foreground tabular-nums">
            {(index ?? 0) + 1} of {count}
          </span>
        )}
      </span>
      <div className="flex max-h-40 min-w-0 gap-2 overflow-auto rounded-xl bg-canvas/60 px-3 py-2 ring-1 ring-hairline">
        {command && (
          <span aria-hidden className="shrink-0 font-mono text-code text-faint">
            $
          </span>
        )}
        <code className="approval-what block min-w-0 flex-1 whitespace-pre-wrap break-all font-mono text-code text-foreground">{approvalSummary(a)}</code>
      </div>
      {a.diff !== undefined && a.diff !== "" && (
        <div className="max-h-60 overflow-auto rounded-xl ring-1 ring-hairline">
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
            size="sm"
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
