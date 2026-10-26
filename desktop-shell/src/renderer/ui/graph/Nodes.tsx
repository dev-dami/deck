import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type CSSProperties, memo, type ReactNode } from "react";
import { agentColour } from "../../agents";
import type { AgentGraphNode, FileGraphNode, FoldGraphNode, GraphNode, TaskGraphNode } from "../../graph";
import type { LaneStatus } from "../../lanes";
import { cn } from "../../lib/cn";
import { AgentRole } from "../AgentRole";
import { Check, Minus, X } from "../icons";

// The Work Graph's nodes: flat, precise cards over the edges, as a
// debugger's graph draws them. One plane each (the surface and a hairline),
// type for hierarchy, an agent as its glyph in its hue, and a small mark for
// its state. Each sits at its layout position through a transform.

// An agent's hue as a custom property, for its glyph and its edges.
export const hueOf = (name: string, kind: string): CSSProperties => ({ "--hue": agentColour(name, kind) }) as CSSProperties;

export const STATUS_LABEL: Record<LaneStatus, string> = {
  working: "Working",
  waiting: "Needs you",
  error: "Failed",
  idle: "Idle",
  exited: "Done",
};

const STATUS_DOT: Record<LaneStatus, string> = {
  working: "bg-signal",
  waiting: "bg-warning",
  error: "bg-destructive",
  idle: "bg-muted-foreground/40",
  exited: "border border-muted-foreground/50",
};

// A 6px status dot; a working one pulses, stepped, while it is in sight.
export function StatusDot({ status, live = true, className }: { status: LaneStatus; live?: boolean; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn("size-1.5 shrink-0 rounded-full", STATUS_DOT[status], status === "working" && live && "animate-deck-pulse", className)}
    />
  );
}

// The agent's glyph in its hue.
export function Glyph({ kind, theme, className }: { kind: string; theme: Theme | null; className?: string }) {
  return (
    <span aria-hidden className={cn("w-3.5 shrink-0 text-center font-mono text-xs text-(--hue)", className)}>
      {kindMark(theme, kind) || "●"}
    </span>
  );
}

// How a node shows: picked (the accent outline), dimmed by a hover
// elsewhere, and whether its live motion may run (it is in sight). Plain
// values, so a hover renders only the nodes whose look it changes.
interface NodeProps<N extends GraphNode> {
  n: N;
  picked: boolean;
  dim: boolean;
  live: boolean;
  theme: Theme | null;
  onPick: (id: string) => void;
  onHover: (id: string | null) => void;
}

function Shell({
  n,
  picked,
  dim,
  label,
  className,
  style,
  children,
  onPick,
  onHover,
}: {
  n: GraphNode;
  picked: boolean;
  dim: boolean;
  label: string;
  className: string;
  style?: CSSProperties;
  children: ReactNode;
  onPick: (id: string) => void;
  onHover: (id: string | null) => void;
}) {
  return (
    <div
      className={cn("graph-node absolute top-0 left-0 transition-opacity duration-200", dim && "opacity-35")}
      style={{ transform: `translate(${n.x}px, ${n.y}px)`, width: n.w, height: n.h, ...style }}
    >
      <button
        type="button"
        data-node={n.id}
        data-kind={n.kind}
        aria-label={label}
        aria-pressed={n.kind === "agent" ? picked : undefined}
        className={cn(
          "size-full animate-enter cursor-pointer rounded-md border bg-surface-1 text-left outline-none transition-colors",
          picked ? "border-primary ring-1 ring-primary" : "border-(--graph-rim) hover:border-muted-foreground/50",
          "focus-visible:border-primary focus-visible:ring-1 focus-visible:ring-primary",
          className,
        )}
        onClick={() => onPick(n.id)}
        onPointerEnter={() => onHover(n.id)}
        onPointerLeave={() => onHover(null)}
        onFocus={() => onHover(n.id)}
        onBlur={() => onHover(null)}
      >
        {children}
      </button>
    </div>
  );
}

// The person: where the work starts.
export const YouCard = memo(function YouCard({ n, picked, dim, onPick, onHover }: NodeProps<GraphNode>) {
  return (
    <Shell n={n} picked={picked} dim={dim} label="You" className="grid place-items-center font-medium text-xs" onPick={onPick} onHover={onHover}>
      You
    </Shell>
  );
});

