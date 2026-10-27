import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { type ComponentProps, type ReactNode, useState } from "react";
import { type AgentModels, currentModel, DEFAULT_LABEL, modelRows } from "../agentCatalog";
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
  kind: string;
  model: string;
  effort: string;
  // Whether model or effort differ from what the kind runs now.
  changed: boolean;
}

// What an agent of kind runs now; undefined for the CLI's default.
export type Running = (kind: string) => { model?: string | undefined; effort?: string | undefined };

// What the pickers of a room share: the catalog, and Refresh.
export interface PickerSource {
  agents: readonly AgentModels[] | null;
  loading: boolean;
  listedAt: number | null;
  onRefresh: () => void;
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
  kind,
  running,
  theme,
  action,
  needsChange = false,
  onPick,
}: {
  source: PickerSource;
  // The kinds the agent section offers.
  kinds: readonly string[];
  // The agent the picker is for; null shows the agent section.
  kind: string | null;
  running: Running;
  theme: Theme | null;
  // The action item's label for the chosen kind.
  action: (kind: string) => string;
  // The action waits for a change (a lane's picker); otherwise it always applies.
  needsChange?: boolean;
  onPick: (p: Pick) => void;
}) {
  const { agents, loading, listedAt, onRefresh } = source;
  const startKind = kind ?? kinds[0] ?? "";
  const initial = (k: string) => ({ model: currentModel(running(k).model), effort: (running(k).effort ?? "").toLowerCase() });
  const [k, setK] = useState(startKind);
  const [choice, setChoice] = useState(() => initial(startKind));
  const entry = agents?.find((a) => a.kind === k);
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
  const mark = (kk: string) => (
    <span aria-hidden className="font-mono text-[11px] leading-none" style={{ color: kindColour(kk) }}>
      {kindMark(theme, kk) || "●"}
    </span>
  );

  return (
    <div className="agent-picker flex flex-col gap-1" data-kind={k}>
      {kind === null &&
        (kinds.length === 0 ? (
          <p className="px-2 py-1 text-muted-foreground text-xs">This Deck names no agents.</p>
        ) : (
          <div className={cn(segments, "self-start")} role="group" aria-label="Agent">
            {kinds.map((kk) => (
              <Segment key={kk} checked={kk === k} className="text-xs" onClick={() => pickKind(kk)}>
                {mark(kk)}
                {kk}
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
                title={m.id === "" ? `The model ${k} picks itself` : m.id}
                onClick={() => pickModel(m.id)}
              />
            ))}
          </div>
          {agents === null && loading && (
            <p className="flex items-center gap-2 px-2 text-muted-foreground text-xs">
              <Loader2 className="size-3 animate-spin" /> Listing models…
            </p>
          )}
          {entry?.failed === true && <p className="px-2 text-muted-foreground text-xs">Deck could not list {k}'s models.</p>}
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
            onClick={() => onPick({ kind: k, model: model?.id ?? "", effort: efforts.includes(choice.effort) ? choice.effort : "", changed })}
          >
            {action(k)}
          </MenuItem>
        )}
      </div>
    </div>
  );
}
