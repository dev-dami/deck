import type { Approval, Decision } from "@web/protocol";
import { FilePen, ShieldAlert, SquareTerminal } from "lucide-react";
import { approvalSummary, DECISIONS } from "../approvals";
import { cn } from "../lib/cn";
import { DiffView } from "./DiffView";
import { Button } from "./kit/button";

// One request from an agent to run a command or make an edit, with the
// three answers. Above the composer for the open session (as T3 Code shows
// pending approvals) and in the side panel for every session.
export function ApprovalCard({
  a,
  onAnswer,
  className,
}: {
  a: Approval;
  onAnswer: (d: Decision) => void;
  className?: string;
}) {
  const Icon = a.kind === "command" ? SquareTerminal : a.kind === "edit" ? FilePen : ShieldAlert;
  const verb = a.kind === "command" ? "wants to run" : a.kind === "edit" ? "wants to edit" : "asks";
  return (
    <div
      className={cn("approval overflow-hidden rounded-2xl border border-warning/30 bg-card", className)}
      data-approval={a.id}
    >
      <div className="approval-head flex items-center gap-2 px-4 pt-3 text-xs">
        <Icon className="size-3.5 text-warning-foreground" />
        <span className="approval-agent font-medium">{a.agent}</span>
        <span className="text-muted-foreground">{verb}</span>
        {a.description !== "" && a.description !== approvalSummary(a) && (
          <span className="min-w-0 truncate text-muted-foreground" title={a.description}>
            · {a.description}
          </span>
        )}
      </div>
      <pre className="approval-what mx-4 mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-code px-3 py-2 font-mono text-xs">
        {approvalSummary(a)}
      </pre>
      {a.diff !== undefined && a.diff !== "" && (
        <div className="mx-4 mt-2 max-h-60 overflow-auto rounded-lg border">
          <DiffView diff={a.diff} truncated={a.diff_truncated} />
        </div>
      )}
      <div className="approval-actions flex flex-wrap items-center justify-end gap-1.5 px-4 pt-3 pb-3">
        {DECISIONS.map((d) => (
          <Button
            key={d.decision}
            size="sm"
            variant={d.decision === "allow" ? "default" : d.decision === "deny" ? "destructive-outline" : "outline"}
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
