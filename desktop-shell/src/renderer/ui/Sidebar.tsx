import type { Session } from "@web/protocol";
import type { Link } from "@web/state";
import { sessionTitle } from "@web/state";
import { relativeTime } from "@web/time";
import { FolderOpen, Loader2, PencilLine, Search, SquarePen, Trash2 } from "lucide-react";
import { useState } from "react";
import { cn } from "../lib/cn";
import { Badge, Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { modKey } from "./keys";

function Row({
  s,
  open,
  unread,
  now,
  onOpen,
  onRename,
  onDelete,
}: {
  s: Session;
  open: boolean;
  unread: boolean;
  now: number;
  onOpen: () => void;
  onRename: (title: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  if (editing) {
    return (
      <li className="session editing px-1">
        <input
          aria-label="Session title"
          defaultValue={s.title}
          autoFocus
          className="h-8 w-full rounded-md border border-input bg-background px-2 text-[13px] outline-none focus:border-primary"
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              const t = e.currentTarget.value.trim();
              if (t !== "") onRename(t);
              setEditing(false);
            } else if (e.key === "Escape") setEditing(false);
          }}
          onBlur={() => setEditing(false)}
        />
      </li>
    );
  }
  return (
    <li className={cn("session group/row relative", open && "open")} data-session={s.id}>
      <button
        type="button"
        className={cn(
          "session-main flex h-8 w-full cursor-pointer items-center gap-2 rounded-md px-2 text-left text-[13px] outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
          open ? "bg-sidebar-row-active text-foreground" : "text-foreground/80 hover:bg-sidebar-row-hover",
        )}
        onClick={onOpen}
        aria-current={open ? "page" : undefined}
      >
        <span className="flex w-3 shrink-0 justify-center">
          {s.busy ? (
            <Loader2 className="dot busy size-3 animate-spin text-muted-foreground" aria-label="Agents are working" />
          ) : unread ? (
            <span className="dot unread size-1.5 rounded-full bg-primary" title="New messages" />
          ) : null}
        </span>
        <span className={cn("session-title min-w-0 flex-1 truncate", unread && "font-medium text-foreground")}>
          {sessionTitle(s)}
        </span>
        {s.busy && unread && <span className="dot unread size-1.5 shrink-0 rounded-full bg-primary" title="New messages" />}
        <span className="session-meta flex shrink-0 items-center gap-1.5 group-hover/row:invisible">
          {s.pending > 0 && (
            <Badge variant="warning" title="Approvals waiting">
              {s.pending}
            </Badge>
          )}
          <span className="text-2xs text-muted-foreground tabular-nums">{relativeTime(s.updated_at, now)}</span>
        </span>
      </button>
      {confirm ? (
        <span className="absolute inset-y-0 right-1 flex items-center gap-1 rounded-md bg-sidebar pl-2 text-2xs">
          <span className="text-muted-foreground">Delete?</span>
          <Button size="xs" variant="destructive" onClick={onDelete}>
            Yes
          </Button>
          <Button size="xs" variant="ghost" onClick={() => setConfirm(false)}>
            No
          </Button>
        </span>
      ) : (
        <span className="session-tools absolute inset-y-0 right-1 hidden items-center gap-0.5 group-hover/row:flex">
          <Button
            size="icon-xs"
            variant="ghost-muted"
            aria-label={`Rename ${sessionTitle(s)}`}
            title="Rename"
            onClick={() => setEditing(true)}
          >
            <PencilLine />
          </Button>
          <Button
            size="icon-xs"
            variant="ghost-muted"
            className="hover:text-destructive-foreground"
            aria-label={`Delete ${sessionTitle(s)}`}
            title="Delete"
            onClick={() => setConfirm(true)}
          >
            <Trash2 />
          </Button>
        </span>
      )}
    </li>
  );
}

const linkDot: Record<Link, string> = {
  online: "bg-success",
  connecting: "bg-warning animate-deck-pulse",
  offline: "bg-destructive",
};

export function Sidebar({
  project,
  where,
  link,
  sessions,
  openId,
  unread,
  now,
  mac,
  onOpen,
  onNew,
  onRename,
  onDelete,
  onDisconnect,
  onPalette,
}: {
  project: string;
  where: string;
  link: Link;
  sessions: Session[];
  openId: string | null;
  unread: Record<string, true>;
  now: number;
  mac: boolean;
  onOpen: (id: string) => void;
  onNew: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  onDisconnect: () => void;
  onPalette: () => void;
}) {
  return (
    <nav className="sidebar flex w-64 shrink-0 flex-col border-r bg-sidebar" aria-label="Sessions">
      <header className={cn("drag flex flex-col gap-0.5 px-4 pb-3", mac ? "pt-10" : "pt-4")}>
        <div className="flex items-center gap-2">
          <span className="grid size-5 place-items-center rounded-md bg-foreground font-bold text-[11px] text-background">
            d
          </span>
          <div className="project min-w-0 truncate font-semibold text-[15px] tracking-tight">{project || "Deck"}</div>
        </div>
        <div className="where flex min-w-0 items-center gap-1.5 text-2xs text-muted-foreground" title={where}>
          <span className={cn("dot size-1.5 shrink-0 rounded-full", linkDot[link])} />
          <span className="truncate">{link === "online" ? where : link}</span>
        </div>
      </header>
      <div className="px-2 pb-2">
        <Button
          variant="ghost"
          className="new-session h-8 w-full justify-start gap-2 px-2 font-normal text-[13px]"
          onClick={onNew}
        >
          <SquarePen className="text-muted-foreground" />
          New session
          <Kbd className="ml-auto">{modKey(mac)}N</Kbd>
        </Button>
      </div>
      <div className="px-4 pt-2 pb-1 font-medium text-2xs text-muted-foreground">Sessions</div>
      <ul className="sessions flex min-h-0 flex-1 flex-col gap-px overflow-y-auto px-2 pb-2">
        {sessions.map((s) => (
          <Row
            key={s.id}
            s={s}
            open={s.id === openId}
            unread={unread[s.id] === true}
            now={now}
            onOpen={() => onOpen(s.id)}
            onRename={(t) => onRename(s.id, t)}
            onDelete={() => onDelete(s.id)}
          />
        ))}
        {sessions.length === 0 && <li className="px-2 py-1.5 text-muted-foreground text-xs">No sessions yet.</li>}
      </ul>
      <footer className="side-foot flex items-center gap-1 border-t px-2 py-2">
        <Button variant="ghost-muted" size="sm" className="font-normal" onClick={onDisconnect}>
          <FolderOpen />
          Switch Deck
        </Button>
        <Button
          variant="ghost-muted"
          size="sm"
          className="ml-auto font-normal"
          onClick={onPalette}
          aria-label="Commands"
          title="Commands"
        >
          <Search />
          <Kbd>{modKey(mac)}K</Kbd>
        </Button>
      </footer>
    </nav>
  );
}
