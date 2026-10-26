import type { Link } from "@web/state";
import { BarChart3, Settings, Unplug, Users } from "lucide-react";
import { cn } from "../lib/cn";
import type { SettingsSection } from "../ui";
import { Button } from "./kit/button";

const LINK_LABEL: Record<Link, string> = { online: "Connected", connecting: "Connecting…", offline: "Offline" };

const linkDot: Record<Link, string> = {
  online: "bg-success",
  connecting: "bg-warning animate-deck-pulse",
  offline: "bg-destructive",
};

const quiet =
  "flex shrink-0 cursor-pointer items-center gap-1.5 rounded px-1 text-2xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring";

// The sidebar's footer: Settings, Usage, Team and Close project (Search at
// the top opens the commands), then an update that waits, the connection
// and the teammates online. The connection is a dot and a word; the word
// gives way when the footer is narrower than 15rem.
export function SidebarFooter({
  link,
  settings,
  usage,
  team,
  online,
  update,
  onSettings,
  onUsage,
  onTeam,
  onDisconnect,
}: {
  link: Link;
  settings: SettingsSection | null;
  usage: boolean;
  team: boolean;
  // Teammates online, or null without a team link.
  online: number | null;
  update: { version: string; onRestart: () => void } | null;
  onSettings: (s: SettingsSection | null) => void;
  onUsage: () => void;
  onTeam: () => void;
  onDisconnect: () => void;
}) {
  return (
    <footer className="side-foot @container flex items-center gap-0.5 px-2 py-2">
      <Button variant="ghost-muted" size="icon-sm" aria-label="Settings" title="Settings" onClick={() => onSettings(settings === null ? "general" : null)}>
        <Settings />
      </Button>
      <Button variant="ghost-muted" size="icon-sm" aria-label="Usage" title="Usage" aria-pressed={usage} onClick={onUsage}>
        <BarChart3 />
      </Button>
      <Button variant="ghost-muted" size="icon-sm" aria-label="Team" title="Team" aria-pressed={team} onClick={onTeam}>
        <Users />
      </Button>
      <Button variant="ghost-muted" size="icon-sm" aria-label="Close project" title="Close project" onClick={onDisconnect}>
        <Unplug />
      </Button>
      {update !== null && (
        <button
          type="button"
          className="update ml-auto min-w-0 cursor-pointer truncate rounded px-1 text-2xs text-muted-foreground hover:text-foreground"
          title={`Deck ${update.version} is downloaded. Restart to install it.`}
          onClick={update.onRestart}
        >
          Update ready · Restart
        </button>
      )}
      <button
        type="button"
        className={cn("link h-6", quiet, update === null && "ml-auto")}
        title={`${LINK_LABEL[link]}. Open the connection settings.`}
        onClick={() => onSettings("connection")}
      >
        <span aria-hidden className={cn("dot size-1.5 shrink-0 rounded-full", linkDot[link])} />
        <span className="sr-only @min-[15rem]:not-sr-only">{LINK_LABEL[link]}</span>
      </button>
      {online !== null && (
        <button
          type="button"
          className={cn("presence h-6", quiet)}
          aria-label={`${online} ${online === 1 ? "teammate" : "teammates"} online`}
          title="Open the team"
          onClick={onTeam}
        >
          <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", online > 0 ? "bg-success" : "bg-muted-foreground/40")} />
          <span className="tabular-nums">{online}</span>
        </button>
      )}
    </footer>
  );
}
