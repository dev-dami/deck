import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { CornerDownRight, Lock, Square } from "./icons";
import { type ReactNode, useLayoutEffect, useRef, useState } from "react";
import { agentColour } from "../agents";
import { tintOf } from "../teamGate";
import { fitLanes, type Lane } from "../lanes";
import { cn } from "../lib/cn";
import { AgentRole } from "./AgentRole";
import { AgentPicker, type PickerSource } from "./AgentPicker";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "./kit/menu";
import { Ring } from "./kit/ring";

// The lane strip under the room header: one chip per participant. A click
// filters the timeline to that lane; on an agent it also opens its model,
// effort, Stop and Steer.

const WORD: Record<Lane["status"], string> = {
  working: "working",
  waiting: "waiting",
  error: "error",
  idle: "idle",
  exited: "exited",
};

// Each lane is the agent as Deck draws it: its glyph in its hue and its
// name, with no container. The glyph is lit while the agent works or asks
// for something, and dimmed while it rests. The lane the timeline shows
// alone is pressed: a fill of the selection behind it.
const chipBase =
  "relative flex h-7 max-w-60 shrink-0 cursor-pointer items-center gap-1.5 rounded-full px-2 text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-pressed:bg-selected aria-pressed:text-foreground";
const chip = `lane ${chipBase}`;

// Only a state that moves or asks gets a dot; it pulses while the agent works.
const DOT: Partial<Record<Lane["status"], string>> = {
  working: "bg-signal animate-deck-pulse",
  waiting: "bg-warning",
  error: "bg-destructive",
};

// The strip keeps to one line and folds lanes into "+N"; it wraps only for
// lanes that never fold (pinnedLane), and then it grows in the flow.
const LINES = 1;
const GAP = 2;
// The width of a chip not measured yet, and of "+N".
const GUESS = 100;
const MORE = 36;

// One line in the "+N" popover: a lane that did not fit.
function FoldedLane({ lane, on, onFilter }: { lane: Lane; on: boolean; onFilter: () => void }) {
  return (
    <MenuItem className={cn(on && "on bg-selected")} onClick={onFilter}>
      <span className="min-w-0 flex-1 truncate font-mono text-2xs">{lane.name}</span>
      <span className="shrink-0 text-2xs text-faint">{lane.kind === "mate" ? lane.role : lane.kind === "agent" ? WORD[lane.status] : ""}</span>
    </MenuItem>
  );
}

