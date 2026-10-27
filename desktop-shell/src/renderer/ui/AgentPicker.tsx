import type { ServerInfo, Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { type ComponentProps, type ReactNode, useState } from "react";
import { type AgentModels, currentModel, DEFAULT_LABEL, modelRows, useAgentCatalog } from "../agentCatalog";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import { Check, Loader2, RefreshCw } from "./icons";
import { MenuItem } from "./kit/menu";

// One choice in a picker: a radio item of the menu it sits in, so the
// arrows, Home and End move through every section alike, and Enter or Space
// picks. The mode picker uses it; the agent picker's denser cells below
// keep the same role and keys.
export function PickRow({
  checked,
  lead,
  label,
  detail,
  className,
  ...props
}: Omit<ComponentProps<"button">, "children"> & { checked: boolean; lead?: ReactNode; label: ReactNode; detail?: ReactNode }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={checked}
      className={cn(
        "pick flex w-full cursor-pointer items-start gap-2.5 rounded-[9px] px-2 py-1.5 text-left outline-none transition-colors hover:bg-hover focus-visible:bg-selected",
        checked && "bg-selected hover:bg-selected",
        className,
      )}
      {...props}
    >
      {lead !== undefined && <span className="mt-0.5 grid size-3.5 shrink-0 place-items-center">{lead}</span>}
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate font-medium text-[13px]">{label}</span>
        {detail !== undefined && detail !== "" && <span className="text-muted-foreground text-xs leading-snug">{detail}</span>}
      </span>
      {checked && <Check className="mt-0.5 size-3.5 shrink-0 text-foreground" weight="bold" />}
    </button>
  );
}

// A model in the picker's grid: one line, its name and, when the name is
// not its id, the id after it in small type.
function ModelCell({ checked, label, detail, ...props }: Omit<ComponentProps<"button">, "role" | "children"> & { checked: boolean; label: string; detail: string }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={checked}
      className={cn(
        "pick pick-model flex h-7 min-w-0 cursor-pointer items-center gap-1.5 rounded-[7px] px-2 text-left text-xs outline-none transition-colors hover:bg-hover focus-visible:bg-selected",
        checked && "bg-selected hover:bg-selected",
      )}
      {...props}
    >
      <span className="truncate font-medium">{label}</span>
      {detail !== "" && <span className="min-w-0 truncate font-mono text-3xs text-faint">{detail}</span>}
      {checked && <Check className="ml-auto size-3 shrink-0 text-foreground" weight="bold" />}
    </button>
  );
}

// One segment of a segmented control: an agent kind, or an effort.
function Segment({ checked, className, ...props }: Omit<ComponentProps<"button">, "role"> & { checked: boolean }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={checked}
      className={cn(
        "flex h-6 shrink-0 cursor-pointer items-center gap-1 rounded-full px-2 text-2xs text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
        checked && "bg-selected font-medium text-foreground",
        className,
      )}
      {...props}
    />
  );
}

const segments = "flex min-w-0 flex-wrap items-center gap-0.5 rounded-full border border-hairline p-0.5";

export interface Pick {
  // The agent picked: its kind, or a named agent's name (named).
  agent: string;
  kind: string;
  model: string;
  effort: string;
  // Whether model or effort differ from what the kind runs now.
  changed: boolean;
}

// What an agent of kind (or the named agent) runs now; undefined for the
// CLI's default.
export type Running = (agent: string) => { model?: string | undefined; effort?: string | undefined };

// A named agent the agent section offers after the kinds: it runs its kind's
// models, and wears its own colour.
export interface NamedAgent {
  name: string;
  kind: string;
  color?: string | undefined;
}

// What the pickers of a room share: the catalog, and Refresh.
export interface PickerSource {
  agents: readonly AgentModels[] | null;
  loading: boolean;
  listedAt: number | null;
  onRefresh: () => void;
}

// The pickers' source for a room (sid; null on the new-thread screen) of
// the Deck server describes: its catalog, listed once per Deck and kept
// (agentCatalog.ts), so a second picker asks nothing.
export function usePickerSource(c: DeckClient, server: ServerInfo | null, sid: string | null): PickerSource {
  const catalog = useAgentCatalog(server === null ? null : { deck: server.server_id, version: server.version, kinds: server.kinds }, (kind, refresh) =>
    c.listModels(sid ?? "", kind, refresh),
  );
  return { agents: catalog.catalog?.agents ?? null, loading: catalog.loading, listedAt: catalog.catalog?.at ?? null, onRefresh: catalog.refresh };
}

const ago = (at: number, now: number) => {
  const m = Math.round((now - at) / 60_000);
  return m < 1 ? "just now" : m < 60 ? `${m} min ago` : m < 48 * 60 ? `${Math.round(m / 60)} h ago` : `${Math.round(m / 1440)} days ago`;
};

