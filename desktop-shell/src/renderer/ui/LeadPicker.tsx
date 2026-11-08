import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type CSSProperties, useState } from "react";
import { currentModel, modelRows } from "../agentCatalog";
import { agentColour } from "../agents";
import { effortLevels } from "../effort";
import { cn } from "../lib/cn";
import { tintOf } from "../teamGate";
import { AgentPicker, type NamedAgent, type Pick, type PickerSource } from "./AgentPicker";
import { EffortPicker } from "./EffortPicker";
import { ChevronDown } from "./icons";
import { Popover } from "./kit/menu";

// The thread's agent (docs/design/lead-agent-and-subthreads.md): one chip
// in the composer's bar, the agent the person talks to, with its model. It
// opens the agent picker: a kind or a named agent, then its model and
// effort. Every message goes to that agent; it calls subagents and opens
// sub-threads itself, so there is no "@" to address anyone. Its reasoning
// effort sits right after the chip as a glyph with no word (EffortPicker),
// a click away from the levels its model takes.
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
  // effort set, means it takes none and the bar shows no glyph.
  const kindModels = source.agents?.find((a) => a.kind === lead.kind)?.models ?? [];
  const running = modelRows(kindModels, lead.model).find((m) => m.id === currentModel(lead.model));
  const effort = lead.effort.trim().toLowerCase();
  const levels = effortLevels(running?.efforts ?? [], effort);
  const pickEffort = (e: string) =>
    onPick({ agent: lead.profile !== "" ? lead.profile : lead.kind, kind: lead.kind, model: currentModel(lead.model), effort: e, changed: true });
  return (
    <>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        label="Agent"
        side="top"
        className="w-[26rem] max-w-[calc(100vw-2rem)]"
        trigger={
          <button
            type="button"
            className="lead-picker group/lead flex h-7 min-w-0 max-w-64 shrink cursor-pointer items-center gap-1.5 rounded-full px-2 text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected"
            style={{ "--hue": colour } as CSSProperties}
            aria-haspopup="menu"
            aria-expanded={open}
            aria-label={`Agent: ${label}${model !== "" ? `, ${model}` : ""}. Change it`}
            data-lead={lead.kind}
            title="The agent you talk to; it calls subagents and opens sub-threads itself"
            onClick={() => setOpen((o) => !o)}
          >
            <span aria-hidden className="font-mono text-(--hue) text-[11px] leading-none">
              {kindMark(theme, lead.kind) || "●"}
            </span>
            <span className="lead-name truncate font-medium text-foreground">{label}</span>
            {model !== "" && <span className="lead-model hidden truncate text-faint @[34rem]/bar:inline">{model}</span>}
            <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", STATUS_DOT[status] ?? "bg-muted-foreground/40")} />
            <ChevronDown aria-hidden className="size-3 shrink-0 text-faint" />
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
      {levels.length > 0 && <EffortPicker levels={levels} effort={effort} model={model !== "" ? model : (running?.label ?? "")} onPick={pickEffort} />}
    </>
  );
}
