import type { HighlightItem } from "../../shared/bridge";
import type { DeckClient } from "../client";
import { serves } from "../client";
import { highlightCounts } from "../highlights";
import { useRoomLive } from "../live";
import { cn } from "../lib/cn";
import { CircleAlert, Flag } from "./icons";
import { Button } from "./kit/button";

// The composer's Highlights button: the number of open items, a dot while
// one is unread. It shows only for a Deck that serves Highlights.
export function HighlightsPill({ c, sid, onOpen }: { c: DeckClient; sid: string; onOpen: () => void }) {
  const live = useRoomLive(c, sid);
  if (!serves(c, "Highlights")) return null;
  const { open, unread } = highlightCounts(live.items);
  const label = open > 0 ? `Highlights, ${open} open${unread ? ", some unread" : ""}` : "Highlights";
  return (
    <Button variant="ghost-muted" size="sm" aria-label={label} title={label} className={cn("gap-1.5 px-2", open > 0 && "text-foreground")} onClick={onOpen}>
      <Flag className="size-4" />
      {/* Narrow composers keep the icon and the count. */}
      <span className="max-[720px]:hidden">Highlights</span>
      {open > 0 && <span className="tabular-nums">{open}</span>}
      {unread && <span aria-hidden className="size-1.5 rounded-full bg-info" />}
    </Button>
  );
}

// One line above the composer for the most urgent open item: a decision
// for the person, else a blocker (stripItem). It never stacks.
export function HighlightsStrip({ item, onOpen }: { item: HighlightItem; onOpen: () => void }) {
  return (
    <button
      type="button"
      className="highlights-strip flex h-8 w-full cursor-pointer items-center gap-2 px-3 text-left text-xs outline-none hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring"
      onClick={onOpen}
    >
      {item.kind === "needs_you" ? <CircleAlert className="size-4 shrink-0 text-warning-foreground" /> : <Flag className="size-4 shrink-0 text-destructive-foreground" />}
      <span className="shrink-0 text-muted-foreground">{item.kind === "needs_you" ? "Needs you" : "Blocker"}</span>
      <span className="min-w-0 flex-1 truncate">{item.title}</span>
      <span className="shrink-0 text-faint">{item.agent}</span>
    </button>
  );
}
