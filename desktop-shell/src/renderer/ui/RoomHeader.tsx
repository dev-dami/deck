import type { AgentLine, ChangedFile, Session, Theme } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { kindMark } from "@web/theme";
import { FileDiff, PanelLeft, PanelRight, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { agentColour } from "../agents";
import type { Team } from "../crew";
import { shortId } from "../lanes";
import type { ProjectApi } from "../project";
import { type Git, GitActions } from "./Git";
import { Button } from "./kit/button";
import { ShareStack } from "./Team";

// The room header, one row: the room's corner index and title, the git
// actions, who follows the room, and the side panel's toggles.
export function RoomHeader({
  sidebar,
  onShowSidebar,
  project,
  open,
  loaded,
  roster,
  theme,
  onRename,
  api,
  git,
  team,
  shares,
  onShare,
  files,
  terminal,
  terminalKey,
  onTerminal,
  diff,
  panelOpen,
  onDiff,
  onPanel,
  onError,
  onInfo,
}: {
  sidebar: boolean;
  onShowSidebar: () => void;
  project: string;
  open: Session | undefined;
  // The thread list arrived: without a thread, the room is a new one.
  loaded: boolean;
  roster: AgentLine[];
  theme: Theme | null;
  onRename: (title: string) => void;
  api: ProjectApi | undefined;
  git: Git;
  team: Team | string | null;
  shares: Record<string, string>;
  onShare: (peer: string, role: string) => Promise<string | null>;
  files: ChangedFile[] | null;
  terminal: boolean;
  terminalKey: string;
  onTerminal: () => void;
  // The Changes tab shows.
  diff: boolean;
  panelOpen: boolean;
  onDiff: () => void;
  onPanel: () => void;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const first = roster[0];
  const added = files?.reduce((n, f) => n + f.added, 0) ?? 0;
  const removed = files?.reduce((n, f) => n + f.removed, 0) ?? 0;
  const title = open === undefined ? (loaded ? "New thread" : "Connecting…") : sessionTitle(open);
  return (
    <header className="topbar drag @container/top flex h-12 shrink-0 items-center gap-2 px-4">
      {!sidebar && (
        <Button variant="ghost-muted" size="icon-sm" aria-label="Show the sidebar" title="Show the sidebar" onClick={onShowSidebar}>
          <PanelLeft />
        </Button>
      )}
      <div className="flex min-w-28 flex-1 items-center gap-2.5 text-sm">
        {open !== undefined && (
          // The corner index: the first agent's glyph and the room's short id, in that agent's hue.
          <span
            className="room-index inline-flex shrink-0 items-center gap-1 font-mono text-2xs"
            style={{ color: first === undefined ? "var(--text-faint)" : agentColour(first.id, first.kind) }}
            title={open.id}
          >
            <span aria-hidden>{first === undefined ? "●" : kindMark(theme, first.kind) || "●"}</span>
            <span>{shortId(open.id)}</span>
          </span>
        )}
        <span className="project max-w-24 shrink truncate text-muted-foreground @3xl/top:max-w-40">{project || "Deck"}</span>
        <span aria-hidden className="text-faint">
          /
        </span>
        {renaming && open !== undefined ? (
          <input
            autoFocus
            defaultValue={open.title}
            aria-label="Thread title"
            onFocus={(e) => e.currentTarget.select()}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                const t = e.currentTarget.value.trim();
                if (t !== "") onRename(t);
                setRenaming(false);
              } else if (e.key === "Escape") setRenaming(false);
            }}
            onBlur={() => setRenaming(false)}
            className="min-w-0 flex-1 rounded-sm border border-input bg-card px-1 font-medium text-sm outline-none focus:border-primary/50 focus:ring-[3px] focus:ring-ring"
          />
        ) : (
          <h1
            className="title min-w-0 flex-1 truncate font-medium text-foreground"
            title={open === undefined ? undefined : "Double-click to rename"}
            onDoubleClick={() => open !== undefined && setRenaming(true)}
          >
            {title}
          </h1>
        )}
      </div>
      {api !== undefined && <GitActions git={git} project={api} threadTitle={open === undefined ? "" : sessionTitle(open)} onError={onError} onInfo={onInfo} />}
      {api !== undefined && open !== undefined && <ShareStack team={team} shares={shares} onSet={onShare} onError={onError} />}
      <div className="panel-toggles flex shrink-0 items-center gap-1">
        {api !== undefined && (
          <Button variant="outline" size="icon-sm" aria-label="Terminal" title={`Terminal (${terminalKey})`} aria-pressed={terminal} onClick={onTerminal}>
            <SquareTerminal />
          </Button>
        )}
        <Button variant="outline" size="sm" className="font-normal" aria-pressed={diff} onClick={onDiff}>
          <FileDiff />
          <span className="sr-only @3xl/top:not-sr-only">Diff</span>
          {files !== null && files.length > 0 && (
            <span className="font-mono text-xs">
              <span className="text-diff-addition-foreground">+{added}</span> <span className="text-diff-deletion-foreground">−{removed}</span>
            </span>
          )}
        </Button>
        <Button
          variant="ghost-muted"
          size="icon-sm"
          aria-label={panelOpen ? "Hide the side panel" : "Show the side panel"}
          title={panelOpen ? "Hide the side panel" : "Show the side panel"}
          aria-pressed={panelOpen}
          onClick={onPanel}
        >
          <PanelRight />
        </Button>
      </div>
    </header>
  );
}
