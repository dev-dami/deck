import type { AgentLine, ChangedFile, Theme } from "@web/protocol";
import type { ProjectApi } from "../../project";

// The Work Graph (docs/design-glass.md, section 7): one thread's agents,
// sub-agents, tasks and files as an interactive map.
export interface WorkGraphProps {
  // The local project, for the crew and the board (useCrew, crewOf); a
  // paired Deck has neither, and the graph draws the roster and files.
  api: ProjectApi | undefined;
  sid: string;
  roster: AgentLine[];
  files: ChangedFile[] | null;
  theme: Theme | null;
  // The lane filter in force (lanes.ts: agentLane(name)), and how to set
  // it: "Show its messages" filters the conversation to an agent.
  lane: string | null;
  onLane: (lane: string | null) => void;
  onOpenFile: (path: string) => void;
  onOpenTasks: () => void;
  onSteer: (agent: string) => void;
  onStop: (agent: string) => void;
  // A narrow host (the side panel) drops the minimap and the legend.
  compact?: boolean;
}

export function WorkGraph(_props: WorkGraphProps) {
  return <div className="work-graph flex min-h-0 flex-1" />;
}
