import type { Session, Theme } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { kindColour, kindMark } from "@web/theme";
import { relativeTime } from "@web/time";
import { Archive, ArchiveRestore, CircleCheck, FileDiff, Folder, GitCompareArrows, type LucideIcon, ShieldQuestion, Trash2 } from "lucide-react";
import { memo, useState } from "react";
import type { ArchiveOffer, ThreadChoice } from "../../shared/bridge";
import type { RoomState, RoomStatus } from "../attention";
import { cn } from "../lib/cn";
import type { RowStyle } from "../prefs";
import { sameData } from "../select";

// Each state is a glyph; the word stays for screen readers and the tooltip.
const STATUS: Record<Exclude<RoomState, "idle">, { label: string; icon: LucideIcon | null; className: string }> = {
  working: { label: "Working", icon: null, className: "text-signal" },
  "needs-you": { label: "Needs you", icon: ShieldQuestion, className: "text-warning-foreground" },
  overlap: { label: "Overlap", icon: GitCompareArrows, className: "text-warning-foreground" },
  review: { label: "To review", icon: FileDiff, className: "text-info-foreground" },
  done: { label: "Done", icon: CircleCheck, className: "text-success-foreground" },
};

// The agents that wrote in the thread, as T3 Code stacks provider icons.
// The room's short id stays in the room header.
function RowIndex({ kinds, theme }: { kinds: string[]; theme: Theme | null }) {
  return (
    // It gives way to the row's hover actions, which sit over its end.
    <span className="kind-stack ml-auto inline-flex shrink-0 items-center gap-0.5 font-mono text-3xs transition-opacity group-focus-within/sidebar-row:opacity-0 group-hover/sidebar-row:opacity-0">
      {kinds.slice(0, 3).map((k) => (
        <span key={k} className="grid size-3.5 place-items-center" style={{ color: kindColour(k) }} title={k}>
          {kindMark(theme, k) || k.slice(0, 1)}
        </span>
      ))}
    </span>
  );
}

