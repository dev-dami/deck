import type { AgentLine, Approval, Decision, Session } from "@web/protocol";
import { sessionTitle } from "@web/state";

export type AgentStatus = "waiting" | "busy" | "idle";

// An agent waits when one of its approvals is open; else it is busy or idle
// as the roster says. Waiting wins: it is the state a person must act on.
export function agentStatus(line: AgentLine, sessionId: string, approvals: Approval[]): AgentStatus {
  if (approvals.some((a) => a.session_id === sessionId && a.agent === line.id)) return "waiting";
  return line.busy ? "busy" : "idle";
}

export interface ApprovalGroup {
  sessionId: string;
  title: string;
  items: Approval[];
}

// The inbox: approvals of every session, the open session first, then the
// other sessions in the order of their oldest approval.
export function groupApprovals(
  approvals: Approval[],
  sessions: Session[],
  openId: string | null,
): ApprovalGroup[] {
  const groups = new Map<string, ApprovalGroup>();
  for (const a of approvals) {
    let g = groups.get(a.session_id);
    if (g === undefined) {
      g = {
        sessionId: a.session_id,
        title: sessionTitle(sessions.find((s) => s.id === a.session_id)),
        items: [],
      };
      groups.set(a.session_id, g);
    }
    g.items.push(a);
  }
  const list = [...groups.values()];
  const open = list.filter((g) => g.sessionId === openId);
  return [...open, ...list.filter((g) => g.sessionId !== openId)];
}

// The one line that says what the agent wants to do.
export function approvalSummary(a: Approval): string {
  if (a.kind === "command" && a.command !== undefined && a.command !== "") return a.command;
  if (a.kind === "edit" && a.path !== undefined && a.path !== "") return a.path;
  return a.description;
}

export const DECISIONS: ReadonlyArray<{ decision: Decision; label: string }> = [
  { decision: "allow", label: "Allow" },
  { decision: "allow_session", label: "Allow for session" },
  { decision: "deny", label: "Deny" },
];
