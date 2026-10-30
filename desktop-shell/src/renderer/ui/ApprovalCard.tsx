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
  if (a.kind === "command") return "Command approval";
  if (a.kind === "edit") return "File change approval";
  return a.kind === "browser_origin" ? "Website access" : "Approval";
}

// A request from an agent: "needs you" in the warning colour with its
// shield, the agent, what it wants to run or change in a recessed line,
// then the answers. Stacked over the composer for the open thread, as a card of
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
      className={cn("approval card flex flex-col gap-2.5 rounded-2xl bg-surface-1 px-3.5 pt-3 pb-2.5 ring-1 ring-hairline", className)}
      data-approval={a.id}
    >
      <span className="approval-head flex w-full min-w-0 items-baseline gap-2 text-xs">
        <ShieldQuestion aria-hidden className="size-3.5 shrink-0 self-center text-warning-foreground" weight="fill" />
        <span className="shrink-0 font-medium text-warning-foreground">{kindLabel(a)}</span>
        <span className="approval-agent min-w-0 truncate text-muted-foreground">
          <span className="font-mono">{a.agent}</span>
          {a.description !== "" && a.description !== approvalSummary(a) ? ` · ${a.description}` : ""}
        </span>
        {count !== undefined && count > 1 && (
          <span className="ml-auto shrink-0 text-2xs text-faint tabular-nums">
            {(index ?? 0) + 1} of {count}
          </span>
        )}
      </span>
      <div className="flex max-h-40 min-w-0 gap-2 overflow-auto rounded-lg bg-hover px-3 py-2">
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