// A row renders again only when its data changes: its handlers call
// through the Sidebar's latest props.
export const ThreadRow = memo(function ThreadRow({
  s,
  project,
  active,
  unread,
  status,
  showLabel,
  digit,
  now,
  variant,
  kinds,
  theme,
  hideProject = false,
  branch,
  archive,
  onUnarchive,
  onOpen,
  onRename,
  onDelete,
  onArchive,
  onMenu,
}: {
  s: Session;
  project: string;
  // Under its project's heading the card leaves the project out.
  hideProject?: boolean;
  active: boolean;
  unread: boolean;
  status: RoomStatus | undefined;
  // A compact row shows its status label always (the current project's
  // rows) or on hover.
  showLabel: boolean;
  // Shown while Ctrl/Cmd is held: the row's key, 1 to 9.
  digit: number | null;
  now: number;
  variant: RowStyle;
  kinds: string[];
  theme: Theme | null;
  // The room's own branch when it is isolated, else "".
  branch: string;
  archive: ArchiveOffer;
  // Set for an archived row whose Deck can bring it back.
  onUnarchive: (() => void) | undefined;
  onOpen: () => void;
  onRename: (title: string) => void;
  onDelete: () => void;
  onArchive: () => void;
  // The native menu of the row: Rename, Archive, or Delete (main asks first).
  onMenu: () => Promise<ThreadChoice>;
}) {
  const [renaming, setRenaming] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const state = status === undefined || status.state === "idle" ? null : status.state;
  const st = state === null ? null : STATUS[state];
  const label = status?.label ?? "";
  const recede = !active && state === null;
  // The open thread is lifted one surface; hover is a white alpha.
  const surface = cn(
    "session-main group/sidebar-row relative block w-full cursor-pointer overflow-hidden rounded-md text-left outline-none transition-colors select-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
    active
      ? "bg-sidebar-row-active text-foreground inset-shadow-hl"
      : recede
        ? "text-muted-foreground hover:bg-sidebar-row-hover hover:text-foreground"
        : "text-foreground hover:bg-sidebar-row-hover",
  );
  // A working thread carries a 2px rail in the hue of an agent in it.
  const rail = state === "working" && (
    <span
      aria-hidden
      className="absolute inset-y-2 left-0 w-0.5 rounded-full bg-current"
      style={{ color: kinds[0] === undefined ? "var(--signal)" : kindColour(kinds[0]) }}
    />
  );
  const title = renaming ? (
    <input
      autoFocus
      defaultValue={s.title}
      aria-label="Thread title"
      onClick={(e) => e.stopPropagation()}
      onFocus={(e) => e.currentTarget.select()}
      onKeyDown={(e) => {
        e.stopPropagation();
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
    <span
      className={cn(
        "session-title min-w-0 flex-1 truncate text-sm",
        "font-normal",
        active || unread ? "text-foreground" : recede ? "text-muted-foreground" : "text-foreground/90",
      )}
    >
      {sessionTitle(s)}
    </span>
  );
  const statusSlot = (
    <span className="relative ml-auto flex h-5 min-w-8 shrink-0 items-center justify-end text-xs">
      <span
        className={cn(
          "flex items-center font-mono text-2xs text-faint transition-opacity group-focus-within/sidebar-row:pointer-events-none group-focus-within/sidebar-row:opacity-0 group-hover/sidebar-row:pointer-events-none group-hover/sidebar-row:opacity-0",
        )}
      >
        {digit !== null ? (
          <span className="row-digit font-mono text-2xs text-faint">{digit}</span>
        ) : st !== null ? (
          <span className={cn("inline-flex items-center gap-1", st.className)} data-status={state} title={label || st.label}>
            {st.icon === null ? (
              <span aria-hidden className="size-1.5 shrink-0 animate-deck-pulse rounded-full bg-current" />
            ) : (
              <st.icon aria-hidden className="size-3.5 shrink-0" />
            )}
            <span role="status" className="sr-only">
              {st.label}
            </span>
            {state === "needs-you" && s.pending > 1 && <span>{s.pending}</span>}
          </span>
        ) : (
          relativeTime(s.updated_at, now)
        )}
      </span>
      <span className="pointer-events-none absolute inset-y-0 right-0 flex items-center opacity-0 transition-opacity group-focus-within/sidebar-row:pointer-events-auto group-focus-within/sidebar-row:opacity-100 group-hover/sidebar-row:pointer-events-auto group-hover/sidebar-row:opacity-100">
        {confirm ? (
          <span className="flex items-center gap-1 text-xs">
            <button
              type="button"
              className="rounded px-1 text-destructive-foreground hover:underline"
              onClick={(e) => {
                e.stopPropagation();
                onDelete();
              }}
            >
              Delete
            </button>
            <button
              type="button"
              className="rounded px-1 text-muted-foreground hover:text-foreground"
              onClick={(e) => {
                e.stopPropagation();
                setConfirm(false);
              }}
            >
              Keep
            </button>
          </span>
        ) : (
          <>
            {onUnarchive !== undefined && (
              <button
                type="button"
                aria-label={`Unarchive ${sessionTitle(s)}`}
                title="Unarchive thread"
                className="inline-flex cursor-pointer items-center rounded-md px-1 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                onClick={(e) => {
                  e.stopPropagation();
                  onUnarchive();
                }}
              >
                <ArchiveRestore className="size-3.5" />
              </button>
            )}
            {(archive === "on" || archive === "busy") && (
              <button
                type="button"
                aria-label={`Archive ${sessionTitle(s)}`}
                title={archive === "on" ? "Archive thread" : "Stop its agents to archive it"}
                aria-disabled={archive !== "on"}
                className="inline-flex cursor-pointer items-center rounded-md px-1 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-disabled:cursor-default aria-disabled:opacity-40"
                onClick={(e) => {
                  e.stopPropagation();
                  if (archive === "on") onArchive();
                }}
              >
                <Archive className="size-3.5" />
              </button>
            )}
            <button
              type="button"
              aria-label={`Delete ${sessionTitle(s)}`}
              title="Delete thread"
              className="-mr-1 inline-flex cursor-pointer items-center gap-1 rounded-md px-1.5 text-muted-foreground text-xs outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              onClick={(e) => {
                e.stopPropagation();
                setConfirm(true);
              }}
            >
              <Trash2 className="size-3.5" />
            </button>
          </>
        )}
      </span>
    </span>
  );
  const branchTag = branch !== "" && (
    <span className="row-branch min-w-0 max-w-24 shrink truncate font-mono text-3xs text-faint" title={branch}>
      {branch}
    </span>
  );
  const common = {
    role: "button" as const,
    tabIndex: 0,
    "aria-label": sessionTitle(s),
    "aria-current": active ? ("page" as const) : undefined,
    onClick: onOpen,
    onDoubleClick: (e: React.MouseEvent) => {
      if ((e.target as HTMLElement).closest("button, input")) return;
      setRenaming(true);
    },
    onContextMenu: (e: React.MouseEvent) => {
      if ((e.target as HTMLElement).closest("input")) return;
      e.preventDefault();
      void onMenu().then((choice) => {
        if (choice === "rename") setRenaming(true);
        else if (choice === "archive") onArchive();
        else if (choice === "unarchive") onUnarchive?.();
        else if (choice === "delete") onDelete();
      });
    },
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
      e.preventDefault();
      onOpen();
    },
  };
  if (variant === "compact") {
    return (
      <li className={cn("session list-none animate-enter", active && "open")} data-session={s.id}>
        <div {...common} className={cn(surface, "flex min-h-9 flex-col justify-center px-2.5 py-1")}>
          {rail}
          <div className="flex h-7 min-w-0 items-center gap-2">
            {title}
            {branchTag}
            <RowIndex kinds={kinds} theme={theme} />
            {statusSlot}
          </div>
          {label !== "" && (
            <span
              className={cn(
                "status-label -mt-1 truncate pb-0.5 text-2xs text-faint",
                showLabel ? "block" : "hidden group-focus-within/sidebar-row:block group-hover/sidebar-row:block",
              )}
            >
              {label}
            </span>
          )}
        </div>
      </li>
    );
  }
  return (
    <li className={cn("session list-none animate-enter", active && "open")} data-session={s.id}>
      <div {...common} className={surface}>
        {rail}
        <div className={cn("relative px-2.5 py-2", hideProject ? "h-[3.625rem]" : "h-[4.875rem]")}>
          {hideProject ? (
            <div className="flex h-5 min-w-0 items-center gap-1.5">
              {title}
              {statusSlot}
            </div>
          ) : (
            <div className="flex h-5 min-w-0 items-center gap-1.5">
              <Folder className="size-3.5 shrink-0 text-muted-foreground/70" />
              <span className="min-w-0 flex-1 truncate text-2xs text-faint">
                {project}
              </span>
              {statusSlot}
            </div>
          )}
          {!hideProject && <div className="mt-1 flex min-w-0">{title}</div>}
          <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-secondary-label text-xs">
            <span className={cn("min-w-0 flex-1 truncate text-faint", label !== "" && "status-label text-2xs")}>
              {label !== "" ? label : s.last_text !== undefined && s.last_text !== "" ? s.last_text : `${s.mode} mode`}
            </span>
            {branchTag}
            <RowIndex kinds={kinds} theme={theme} />
          </div>
        </div>
      </div>
    </li>
  );
}, sameData);