function AgentChip({
  lane,
  theme,
  on,
  onFilter,
  picker,
  setModel,
  onStop,
  onSteer,
  mark,
}: {
  lane: Lane;
  theme: Theme | null;
  // What else the chip shows of the agent (its plan's quota).
  mark?: ReactNode;
  on: boolean;
  onFilter: () => void;
  picker: PickerSource;
  setModel: (model: string, effort?: string) => void;
  onStop: () => void;
  onSteer: () => void;
}) {
  const [open, setOpen] = useState(false);
  const close = () => setOpen(false);
  const colour = agentColour(lane.name, lane.agentKind, tintOf(lane.color));
  const running = lane.status === "working" || lane.status === "waiting";
  return (
    <Popover
      open={open}
      onClose={close}
      label={`${lane.name}`}
      className="w-72"
      trigger={
        <button
          type="button"
          className={chip}
          data-lane={lane.id}
          data-status={lane.status}
          aria-pressed={on}
          aria-expanded={open}
          aria-label={`${lane.name} ${WORD[lane.status]}`}
          title={`${lane.name}: ${lane.model || "default model"}${lane.effort !== "" ? ` · ${lane.effort}` : ""}`}
          onClick={() => {
            onFilter();
            setOpen((o) => !o);
          }}
        >
          {lane.depth > 0 && <CornerDownRight aria-hidden className="-ml-0.5 size-3 shrink-0 text-faint" />}
          <span
            aria-hidden
            className={cn("grid size-3.5 shrink-0 place-items-center font-mono text-[11px] leading-none", !running && (lane.status === "exited" ? "opacity-30" : "opacity-55"))}
            style={{ color: colour }}
          >
            {kindMark(theme, lane.agentKind) || "●"}
          </span>
          <span className={cn("lane-name truncate font-mono text-2xs", running ? "text-foreground" : lane.status === "exited" ? "text-faint" : "text-foreground/80")}>
            {lane.name}
          </span>
          <AgentRole role={lane.role} className="max-w-32 text-2xs" />
          {DOT[lane.status] !== undefined && <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", DOT[lane.status])} />}
          {/* The word shows when the agent asks for something; else it is for
              the tooltip and screen readers. */}
          <span className={cn("lane-status shrink-0 text-2xs", lane.status === "waiting" || lane.status === "error" ? "text-warning-foreground" : "sr-only")}>
            {WORD[lane.status]}
          </span>
          {lane.contextPct !== null && (
            <span className="flex items-center tabular-nums" title={`${lane.contextPct}% of its context`}>
              <Ring pct={lane.contextPct} className="lane-ring" />
            </span>
          )}
          {mark}
          {lane.leases.length > 0 && (
            <span
              className="lane-leases flex items-center gap-0.5 font-mono text-3xs text-faint tabular-nums"
              title={`Holds ${lane.leases.length} ${lane.leases.length === 1 ? "path" : "paths"}`}
            >
              <Lock aria-hidden className="size-2.5" />
              {lane.leases.length}
            </span>
          )}
        </button>
      }
    >
      <MenuLabel>
        {lane.name} · {lane.model || "default model"}
        {lane.effort !== "" ? ` · ${lane.effort}` : ""}
      </MenuLabel>
      <AgentPicker
        source={picker}
        kinds={[lane.agentKind]}
        kind={lane.agentKind}
        running={() => ({ model: lane.model, effort: lane.effort })}
        theme={theme}
        action={() => "Apply"}
        needsChange
        onPick={(p) => {
          close();
          setModel(p.model, p.effort);
        }}
      />
      {lane.leases.length > 0 && (
        <>
          <MenuSeparator />
          <MenuLabel>Holds</MenuLabel>
          <ul className="lane-leased flex max-h-32 flex-col overflow-y-auto px-2 pb-1" aria-label={`Paths ${lane.name} holds`}>
            {lane.leases.map((p) => (
              <li key={p} className="flex items-center gap-1.5 truncate py-0.5 font-mono text-2xs text-muted-foreground" title={p}>
                <Lock aria-hidden className="size-2.5 shrink-0 text-faint" />
                <span className="truncate">{p}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      <MenuSeparator />
      <MenuItem
        onClick={() => {
          close();
          onSteer();
        }}
      >
        <CornerDownRight /> Steer {lane.name}
      </MenuItem>
      <MenuItem
        className="text-destructive-foreground"
        disabled={!running}
        onClick={() => {
          close();
          onStop();
        }}
      >
        <Square /> Stop {lane.name}
      </MenuItem>
    </Popover>
  );
}

export function Lanes({
  lanes,
  theme,
  filter,
  onFilter,
  picker,
  setModel,
  onStop,
  onSteer,
  mark,
}: {
  lanes: Lane[];
  // What else an agent's chip shows (its plan's quota).
  mark?: (lane: Lane) => ReactNode;
  theme: Theme | null;
  // The lane the timeline shows alone, or null for all.
  filter: string | null;
  onFilter: (lane: string | null) => void;
  // The agents' models, for each agent's picker.
  picker: PickerSource;
  setModel: (kind: string, model: string, effort?: string) => void;
  onStop: (agent: string) => void;
  onSteer: (agent: string) => void;
}) {
  const [more, setMore] = useState(false);
  // The row's inner width, and each chip's width as it last rendered.
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const widths = useRef(new Map<string, number>());
  const [, setMeasured] = useState(0);
  useLayoutEffect(() => {
    const el = box.current;
    if (el === null) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e?.contentRect.width ?? 0)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  useLayoutEffect(() => {
    let changed = false;
    for (const el of box.current?.querySelectorAll<HTMLElement>("[data-lane]") ?? []) {
      const id = el.dataset["lane"] ?? "";
      const w = Math.ceil(el.getBoundingClientRect().width);
      if (w > 0 && widths.current.get(id) !== w) {
        widths.current.set(id, w);
        changed = true;
      }
    }
    if (changed) setMeasured((n) => n + 1);
  });
  const { shown, folded } = fitLanes(lanes, (l) => widths.current.get(l.id) ?? GUESS, width, { gap: GAP, lines: LINES, more: MORE });
  const toggle = (id: string) => onFilter(filter === id ? null : id);
  return (
    // One line of chips under the toolbar, on the sheet with no rule of its own.
    <div
      ref={box}
      className="lanes flex min-h-9 shrink-0 flex-wrap content-center items-center gap-0.5 px-2 pb-1.5"
      role="toolbar"
      aria-label="Lanes"
      data-yolo-region=""
    >
      {shown.map((l) =>
        l.kind === "agent" ? (
          <AgentChip
            key={l.id}
            lane={l}
            theme={theme}
            on={filter === l.id}
            onFilter={() => toggle(l.id)}
            picker={picker}
            setModel={(model, effort) => setModel(l.agentKind, model, effort)}
            onStop={() => onStop(l.name)}
            onSteer={() => onSteer(l.name)}
            mark={mark?.(l)}
          />
        ) : (
          <button
            key={l.id}
            type="button"
            className={chip}
            data-lane={l.id}
            aria-pressed={filter === l.id}
            aria-label={l.kind === "you" ? "you" : `${l.name} ${l.role}`}
            onClick={() => toggle(l.id)}
          >
            {l.kind === "you" ? (
              <span aria-hidden className="grid size-3.5 shrink-0 place-items-center">
                <span className="size-1.5 rounded-full bg-foreground/60" />
              </span>
            ) : (
              <span aria-hidden className="grid size-3.5 shrink-0 place-items-center font-mono text-[11px] text-faint">
                ◇
              </span>
            )}
            <span className="lane-name truncate font-mono text-2xs text-foreground/80">{l.name}</span>
            {l.kind === "mate" && <span className="lane-role text-2xs text-faint">{l.role}</span>}
          </button>
        ),
      )}
      {folded.length > 0 && (
        <Popover
          open={more}
          onClose={() => setMore(false)}
          label="More lanes"
          className="w-56"
          trigger={
            <button
              type="button"
              className={cn(chipBase, "lane-more px-2.5 text-2xs text-muted-foreground tabular-nums aria-expanded:bg-selected")}
              aria-expanded={more}
              aria-label={`${folded.length} more`}
              title={folded.map((l) => l.name).join(", ")}
              onClick={() => setMore((v) => !v)}
            >
              +{folded.length}
            </button>
          }
        >
          {folded.map((l) => (
            <FoldedLane
              key={l.id}
              lane={l}
              on={filter === l.id}
              onFilter={() => {
                setMore(false);
                toggle(l.id);
              }}
            />
          ))}
        </Popover>
      )}
    </div>
  );
}
