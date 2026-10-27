import type { Link } from "@web/state";
import { BarChart3, Settings, Unplug, Users } from "./icons";
import { cn } from "../lib/cn";
import type { SettingsSection } from "../ui";
import { Button } from "./kit/button";
import { NotificationBell } from "./NotificationBell";

const LINK_LABEL: Record<Link, string> = { online: "Connected", connecting: "Connecting…", offline: "Offline" };

// The connection's dot: green while it is live, amber and pulsing while it
// connects, red when it is gone.
const linkDot: Record<Link, string> = {
  online: "bg-success",
  connecting: "bg-warning animate-deck-pulse",
  offline: "bg-destructive",
};

// The connection and the teammates: a dot or a glyph and a word, with no
// container; the word brightens on hover.
const quiet =
  "flex h-6 shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-1 text-2xs text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring";

// The sidebar's footer: Settings, Usage, Team, the notifications' bell and
// Close project (Search at the top opens the commands), then an update
// that waits, the connection and the teammates online. The connection is a
// live dot and a word; the word gives way when the footer is narrower than
// 15rem.
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
    <footer className="side-foot @container relative flex h-12 shrink-0 items-center gap-0.5 px-2">
      <Button
        variant="ghost-muted"
        size="icon-sm"
        aria-label="Settings"
        title="Settings"
        aria-pressed={settings !== null}
        onClick={() => onSettings(settings === null ? "general" : null)}
      >
        <Settings weight={settings !== null ? "fill" : "regular"} />
      </Button>
      <Button variant="ghost-muted" size="icon-sm" aria-label="Usage" title="Usage" aria-pressed={usage} onClick={onUsage}>
        <BarChart3 weight={usage ? "fill" : "regular"} />
      </Button>
      <Button variant="ghost-muted" size="icon-sm" aria-label="Team" title="Team" aria-pressed={team} onClick={onTeam}>
        <Users weight={team ? "fill" : "regular"} />
      </Button>
      <NotificationBell onSettings={() => onSettings("general")} />
      <Button variant="ghost-muted" size="icon-sm" aria-label="Close project" title="Close project" onClick={onDisconnect}>
        <Unplug />
      </Button>
      {update !== null && (
        <button
          type="button"
          className="update ml-auto h-6 min-w-0 cursor-pointer truncate rounded-md px-1 font-medium text-2xs text-info-foreground hover:underline"
          title={`Deck ${update.version} is downloaded. Restart to install it.`}
          onClick={update.onRestart}
        >
          Update ready · Restart
        </button>
      )}
      <span className={cn("flex shrink-0 items-center gap-1", update === null ? "ml-auto" : "ml-1")}>
        {online !== null && (
          <button
            type="button"
            className={cn("presence", quiet)}
            aria-label={`${online} ${online === 1 ? "teammate" : "teammates"} online`}
            title="Open the team"
            onClick={onTeam}
          >
            <Users aria-hidden className="size-3" />
            <span className="tabular-nums">{online}</span>
          </button>
        )}
        <button type="button" className={cn("link", quiet)} title={`${LINK_LABEL[link]}. Open the connection settings.`} onClick={() => onSettings("connection")}>
          <span aria-hidden className={cn("dot size-1.5 shrink-0 rounded-full", linkDot[link])} />
          <span className={cn("sr-only @min-[15rem]:not-sr-only", update !== null && "@min-[15rem]:sr-only")}>{LINK_LABEL[link]}</span>
        </button>
      </span>
    </footer>
  );
}