// The one agent picker, for every agent alike, laid out wide and short:
// the agent as a segmented control (when the picker starts one; a lane's
// picker is for its own), the models as a two-column grid of one-line
// cells, then one bar with the model's reasoning efforts as a segmented
// control, Refresh and the action. Each section shows only when it has a
// choice. The radios only choose; the action applies the choice. The lists
// come from the catalog (agentCatalog.ts), already loaded: opening a picker
// asks nothing.
export function AgentPicker({
  source,
  kinds,
  named = [],
  kind,
  start,
  running,
  theme,
  action,
  needsChange = false,
  onPick,
}: {
  source: PickerSource;
  // The kinds the agent section offers, and the named agents after them.
  kinds: readonly string[];
  named?: readonly NamedAgent[];
  // The agent the picker is for; null shows the agent section.
  kind: string | null;
  // With the agent section, the agent it starts on (a kind or a name).
  start?: string | undefined;
  running: Running;
  theme: Theme | null;
  // The action item's label for the chosen kind.
  action: (kind: string) => string;
  // The action waits for a change (a lane's picker); otherwise it always applies.
  needsChange?: boolean;
  onPick: (p: Pick) => void;
}) {
  const { agents, loading, listedAt, onRefresh } = source;
  const startAgent = kind ?? start ?? kinds[0] ?? "";
  const initial = (a: string) => ({ model: currentModel(running(a).model), effort: (running(a).effort ?? "").toLowerCase() });
  // k is the agent picked: a kind, or a named agent's name; kk its kind.
  const [k, setK] = useState(startAgent);
  const [choice, setChoice] = useState(() => initial(startAgent));
  const namedK = named.find((n) => n.name === k);
  const kk = namedK?.kind ?? k;
  const entry = agents?.find((a) => a.kind === kk);
  const rows = modelRows(entry?.models ?? [], running(k).model);
  const model = rows.find((m) => m.id === choice.model) ?? rows[0];
  const efforts = model?.efforts ?? [];
  const now = initial(k);
  const changed = choice.model !== now.model || choice.effort !== now.effort;
  const pickKind = (next: string) => {
    setK(next);
    setChoice(initial(next));
  };
  const pickModel = (id: string) => {
    const keeps = rows.find((m) => m.id === id)?.efforts.includes(choice.effort) === true;
    setChoice({ model: id, effort: keeps ? choice.effort : "" });
  };
  const mark = (of: string, color?: string) => (
    <span aria-hidden className="font-mono text-[11px] leading-none" style={{ color: color || kindColour(of) }}>
      {kindMark(theme, of) || "●"}
    </span>
  );

  return (
    <div className="agent-picker flex flex-col gap-1" data-kind={kk}>
      {kind === null &&
        (kinds.length === 0 && named.length === 0 ? (
          <p className="px-2 py-1 text-muted-foreground text-xs">This Deck names no agents.</p>
        ) : (
          <div className={cn(segments, "self-start")} role="group" aria-label="Agent">
            {kinds.map((a) => (
              <Segment key={a} checked={a === k} className="text-xs" onClick={() => pickKind(a)}>
                {mark(a)}
                {a}
              </Segment>
            ))}
            {named.map((n) => (
              <Segment key={`@${n.name}`} checked={n.name === k} className="named text-xs" title={`${n.name}, a named ${n.kind}`} onClick={() => pickKind(n.name)}>
                {mark(n.kind, n.color)}@{n.name}
              </Segment>
            ))}
          </div>
        ))}
      {k !== "" && (
        <>
          <div className="pick-models grid max-h-52 grid-cols-2 gap-0.5 overflow-y-auto" role="group" aria-label="Model">
            {rows.map((m) => (
              <ModelCell
                key={m.id}
                checked={m.id === model?.id}
                label={m.label}
                detail={m.id !== "" && m.label.toLowerCase() !== m.id.toLowerCase() ? m.id : ""}
                title={m.id === "" ? (namedK === undefined ? `The model ${kk} picks itself` : `${k}'s own model`) : m.id}
                onClick={() => pickModel(m.id)}
              />
            ))}
          </div>
          {agents === null && loading && (
            <p className="flex items-center gap-2 px-2 text-muted-foreground text-xs">
              <Loader2 className="size-3 animate-spin" /> Listing models…
            </p>
          )}
          {entry?.failed === true && <p className="px-2 text-muted-foreground text-xs">Deck could not list {kk}'s models.</p>}
        </>
      )}
      <div className="pick-bar flex items-center gap-1 border-hairline border-t pt-1">
        {k !== "" && efforts.length > 0 && (
          <div className={segments} role="group" aria-label="Reasoning effort" title="Reasoning effort">
            <Segment checked={choice.effort === ""} onClick={() => setChoice({ ...choice, effort: "" })}>
              {DEFAULT_LABEL}
            </Segment>
            {efforts.map((e) => (
              <Segment key={e} className="effort" checked={choice.effort === e} onClick={() => setChoice({ ...choice, effort: e })}>
                {e}
              </Segment>
            ))}
          </div>
        )}
        <MenuItem
          className="ml-auto w-auto shrink-0 text-muted-foreground text-xs"
          aria-label="Refresh agents"
          title={listedAt !== null && !loading ? `Refresh agents (listed ${ago(listedAt, Date.now())})` : "Refresh agents"}
          disabled={loading}
          onClick={onRefresh}
        >
          {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
          {listedAt !== null && !loading && <span className="text-2xs text-faint">{ago(listedAt, Date.now())}</span>}
        </MenuItem>
        {k !== "" && (
          <MenuItem
            className="pick-apply w-auto shrink-0 bg-selected px-3 font-medium text-xs hover:bg-hover"
            disabled={needsChange && !changed}
            onClick={() => onPick({ agent: k, kind: kk, model: model?.id ?? "", effort: efforts.includes(choice.effort) ? choice.effort : "", changed })}
          >
            {action(k)}
          </MenuItem>
        )}
      </div>
    </div>
  );
}
