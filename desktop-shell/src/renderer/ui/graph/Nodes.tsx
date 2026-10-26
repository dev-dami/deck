import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type CSSProperties, memo, type ReactNode } from "react";
import { agentColour } from "../../agents";
import type { AgentGraphNode, FileGraphNode, FoldGraphNode, GraphNode, TaskGraphNode } from "../../graph";
import type { LaneStatus } from "../../lanes";
import { cn } from "../../lib/cn";
import {
  Check,
  Circle,
  CircleDashed,
  Eye,
  Files,
  FileText,
  Flag,
  Loader2,
  Lock,
  Minus,
  ShieldQuestion,
  Trophy,
  User,
  Wrench,
  XCircle,
  type IconType,
  CircleAlert,
} from "../icons";
import { Ring } from "../kit/ring";

// The Work Graph's nodes: HTML cards of clear glass placed over the edges,
// one per kind. Each sits at its layout position through a transform; the
// card inside it lifts when it is picked, so the two never fight.


// An agent's hue as a custom property, for its glyph, ring and edges.
export const hueOf = (name: string, kind: string): CSSProperties => ({ "--hue": agentColour(name, kind) }) as CSSProperties;

export const STATUS_LABEL: Record<LaneStatus, string> = {
  working: "Working",
  waiting: "Needs you",
  error: "Failed",
  idle: "Idle",
  exited: "Done",
};

const STATUS_RING: Record<LaneStatus, string> = {
  working: "ring-signal",
  waiting: "ring-warning",
  error: "ring-destructive",
  idle: "ring-transparent",
  exited: "ring-transparent",
};

// The agent's glyph on a disc of its hue, inside its status ring.
export function AgentMark({
  kind,
  theme,
  status,
  live,
  className,
}: {
  kind: string;
  theme: Theme | null;
  status: LaneStatus;
  live: boolean;
  className?: string;
}) {
  return (
    <span
      aria-hidden
      className={cn(
        "relative grid size-8 shrink-0 place-items-center rounded-full font-mono text-[13px] text-(--hue)",
        "bg-[color-mix(in_srgb,var(--hue)_15%,transparent)] ring-1 ring-[color-mix(in_srgb,var(--hue)_30%,transparent)] ring-inset",
        className,
      )}
    >
      {kindMark(theme, kind) || "●"}
      <span
        className={cn(
          "pointer-events-none absolute -inset-[3px] rounded-full ring-[1.5px]",
          STATUS_RING[status],
          status === "working" && live && "animate-deck-pulse",
        )}
      />
    </span>
  );
}

// How a node shows: picked (the accent ring), dimmed by a hover elsewhere,
// and whether its live motion may run (it is in sight). Plain values, so a
// hover renders only the nodes whose look it changes.
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
          "liquid-clear size-full animate-enter cursor-pointer text-left outline-none transition-[translate,box-shadow,background-color] duration-200",
          "bg-[color-mix(in_srgb,var(--canvas)_72%,transparent)] hover:bg-[color-mix(in_srgb,var(--surface-1)_90%,transparent)]",
          "focus-visible:ring-2 focus-visible:ring-primary",
          picked && "-translate-y-0.5 shadow-popover ring-2 ring-primary",
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
    <Shell n={n} picked={picked} dim={dim} label="You" className="flex items-center justify-center gap-1.5 rounded-full px-3" onPick={onPick} onHover={onHover}>
      <User className="size-3.5 text-primary" weight="bold" />
      <span className="font-medium text-sm">You</span>
    </Shell>
  );
});


const toolText = (n: AgentGraphNode) => {
  const t = n.tools[0];
  if (t === undefined) return null;
  return { name: t.subagent ? "subagent" : t.name, summary: t.summary };
};

// An agent: its glyph in its hue inside the status ring, its name and
// model, its context, and the tool it runs now.
export const AgentCard = memo(function AgentCard({ n, picked, dim, live, theme, onPick, onHover }: NodeProps<AgentGraphNode>) {
  const tool = toolText(n);
  const meta = [n.model, n.effort].filter((x) => x !== "").join(" · ") || n.agentKind;
  return (
    <Shell
      n={n}
      picked={picked}
      dim={dim}
      label={`${n.name}, ${STATUS_LABEL[n.status].toLowerCase()}`}
      className={cn("flex flex-col justify-center gap-2 rounded-xl px-2.5", n.status === "exited" && "opacity-70")}
      style={hueOf(n.name, n.agentKind)}
      onPick={onPick}
      onHover={onHover}
    >
      <span className="flex min-w-0 items-center gap-2.5">
        <AgentMark kind={n.agentKind} theme={theme} status={n.status} live={live} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-medium text-sm leading-tight">{n.name}</span>
          <span className="truncate text-2xs text-muted-foreground">{meta}</span>
        </span>
        {n.status === "waiting" ? (
          <ShieldQuestion className="size-4 shrink-0 text-warning" weight="fill" />
        ) : n.status === "error" ? (
          <CircleAlert className="size-4 shrink-0 text-destructive" weight="fill" />
        ) : (
          n.contextPct !== null && <Ring pct={n.contextPct} className="size-3.5" />
        )}
      </span>
      {tool !== null && (
        <span className="flex h-5 min-w-0 items-center gap-1.5 rounded-md bg-hover px-1.5 text-2xs">
          <Wrench className="size-3 shrink-0 text-(--hue)" />
          <span className="shrink-0 font-medium">{tool.name}</span>
          <span className="min-w-0 truncate font-mono text-muted-foreground">{tool.summary}</span>
        </span>
      )}
    </Shell>
  );
});

