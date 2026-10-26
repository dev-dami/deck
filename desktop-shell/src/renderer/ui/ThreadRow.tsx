import type { Session, Theme } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { kindColour, kindMark } from "@web/theme";
import { relativeTime } from "@web/time";
import { Archive, ArchiveRestore, CircleCheck, FileDiff, Folder, GitCompareArrows, type IconType, ShieldQuestion, Trash2 } from "./icons";
import { memo, useState } from "react";
import type { ArchiveOffer, ThreadChoice } from "../../shared/bridge";
import type { RoomState, RoomStatus } from "../attention";
import { cn } from "../lib/cn";
import type { RowStyle } from "../prefs";
import { sameData } from "../select";

// Each state is a dot or a small glyph at the row's leading edge; the word
// stays for screen readers and the tooltip.
const STATUS: Record<Exclude<RoomState, "idle">, { label: string; icon: IconType | null; className: string }> = {
  working: { label: "Working", icon: null, className: "text-signal" },
  "needs-you": { label: "Needs you", icon: ShieldQuestion, className: "text-warning" },
  overlap: { label: "Overlap", icon: GitCompareArrows, className: "text-warning" },
  review: { label: "To review", icon: FileDiff, className: "text-info-foreground" },
  done: { label: "Done", icon: CircleCheck, className: "text-success" },
};

// The agents that wrote in the thread, each glyph in its hue, as T3 Code
// stacks provider icons. The room's short id stays in the room header.
function RowIndex({ kinds, theme }: { kinds: string[]; theme: Theme | null }) {
  if (kinds.length === 0) return null;
  return (
    // It gives way to the row's hover actions, which sit over its end.
    <span className="kind-stack inline-flex shrink-0 items-center font-mono text-2xs transition-opacity group-focus-within/sidebar-row:opacity-0 group-hover/sidebar-row:opacity-0">
      {kinds.slice(0, 3).map((k) => (
        <span key={k} className="grid size-3.5 place-items-center" style={{ color: kindColour(k) }} title={k}>
          {kindMark(theme, k) || k.slice(0, 1)}
        </span>
      ))}
    </span>
  );
}

// A row's action on hover: a round glyph button.
const rowAction =
  "grid size-6 cursor-pointer place-items-center rounded-full text-muted-foreground outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring";

