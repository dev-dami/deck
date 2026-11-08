import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type CSSProperties, useLayoutEffect, useRef, useState } from "react";
import { currentModel, modelRows } from "../agentCatalog";
import { agentColour } from "../agents";
import { effortLevels } from "../effort";
import { cn } from "../lib/cn";
import { tintOf } from "../teamGate";
import { AgentPicker, type NamedAgent, type Pick, type PickerSource } from "./AgentPicker";
import { EffortDial } from "./EffortDial";
import { Popover } from "./kit/menu";

// The thread's agent (docs/design/lead-agent-and-subthreads.md): one
// control in the composer's bar, the agent the person talks to, its model
// and its reasoning effort, after T3 Code's single model-and-effort control.
// The chip opens the agent picker: a kind or a named agent, then its model
// and effort. Every message goes to that agent; it calls subagents and
// opens sub-threads itself, so there is no "@" to address anyone. The
// effort dial is attached to the chip's end (EffortDial): turn it, or
// click it for the levels its model takes. A new agent or model slides into
// the chip, which eases to its new width on the spring.
export interface Lead {
  // Its name in the thread, or the kind or named agent picked for a thread
  // that has not started.
  agent: string;
  kind: string;
  // What it was asked to run (an alias, "" for the CLI's default) and the
  // model that is ("claude-opus-5-5"), which the chip shows when known.
  model: string;
  slug?: string;
  effort: string;
  // The named agent it runs as, "" for a plain kind.
  profile: string;
  color?: string;
}

const STATUS_DOT: Record<string, string> = {
  busy: "bg-signal animate-deck-pulse",
  waiting: "bg-warning",
  working: "bg-signal animate-deck-pulse",
  tool: "bg-signal animate-deck-pulse",
  approval: "bg-warning",
  error: "bg-destructive",
};

// Eases el from the width it had to the one it has now, on the spring,
// clipping what does not fit yet. Reduced motion jumps.
function easeWidth(el: HTMLElement, from: number) {
  const to = el.getBoundingClientRect().width;
  const root = document.documentElement;
  if (Math.abs(to - from) < 1 || root.dataset["motion"] === "reduced" || typeof el.animate !== "function") return;
  const spring = getComputedStyle(root).getPropertyValue("--spring").trim() || "ease";
  el.style.overflow = "hidden";
  const run = el.animate([{ width: `${from}px` }, { width: `${to}px` }], { duration: 520, easing: spring });
  const done = () => {
    el.style.overflow = "";
  };
  run.onfinish = done;
  run.oncancel = done;
}

export function LeadPicker({
  lead,
  status = "idle",
  source,
  kinds,
  named = [],
  theme,
  onPick,
}: {
  lead: Lead;
  // What the agent is doing ("idle", "busy", "waiting" ...).
  status?: string | undefined;
  source: PickerSource;
  kinds: readonly string[];
  named?: readonly NamedAgent[];
  theme: Theme | null;
  onPick: (p: Pick) => void;
}) {
  const [open, setOpen] = useState(false);
  const colour = agentColour(lead.agent, lead.kind, tintOf(lead.color));
  const label = lead.profile !== "" ? lead.profile : lead.kind;
  const model = lead.slug || lead.model;
  // The levels the lead's model takes, from the catalog; none, and no
  // effort set, means it takes none and the bar shows no dial.
  const kindModels = source.agents?.find((a) => a.kind === lead.kind)?.models ?? [];
  const running = modelRows(kindModels, lead.model).find((m) => m.id === currentModel(lead.model));
  const effort = lead.effort.trim().toLowerCase();
  const levels = effortLevels(running?.efforts ?? [], effort);
  const pickEffort = (e: string) =>
    onPick({ agent: lead.profile !== "" ? lead.profile : lead.kind, kind: lead.kind, model: currentModel(lead.model), effort: e, changed: true });

  // A new agent or model slides in; the control eases to its new width.
  const swap = `${lead.kind}|${label}|${model}|${levels.length > 0}`;
  const box = useRef<HTMLDivElement>(null);
  const width = useRef(0);
  const shown = useRef(swap);
  useLayoutEffect(() => {
    const el = box.current;
    if (el === null) return;
    if (shown.current !== swap && width.current > 0) easeWidth(el, width.current);
    shown.current = swap;
    const keep = () => {
      width.current = el.getBoundingClientRect().width;
    };
    keep();
    const ro = new ResizeObserver(keep);
    ro.observe(el);
    return () => ro.disconnect();
  }, [swap]);

  return (
    <div
      ref={box}
      className="lead-control flex h-7 min-w-0 max-w-72 shrink items-center rounded-full border border-hairline"
      style={{ "--hue": colour } as CSSProperties}
      data-lead={lead.kind}
    >
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        label="Agent"
        side="top"
        className="lead-pop w-[30rem] max-w-[calc(100vw-2rem)]"
        trigger={
          <button
            type="button"
            className={cn(
              "lead-picker group/lead flex h-[26px] min-w-0 shrink cursor-pointer items-center gap-1.5 rounded-full pr-2 pl-2.5 text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected",
              levels.length > 0 && "pr-1.5",
            )}
            aria-haspopup="menu"
            aria-expanded={open}
            aria-label={`Agent: ${label}${model !== "" ? `, ${model}` : ""}. Change it`}
            title="The agent you talk to; it calls subagents and opens sub-threads itself"
            onClick={() => setOpen((o) => !o)}
          >
            <span aria-hidden className="font-mono text-(--hue) text-[11px] leading-none">
              {kindMark(theme, lead.kind) || "●"}
            </span>
            <span key={swap} className="lead-swap flex min-w-0 items-baseline gap-1.5">
              <span className="lead-name truncate font-medium text-foreground">{label}</span>
              {model !== "" && <span className="lead-model hidden truncate text-faint @[34rem]/bar:inline">{model}</span>}
            </span>
            <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", STATUS_DOT[status] ?? "bg-muted-foreground/40")} />
          </button>
        }
      >
        <AgentPicker
          source={source}
          kinds={kinds}
          named={named}
          kind={null}
          start={lead.profile !== "" ? lead.profile : lead.kind}
          running={(a) => (a === lead.kind || a === lead.profile ? { model: lead.model, effort: lead.effort } : {})}
          theme={theme}
          action={(k) => (k === label ? "Use this model" : `Talk to ${k}`)}
          onPick={(p) => {
            setOpen(false);
            onPick(p);
          }}
        />
      </Popover>
      {levels.length > 0 && (
        <>
          <span aria-hidden className="h-3.5 w-px shrink-0 bg-hairline" />
          <EffortDial levels={levels} effort={effort} model={model !== "" ? model : (running?.label ?? "")} onPick={pickEffort} />
        </>
      )}
    </div>
  );
}