const TASK_ICON: Record<string, { icon: IconType; className: string; weight?: "fill" | "bold" }> = {
  done: { icon: Check, className: "text-success", weight: "bold" },
  running: { icon: Loader2, className: "text-signal animate-spin" },
  review: { icon: Eye, className: "text-signal" },
  blocked: { icon: CircleAlert, className: "text-warning", weight: "fill" },
  failed: { icon: XCircle, className: "text-destructive", weight: "fill" },
  cancelled: { icon: Minus, className: "text-faint" },
  ready: { icon: Circle, className: "text-muted-foreground" },
  pending: { icon: CircleDashed, className: "text-faint" },
};

export function TaskIcon({ status, live = true, className }: { status: string; live?: boolean; className?: string }) {
  const s = TASK_ICON[status] ?? TASK_ICON["pending"]!;
  const Icon = s.icon;
  return <Icon aria-hidden className={cn("size-3.5 shrink-0", s.className, !live && "animate-none", className)} weight={s.weight ?? "regular"} />;
}

export const taskTitle = (t: TaskGraphNode["task"]): string => (t.entry ? `${t.owner || t.id}'s entry` : t.title || t.id);

// A task of the board: its status, its title, its id and owner.
export const TaskCard = memo(function TaskCard({ n, picked, dim, live, theme, onPick, onHover }: NodeProps<TaskGraphNode>) {
  const t = n.task;
  const KindIcon = t.race ? Flag : t.entry ? Trophy : null;
  return (
    <Shell n={n} picked={picked} dim={dim} label={`Task ${t.id}: ${taskTitle(t)}, ${t.status}`} className="flex items-center gap-2.5 rounded-xl px-3" onPick={onPick} onHover={onHover}>
      <TaskIcon status={t.status} live={live} />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="flex min-w-0 items-center gap-1.5">
          {KindIcon !== null && <KindIcon className="size-3 shrink-0 text-muted-foreground" />}
          <span className="truncate font-medium text-sm leading-tight">{taskTitle(t)}</span>
        </span>
        <span className="flex min-w-0 items-center gap-1 text-2xs text-muted-foreground">
          <span className="font-mono">{t.id}</span>
          <span aria-hidden>·</span>
          <span className="truncate">{t.status}</span>
          {t.owner !== "" && !t.entry && (
            <>
              <span aria-hidden>·</span>
              <span className="truncate" style={hueOf(t.owner, n.ownerKind)}>
                <span className="font-mono text-(--hue)">{kindMark(theme, n.ownerKind) || "●"}</span> {t.owner}
              </span>
            </>
          )}
        </span>
      </span>
    </Shell>
  );
});

// A file: its name, then its folder in faint, and its +/− when it changed.
export const FileCard = memo(function FileCard({ n, picked, dim, onPick, onHover }: NodeProps<FileGraphNode>) {
  const slash = n.path.replace(/\/\*\*$/, "").lastIndexOf("/");
  const name = n.glob ? n.path : n.path.slice(slash + 1);
  const dir = n.glob || slash < 0 ? "" : n.path.slice(0, slash);
  const Icon = n.glob ? Lock : FileText;
  return (
    <Shell
      n={n}
      picked={picked}
      dim={dim}
      label={n.glob ? `Files under ${n.path}` : `${n.path}${n.changed ? `, +${n.added} −${n.removed}` : ""}`}
      className="flex items-center gap-2 rounded-full px-3"
      onPick={onPick}
      onHover={onHover}
    >
      <Icon className={cn("size-3.5 shrink-0", n.changed ? "text-info-foreground" : "text-muted-foreground")} />
      <span className="flex min-w-0 flex-1 items-baseline gap-1.5">
        <span className="shrink-0 font-mono text-xs">{name}</span>
        {dir !== "" && <span className="min-w-0 truncate font-mono text-3xs text-faint">{dir}</span>}
      </span>
      {n.changed && (
        <span className="shrink-0 font-mono text-2xs tabular-nums">
          <span className="text-diff-addition-foreground">+{n.added}</span> <span className="text-diff-deletion-foreground">−{n.removed}</span>
        </span>
      )}
    </Shell>
  );
});

// The changed files no one is linked to, as one node.
export const FoldCard = memo(function FoldCard({ n, picked, dim, onPick, onHover }: NodeProps<FoldGraphNode>) {
  return (
    <Shell n={n} picked={picked} dim={dim} label={`${n.paths.length} more changed files`} className="flex items-center gap-2 rounded-full px-3" onPick={onPick} onHover={onHover}>
      <Files className="size-3.5 shrink-0 text-info-foreground" />
      <span className="min-w-0 flex-1 truncate text-xs">{n.paths.length} more files</span>
      <span className="shrink-0 font-mono text-2xs tabular-nums">
        <span className="text-diff-addition-foreground">+{n.added}</span> <span className="text-diff-deletion-foreground">−{n.removed}</span>
      </span>
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