// The row's leading mark: its state, or the accent's dot while it is
// unread. A row with neither keeps the space, so every title lines up.
function Mark({ state, unread, label }: { state: Exclude<RoomState, "idle"> | null; unread: boolean; label: string }) {
  const st = state === null ? null : STATUS[state];
  return (
    <span className="grid size-4 shrink-0 place-items-center">
      {st !== null ? (
        <span className={cn("grid place-items-center", st.className)} data-status={state} title={label || st.label}>
          {st.icon === null ? (
            <span aria-hidden className="size-2 animate-deck-pulse rounded-full bg-current shadow-[0_0_0_3px_color-mix(in_srgb,currentColor_22%,transparent)]" />
          ) : (
            <st.icon aria-hidden weight="fill" className="size-3.5" />
          )}
          <span role="status" className="sr-only">
            {st.label}
          </span>
        </span>
      ) : (
        unread && (
          <span className="size-1.5 rounded-full bg-primary" title="Unread">
            <span className="sr-only">Unread</span>
          </span>
        )
      )}
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
  const label = status?.label ?? "";
  const recede = !active && state === null && !unread;
  // The open thread is a capsule of the selection, lit along its top edge;
  // hover is a lighter wash. The radius nests in the sidebar's.
  const surface = cn(
    "session-main group/sidebar-row relative block w-full cursor-pointer overflow-hidden rounded-[10px] text-left outline-none transition-colors select-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
    active ? "bg-selected text-foreground inset-shadow-hl" : "hover:bg-hover",
  );
  const mark = <Mark state={state} unread={unread && !active} label={label} />;
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
        unread && !active ? "font-medium" : "font-normal",
        active || unread ? "text-foreground" : recede ? "text-foreground/75 group-hover/sidebar-row:text-foreground" : "text-foreground/90",
      )}
    >
      {sessionTitle(s)}
    </span>
  );
  // The row's end: its key while Ctrl/Cmd is held, how many asks wait, or
  // how long ago it moved. Its actions take the place on hover.
  const statusSlot = (
    <span className="relative flex h-5 min-w-6 shrink-0 items-center justify-end group-focus-within/sidebar-row:min-w-12 group-hover/sidebar-row:min-w-12">
      <span className="flex items-center text-2xs text-faint tabular-nums transition-opacity group-focus-within/sidebar-row:pointer-events-none group-focus-within/sidebar-row:opacity-0 group-hover/sidebar-row:pointer-events-none group-hover/sidebar-row:opacity-0">
        {digit !== null ? (
          <span className="row-digit grid h-4.5 min-w-4.5 place-items-center rounded-[5px] bg-selected px-1 font-medium text-2xs text-foreground">{digit}</span>
        ) : state === "needs-you" && s.pending > 1 ? (
          <span className="font-medium text-warning-foreground">{s.pending}</span>
        ) : (
          relativeTime(s.updated_at, now)
        )}
      </span>
      <span className="pointer-events-none absolute inset-y-0 right-0 flex items-center opacity-0 transition-opacity group-focus-within/sidebar-row:pointer-events-auto group-focus-within/sidebar-row:opacity-100 group-hover/sidebar-row:pointer-events-auto group-hover/sidebar-row:opacity-100">
        {confirm ? (
          <span className="flex items-center gap-0.5 text-xs">
            <button
              type="button"
              className="h-5 cursor-pointer rounded-full bg-destructive/12 px-2 font-medium text-destructive-foreground hover:bg-destructive/20"
              onClick={(e) => {
                e.stopPropagation();
                onDelete();
              }}
            >
              Delete
            </button>
            <button
              type="button"
              className="h-5 cursor-pointer rounded-full px-1.5 text-muted-foreground hover:bg-hover hover:text-foreground"
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
                className={rowAction}
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
                className={cn(rowAction, "aria-disabled:cursor-default aria-disabled:opacity-40")}
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
              className={cn(rowAction, "-mr-1")}
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
        <div {...common} className={cn(surface, "flex min-h-8 flex-col justify-center py-1 pr-2 pl-1.5")}>
          <div className="flex h-6 min-w-0 items-center gap-2">
            {mark}
            {title}
            {branchTag}
            <RowIndex kinds={kinds} theme={theme} />
            {statusSlot}
          </div>
          {label !== "" && (
            <span
              className={cn(
                "status-label -mt-0.5 truncate pb-0.5 pl-6 text-2xs text-faint",
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
        <div className={cn("relative flex gap-2 py-2 pr-2 pl-1.5", hideProject ? "h-[3.625rem]" : "h-[4.875rem]")}>
          <span className="flex h-5 shrink-0 items-center">{mark}</span>
          <div className="flex min-w-0 flex-1 flex-col">
            {!hideProject && (
              <div className="flex h-4 min-w-0 items-center gap-1 text-2xs text-faint">
                <Folder className="size-3 shrink-0" />
                <span className="min-w-0 truncate">{project}</span>
              </div>
            )}
            <div className={cn("flex h-5 min-w-0 items-center gap-1.5", !hideProject && "mt-0.5")}>
              {title}
              {statusSlot}
            </div>
            <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs">
              <span className={cn("min-w-0 flex-1 truncate text-faint", label !== "" && "status-label text-2xs")}>
                {label !== "" ? label : s.last_text !== undefined && s.last_text !== "" ? s.last_text : `${s.mode} mode`}
              </span>
              {branchTag}
              <RowIndex kinds={kinds} theme={theme} />
            </div>
          </div>
        </div>
      </div>
    </li>
  );
}, sameData);
