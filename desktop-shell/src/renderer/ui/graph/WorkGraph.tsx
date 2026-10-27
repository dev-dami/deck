import type { AgentLine, ChangedFile, Message, Theme } from "@web/protocol";
import { type KeyboardEvent, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useRoomCrew } from "../../crewState";
import { fileWork } from "../../fileWork";
import { type Bounds, buildGraph, chooseDirection, type Direction, type Graph, isEmpty, neighbourhood, stepFocus } from "../../graph";
import { agentLane } from "../../lanes";
import { cn } from "../../lib/cn";
import type { DeckClient } from "../../client";
import type { ProjectApi } from "../../project";
import type { Lease } from "../../roomMeta";
import { useSelect } from "../../select";
import { Graph as GraphIcon } from "../icons";
import { EmptyState } from "../kit/empty";
import { segmentItem, segmentTrack } from "../kit/segmented";
import { Legend, Minimap, mapNodes, ZoomCapsule } from "./Controls";
import { AgentDetail } from "./Detail";
import { FileDetail } from "./FileDetail";
import { History } from "./History";
import { Edges } from "./Edges";
import { NodeCard } from "./Nodes";
import { type FitTarget, type Pad, useView } from "./useView";

// The Work Graph (docs/design-glass.md, section 7): one thread's agents,
// sub-agents, tasks and files as an interactive map.
export interface WorkGraphProps {
  // The client, for the History's threads; without it the graph shows the
  // thread now only.
  c?: DeckClient;
  // Opens a thread's conversation at a message (a History node's click).
  onOpenMessage?: (thread: string, mid: string) => void;
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
  // The room's live leases (lease_changed), when the Deck reports them.
  leases?: readonly Lease[];
}

// Now (this thread's crew at work) or History (every thread's work over
// time); the same for every room while the window lives.
type Scope = "now" | "history";
let lastScope: Scope = "now";

