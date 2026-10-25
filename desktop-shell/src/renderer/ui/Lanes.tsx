import type { ModelChoice, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { CornerDownRight, Lock, Square } from "lucide-react";
import { useState } from "react";
import { agentColour } from "../agents";
import { collapseIdle, type Lane } from "../lanes";
import { cn } from "../lib/cn";
import { Models } from "./AgentsStrip";
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

const chip =
  "lane relative flex h-6 max-w-56 shrink-0 cursor-pointer items-center gap-1.5 rounded-md px-1.5 text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-pressed:bg-selected aria-pressed:text-foreground";

function AgentChip({
  lane,
  theme,
  on,
  onFilter,
  listModels,
  setModel,
  onStop,
  onSteer,
}: {
  lane: Lane;
  theme: Theme | null;
  on: boolean;
  onFilter: () => void;
  listModels: () => Promise<ModelChoice[] | null>;
  setModel: (model: string, effort?: string) => void;
  onStop: () => void;
  onSteer: () => void;
}) {
  const [load, setLoad] = useState<Promise<ModelChoice[] | null> | null>(null);
  const close = () => setLoad(null);
  const colour = agentColour(lane.name, lane.agentKind);
  const running = lane.status === "working" || lane.status === "waiting";
  return (
    <Popover
      open={load !== null}
      onClose={close}
      label={`${lane.name}`}
      className="w-64"
      trigger={
        <button
          type="button"
          className={chip}
          data-lane={lane.id}
          data-status={lane.status}
          aria-pressed={on}
          aria-expanded={load !== null}
          aria-label={`${lane.name} ${WORD[lane.status]}`}
          title={`${lane.name}: ${lane.model || "default model"}${lane.effort !== "" ? ` · ${lane.effort}` : ""}`}
          onClick={() => {
            onFilter();
            setLoad(load === null ? listModels() : null);
          }}
        >
          {lane.depth > 0 && <CornerDownRight aria-hidden className="size-3 shrink-0 text-faint" />}
          <span aria-hidden className="font-mono text-[11px]" style={{ color: colour }}>
            {kindMark(theme, lane.agentKind) || "●"}
          </span>
          <span className="lane-name truncate font-mono text-2xs text-foreground/90">{lane.name}</span>
          {lane.status === "working" && <span aria-hidden className="size-1.5 shrink-0 animate-deck-pulse rounded-full bg-signal" />}
          {lane.status === "waiting" && <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-warning" />}
          {lane.status === "error" && <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-destructive" />}
          <span className={cn("lane-status shrink-0 text-2xs", lane.status === "waiting" ? "text-warning-foreground" : "text-faint")}>
            {WORD[lane.status]}
          </span>
          {lane.contextPct !== null && (
            <span className="flex items-center" title={`${lane.contextPct}% of its context`}>
              <Ring pct={lane.contextPct} className="lane-ring" />
            </span>
          )}
          {lane.leases > 0 && (
            <span className="lane-leases flex items-center gap-0.5 font-mono text-3xs text-faint tabular-nums" title={`Holds ${lane.leases} ${lane.leases === 1 ? "path" : "paths"}`}>
              <Lock aria-hidden className="size-2.5" />
              {lane.leases}
            </span>
          )}
        </button>
      }
    >
      <MenuLabel>
        {lane.name} · {lane.model || "default model"}
        {lane.effort !== "" ? ` · ${lane.effort}` : ""}
      </MenuLabel>
      <Models
        line={{ id: lane.name, name: lane.name, kind: lane.agentKind, model: lane.model, effort: lane.effort, busy: running }}
        load={load ?? Promise.resolve(null)}
        choose={(model, effort) => {
          setModel(model, effort);
          close();
        }}
      />
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
  listModels,
  setModel,
  onStop,
  onSteer,
}: {
  lanes: Lane[];
  theme: Theme | null;
  // The lane the timeline shows alone, or null for all.
  filter: string | null;
  onFilter: (lane: string | null) => void;
  listModels: (kind: string) => Promise<ModelChoice[] | null>;
  setModel: (kind: string, model: string, effort?: string) => void;
  onStop: (agent: string) => void;
  onSteer: (agent: string) => void;
}) {
  const [all, setAll] = useState(false);
  const { shown, idle } = collapseIdle(lanes);
  const list = all ? lanes : shown;
  const toggle = (id: string) => onFilter(filter === id ? null : id);
  return (
    <div className="lanes flex min-h-8 shrink-0 items-center gap-0.5 overflow-x-auto border-hairline border-b px-3 pb-1" role="toolbar" aria-label="Lanes">
      {list.map((l) =>
        l.kind === "agent" ? (
          <AgentChip
            key={l.id}
            lane={l}
            theme={theme}
            on={filter === l.id}
            onFilter={() => toggle(l.id)}
            listModels={() => listModels(l.agentKind)}
            setModel={(model, effort) => setModel(l.agentKind, model, effort)}
            onStop={() => onStop(l.name)}
            onSteer={() => onSteer(l.name)}
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
              <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-muted-foreground" />
            ) : (
              <span aria-hidden className="font-mono text-[11px] text-faint">
                ◇
              </span>
            )}
            <span className="lane-name truncate font-mono text-2xs text-foreground/90">{l.name}</span>
            {l.kind === "mate" && <span className="lane-role text-2xs text-faint">{l.role}</span>}
          </button>
        ),
      )}
      {idle.length > 0 && (
        <button
          type="button"
          className={cn(chip, "lane-idle font-mono text-2xs text-faint")}
          aria-expanded={all}
          title={idle.map((l) => l.name).join(", ")}
          onClick={() => setAll((v) => !v)}
        >
          {all ? "fewer" : `+${idle.length} idle`}
        </button>
      )}
    </div>
  );
}
