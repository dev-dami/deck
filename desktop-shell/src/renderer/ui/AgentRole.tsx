import { cn } from "../lib/cn";

// A named member's one-line role (agentRole in agents.ts), set after its
// glyph and name in faint type: the name leads and the kind's glyph still
// shows what runs it. Nothing when the member has no role.
export function AgentRole({ role, className }: { role: string; className?: string }) {
  if (role === "") return null;
  return (
    <span className={cn("agent-role min-w-0 truncate font-normal font-sans text-faint", className)} title={role}>
      {role}
    </span>
  );
}