export function WorkGraph(props: WorkGraphProps) {
  const { c, api, sid, onOpenMessage } = props;
  const [scope, setScope] = useState<Scope>(lastScope);
  lastScope = scope;
  const room = useRoomCrew(api, sid);
  const history = c !== undefined && onOpenMessage !== undefined;
  return (
    <div className="relative flex min-h-0 flex-1">
      {scope === "history" && history ? (
        <History
          c={c}
          api={api}
          sid={sid}
          tasks={room?.board?.tasks ?? null}
          theme={props.theme}
          compact={props.compact ?? false}
          onOpenMessage={onOpenMessage}
          onOpenFile={props.onOpenFile}
          onOpenTasks={props.onOpenTasks}
        />
      ) : (
        <NowGraph {...props} />
      )}
      {history && (
        <div
          data-graph-control=""
          className={cn(segmentTrack(), "graph-scope absolute top-3 right-3 z-20")}
          role="tablist"
          aria-label="Graph scope"
        >
          {SCOPES.map((s) => (
            <button key={s.scope} type="button" role="tab" aria-selected={scope === s.scope} className={segmentItem({ on: scope === s.scope })} onClick={() => setScope(s.scope)}>
              {s.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

const SCOPES: ReadonlyArray<{ scope: Scope; label: string }> = [
  { scope: "now", label: "Now" },
  { scope: "history", label: "History" },
];

const NO_MESSAGES: readonly Message[] = [];
const noSubscribe = () => () => {};

// The open thread's messages, for who edited which file; none without a client.
function useThreadMessages(c: DeckClient | undefined, sid: string): readonly Message[] {
  return useSelect(
    c?.store.subscribe ?? noSubscribe,
    () => c?.store.state ?? null,
    (s) => s?.messages[sid] ?? NO_MESSAGES,
  );
}

function NowGraph({ api, sid, roster, files, leases, c, onOpenMessage, ...rest }: WorkGraphProps) {
  // The project polls the crew and the board once for the window (crewState.ts).
  const room = useRoomCrew(api, sid);
  const crew = room?.crew ?? null;
  const board = room?.board ?? null;
  const messages = useThreadMessages(c, sid);
  const work = useMemo(() => fileWork(messages, leases ?? []), [messages, leases]);
  const [across, down] = useMemo(() => {
    const input = { roster, crew, board, files, work, ...(leases === undefined ? {} : { leases }) };
    return [buildGraph(input, "across"), buildGraph(input, "down")] as const;
  }, [roster, crew, board, files, leases, work]);
  const openMessage = useMemo(() => (onOpenMessage === undefined ? undefined : (mid: string) => onOpenMessage(sid, mid)), [onOpenMessage, sid]);

  // Across, unless the canvas is tall and narrow enough that down shows the
  // graph larger. A change of direction mounts a new canvas, which fits.
  const box = useRef<HTMLDivElement>(null);
  const [dir, setDir] = useState<Direction | null>(null);
  useLayoutEffect(() => {
    const el = box.current;
    if (el === null) return;
    const look = () => {
      if (el.clientWidth > 0) setDir(chooseDirection(across.bounds, down.bounds, el.clientWidth, el.clientHeight));
    };
    look();
    const ro = new ResizeObserver(look);
    ro.observe(el);
    return () => ro.disconnect();
  }, [across, down]);

  if (isEmpty(across)) {
    return (
      <div className="work-graph flex min-h-0 flex-1 items-center justify-center" data-empty="">
        <EmptyState
          icon={GraphIcon}
          title="Nothing to map yet."
          hint="Send a message. The agents, the tasks they split the work into and the files they touch show here."
          className="max-w-72"
        />
      </div>
    );
  }
  return (
    <div ref={box} className="relative flex min-h-0 flex-1">
      {dir !== null && <Canvas key={`${sid}/${dir}`} g={dir === "down" ? down : across} now={room?.at ?? 0} openMessage={openMessage} {...rest} />}
    </div>
  );
}

// Room for the column labels over the graph.
const LABELS = 30;

const touches = (b: Bounds, s: Bounds) => b.x + b.w >= s.x && b.x <= s.x + s.w && b.y + b.h >= s.y && b.y <= s.y + s.h;

// The canvas: the edges and the cards on one layer that pans and zooms, and
// the controls over it.
function Canvas({
  g,
  now,
  openMessage,
  theme,
  lane,
  compact = false,
  onLane,
  onOpenFile,
  onOpenTasks,
  onSteer,
  onStop,
}: { g: Graph; now: number; openMessage: ((mid: string) => void) | undefined } & Omit<
  WorkGraphProps,
  "api" | "sid" | "roster" | "files" | "leases" | "c" | "onOpenMessage"
>) {
  const [hover, setHover] = useState<string | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  // The canvas is on screen; off it, the live motion stops.
  const [onScreen, setOnScreen] = useState(true);
  const byId = useMemo(() => new Map(g.nodes.map((n) => [n.id, n])), [g]);
  // The neighbourhood that stays bright: the hovered node's, else the picked one's.
  const lit = hover ?? picked;
  const near = useMemo(() => (lit === null || !byId.has(lit) ? null : neighbourhood(g.edges, lit)), [g, byId, lit]);

  // What a fit shows: the graph and its column labels, clear of the
  // controls and of the composer that floats over the room.
  const target = useRef<FitTarget>(null);
  const labelled = !compact && g.dir === "across";
  const labels = labelled ? LABELS : 0;
  target.current = {
    bounds: { x: g.bounds.x, y: g.bounds.y - labels, w: g.bounds.w, h: g.bounds.h + labels },
    pad: (el: HTMLElement): Pad => {
      const composer = Number.parseFloat(getComputedStyle(el).getPropertyValue("--composer-h")) || 0;
      return compact ? { top: 20, right: 20, bottom: 56, left: 20 } : { top: 56, right: 32, bottom: composer + 40, left: 32 };
    },
  };
  const view = useView(target, () => setPicked(null));

  // A new shape fits again, gliding, unless the person moved the view.
  const shape = `${g.nodes.map((n) => n.id).join("|")}/${g.bounds.w}x${g.bounds.h}`;
  const first = useRef(shape);
  useEffect(() => {
    if (shape !== first.current && !view.moved.current) view.fit(true);
    first.current = shape;
  }, [shape, view]);

  useEffect(() => {
    const el = view.root.current;
    if (el === null) return;
    const io = new IntersectionObserver(([e]) => setOnScreen(e?.isIntersecting ?? true));
    io.observe(el);
    return () => io.disconnect();
  }, [view.root]);

  // The host's handlers change each render; the cards keep one.
  const host = useRef({ onOpenFile, onOpenTasks });
  host.current = { onOpenFile, onOpenTasks };
  const pick = useCallback(
    (id: string) => {
      const n = byId.get(id);
      if (n === undefined) return;
      if (n.kind === "agent") setPicked((p) => (p === id ? null : id));
      else if (n.kind === "task") host.current.onOpenTasks();
      else if (n.kind === "fold") host.current.onOpenFile(n.paths[0] ?? "");
      else if (n.kind === "file") {
        // A file the agents edited or asked about shows its timeline; a
        // lease's pattern has no diff: it shows the agent that holds it.
        const holder = n.holders[0];
        if (n.work !== null) setPicked((p) => (p === id ? null : id));
        else if (!n.glob) host.current.onOpenFile(n.path);
        else if (holder !== undefined) setPicked(`agent:${holder}`);
      } else setPicked(null);
    },
    [byId],
  );

  const focusNode = (id: string) => {
    const el = view.root.current?.querySelector<HTMLElement>(`[data-node="${CSS.escape(id)}"]`);
    const n = byId.get(id);
    el?.focus({ preventScroll: true });
    if (n !== undefined) view.reveal(n);
  };

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    // A dialog over the graph (an edit's diff) has its own keys.
    if (e.metaKey || e.ctrlKey || e.altKey || (e.target as HTMLElement).closest('[role="dialog"]') !== null) return;
    const id = (e.target as HTMLElement).closest("[data-node]")?.getAttribute("data-node") ?? null;
    if (e.key.startsWith("Arrow")) {
      if (id === null) return;
      e.preventDefault();
      const next = stepFocus(g.nodes, id, e.key, g.dir);
      if (next !== null && next !== id) focusNode(next);
    } else if (e.key === "+" || e.key === "=") view.zoomBy(1.25, undefined, true);
    else if (e.key === "-" || e.key === "_") view.zoomBy(1 / 1.25, undefined, true);
    else if (e.key === "0") view.fit(true);
    else if (e.key === "Escape" && picked !== null) {
      e.stopPropagation();
      setPicked(null);
    } else return;
  };

  // A tab to a card out of sight scrolls the canvas, which only pans: the
  // scroll turns into a pan of the view.
  const onScroll = () => {
    const el = view.root.current;
    if (el === null || (el.scrollLeft === 0 && el.scrollTop === 0)) return;
    const v = view.view.current;
    const dx = el.scrollLeft;
    const dy = el.scrollTop;
    el.scrollLeft = 0;
    el.scrollTop = 0;
    view.set({ ...v, x: v.x - dx, y: v.y - dy }, { user: true });
  };

  const map = useMemo(() => mapNodes(g), [g]);
  const sel = picked === null ? undefined : byId.get(picked);
  const parent = sel?.kind === "agent" && sel.parent !== null ? byId.get(sel.parent) : undefined;
  const seen = view.seen;

  return (
    <div
      ref={view.root}
      role="group"
      aria-label="Work graph"
      aria-roledescription="graph"
      data-live={onScreen ? "on" : "off"}
      data-dir={g.dir}
      className={cn(
        "work-graph relative isolate flex min-h-0 flex-1 cursor-grab touch-none select-none overflow-hidden outline-none data-moving:cursor-grabbing",
        compact && "work-graph-compact",
      )}
      onKeyDown={onKey}
      onScroll={onScroll}
    >
      <div ref={view.layer} className="graph-layer absolute top-0 left-0 origin-top-left">
        <Edges dir={g.dir} edges={g.edges} nodes={byId} near={near} seen={seen} />
        {labelled &&
          g.layers.map(
            (c) =>
              c.label !== "" && (
                <span
                  key={c.layer}
                  aria-hidden
                  className="absolute top-0 left-0 whitespace-nowrap font-medium text-2xs text-faint"
                  style={{ transform: `translate(${c.at + 1}px, ${g.bounds.y - LABELS + 6}px)` }}
                >
                  {c.label}
                </span>
              ),
          )}
        {g.nodes.map((n) => (
          <NodeCard
            key={n.id}
            n={n}
            picked={n.id === picked}
            dim={near !== null && !near.has(n.id)}
            live={onScreen && (seen === null || touches(n, seen))}
            theme={theme}
            onPick={pick}
            onHover={setHover}
          />
        ))}
      </div>
      {!compact && <Legend g={g} />}
      {!compact && <Minimap bounds={g.bounds} nodes={map} view={view} style={{ bottom: "calc(var(--composer-h, 0px) + 3.5rem)" }} />}
      <ZoomCapsule
        view={view}
        className="absolute right-3 z-10"
        style={{ bottom: compact ? "0.75rem" : "calc(var(--composer-h, 0px) + 0.75rem)" }}
      />
      {sel?.kind === "agent" && (
        <AgentDetail
          key={sel.id}
          a={sel}
          parent={parent?.kind === "agent" ? parent.name : null}
          theme={theme}
          lane={lane}
          now={now}
          compact={compact}
          onLane={onLane}
          onSteer={onSteer}
          onStop={onStop}
          onClose={() => setPicked(null)}
        />
      )}
      {sel?.kind === "file" && sel.work !== null && (
        <FileDetail
          key={sel.id}
          path={sel.path}
          work={sel.work}
          changed={sel.changed}
          theme={theme}
          kindOf={(name) => {
            const a = byId.get(agentLane(name));
            return a?.kind === "agent" ? a.agentKind : name.replace(/[-_]?\d+$/, "");
          }}
          compact={compact}
          onOpenMessage={openMessage}
          onOpenFile={onOpenFile}
          onClose={() => setPicked(null)}
        />
      )}
    </div>
  );
}