// An agent: its glyph and name, a status dot, its model and context, and
// under a hairline the tool it runs now.
export const AgentCard = memo(function AgentCard({ n, picked, dim, live, theme, onPick, onHover }: NodeProps<AgentGraphNode>) {
  const tool = n.tools[0];
  const meta = [n.model, n.effort].filter((x) => x !== "").join(" · ") || n.agentKind;
  return (
    <Shell
      n={n}
      picked={picked}
      dim={dim}
      label={`${n.name}, ${STATUS_LABEL[n.status].toLowerCase()}`}
      className="flex flex-col"
      style={hueOf(n.name, n.agentKind)}
      onPick={onPick}
      onHover={onHover}
    >
      <span className="flex min-h-0 flex-1 flex-col justify-center gap-0.5 px-2.5">
        <span className="flex min-w-0 items-center gap-1.5">
          <Glyph kind={n.agentKind} theme={theme} />
          <span className="flex min-w-0 flex-1 items-baseline gap-1.5">
            <span className={cn("min-w-0 truncate font-medium text-sm leading-tight", n.status === "exited" && "text-muted-foreground")}>
              {n.name}
            </span>
            <AgentRole role={n.role} className="text-2xs" />
          </span>
          {n.status === "waiting" && <span className="shrink-0 font-medium text-3xs text-warning-foreground">needs you</span>}
          <StatusDot status={n.status} live={live} />
        </span>
        <span className="flex min-w-0 items-center gap-1.5 pl-5 text-2xs text-muted-foreground">
          <span className="min-w-0 flex-1 truncate">{meta}</span>
          {n.contextPct !== null && (
            <span className={cn("shrink-0 tabular-nums", n.contextPct >= 85 ? "text-warning-foreground" : "text-faint")}>{n.contextPct}%</span>
          )}
        </span>
      </span>
      {tool !== undefined && (
        <span className="flex h-6 min-w-0 shrink-0 items-center gap-1.5 border-hairline border-t px-2.5 font-mono text-3xs">
          <span className="shrink-0 text-foreground">{tool.subagent ? "subagent" : tool.name}</span>
          <span className="min-w-0 truncate text-muted-foreground">{tool.summary}</span>
        </span>
      )}
    </Shell>
  );
});

// A task's state as a small mark: a check, a cross, a dot.
export function TaskMark({ status, live = true }: { status: string; live?: boolean }) {
  if (status === "done") return <Check aria-hidden className="size-3 shrink-0 text-success" weight="bold" />;
  if (status === "failed") return <X aria-hidden className="size-3 shrink-0 text-destructive" weight="bold" />;
  if (status === "cancelled") return <Minus aria-hidden className="size-3 shrink-0 text-faint" weight="bold" />;
  const dot: Record<string, string> = {
    running: cn("bg-signal", live && "animate-deck-pulse"),
    review: "bg-signal",
    blocked: "bg-warning",
    ready: "border border-muted-foreground",
  };
  return (
    <span aria-hidden className="grid size-3 shrink-0 place-items-center">
      <span className={cn("size-1.5 rounded-full", dot[status] ?? "border border-faint")} />
    </span>
  );
}

export const taskTitle = (t: TaskGraphNode["task"]): string => (t.entry ? `${t.owner || t.id}'s entry` : t.title || t.id);

