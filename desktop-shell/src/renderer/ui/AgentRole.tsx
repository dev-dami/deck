import { cn } from "../lib/cn";
import { useTeamOn } from "../teamGate";

// A named member's one-line role (agentRole in agents.ts), set after its
// glyph and name in faint type: the name leads and the kind's glyph still
// shows what runs it. Nothing when the member has no role, or with the
// team off (teamGate.ts).
export function AgentRole({ role, className }: { role: string; className?: string }) {
  const on = useTeamOn();
  if (role === "" || !on) return null;
  return (
    <span className={cn("agent-role min-w-0 truncate font-normal font-sans text-faint", className)} title={role}>
      {role}
    </span>
  );
}