// A task of the board: its mark and title, then its id, state and owner.
export const TaskCard = memo(function TaskCard({ n, picked, dim, live, theme, onPick, onHover }: NodeProps<TaskGraphNode>) {
  const t = n.task;
  return (
    <Shell
      n={n}
      picked={picked}
      dim={dim}
      label={`Task ${t.id}: ${taskTitle(t)}, ${t.status}`}
      className="flex flex-col justify-center gap-0.5 px-2.5"
      onPick={onPick}
      onHover={onHover}
    >
      <span className="flex min-w-0 items-center gap-1.5" title={t.status}>
        <TaskMark status={t.status} live={live} />
        <span className="min-w-0 flex-1 truncate font-medium text-sm leading-tight">{taskTitle(t)}</span>
        {t.race && <span className="shrink-0 text-3xs text-faint">race</span>}
      </span>
      <span className="flex min-w-0 items-center gap-1.5 pl-[18px] text-2xs text-muted-foreground">
        <span className="shrink-0 font-mono text-faint">{t.id}</span>
        {t.owner !== "" && (
          <span className="flex min-w-0 items-center gap-1" style={hueOf(t.owner, n.ownerKind)}>
            <Glyph kind={n.ownerKind} theme={theme} className="w-auto text-3xs" />
            <span className="truncate">{t.owner}</span>
          </span>
        )}
      </span>
    </Shell>
  );
});

// A file's +/−, in the diff's colours.
function DiffStat({ added, removed }: { added: number; removed: number }) {
  return (
    <span className="shrink-0 font-mono text-3xs tabular-nums">
      <span className="text-diff-addition-foreground">+{added}</span> <span className="text-diff-deletion-foreground">−{removed}</span>
    </span>
  );
}

// A file and the fold lie on one line, or stack in a narrow card (a graph
// laid out down: graph.ts, SIZE_DOWN).
const stacked = (n: GraphNode) => n.h >= 40;

// A file: its name, its folder in faint, and its +/− when it changed. A
// lease's pattern is drawn dashed.
export const FileCard = memo(function FileCard({ n, picked, dim, onPick, onHover }: NodeProps<FileGraphNode>) {
  const slash = n.path.lastIndexOf("/");
  const name = n.glob ? n.path : n.path.slice(slash + 1);
  const dir = n.glob || slash < 0 ? "" : n.path.slice(0, slash);
  const stack = stacked(n);
  return (
    <Shell
      n={n}
      picked={picked}
      dim={dim}
      label={n.glob ? `Files under ${n.path}` : `${n.path}${n.changed ? `, +${n.added} −${n.removed}` : ""}`}
      className={cn(
        "@container flex px-2.5",
        stack ? "flex-col justify-center gap-0.5" : "items-center gap-2",
        n.glob && !picked && "border-dashed",
        !n.changed && !n.glob && "bg-transparent",
      )}
      onPick={onPick}
      onHover={onHover}
    >
      <span className={cn("flex min-w-0 items-baseline gap-1.5 font-mono", !stack && "flex-1")}>
        <span className={cn("min-w-0 truncate text-xs leading-tight", !n.changed && "text-muted-foreground")} title={n.path}>
          {name}
        </span>
        {/* The folder, when the card is wide enough to say it whole-ish. */}
        {dir !== "" && <span className="hidden min-w-0 truncate text-3xs text-faint @[11rem]:inline">{dir}</span>}
      </span>
      {n.changed ? (
        <DiffStat added={n.added} removed={n.removed} />
      ) : (
        stack && <span className="truncate text-3xs text-faint">{n.glob ? "leased" : "unchanged"}</span>
      )}
    </Shell>
  );
});

// The changed files no one is linked to, as one node.
export const FoldCard = memo(function FoldCard({ n, picked, dim, onPick, onHover }: NodeProps<FoldGraphNode>) {
  const stack = stacked(n);
  return (
    <Shell
      n={n}
      picked={picked}
      dim={dim}
      label={`${n.paths.length} more changed files`}
      className={cn("flex px-2.5", stack ? "flex-col justify-center gap-0.5" : "items-center gap-2")}
      onPick={onPick}
      onHover={onHover}
    >
      <span className={cn("min-w-0 truncate text-xs leading-tight", !stack && "flex-1")}>{n.paths.length} more files</span>
      <DiffStat added={n.added} removed={n.removed} />
    </Shell>
  );
});

export function NodeCard(props: NodeProps<GraphNode>) {
  const { n } = props;
  switch (n.kind) {
    case "you":
      return <YouCard {...props} n={n} />;
    case "agent":
      return <AgentCard {...props} n={n} />;
    case "task":
      return <TaskCard {...props} n={n} />;
    case "file":
      return <FileCard {...props} n={n} />;
    case "fold":
      return <FoldCard {...props} n={n} />;
  }
}
