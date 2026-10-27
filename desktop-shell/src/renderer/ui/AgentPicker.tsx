import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { type ComponentProps, type ReactNode, useState } from "react";
import { type AgentModels, currentModel, DEFAULT_LABEL, modelRows } from "../agentCatalog";
import { cn } from "../lib/cn";
import { Check, Loader2, RefreshCw } from "./icons";
import { MenuItem, MenuLabel, MenuSeparator } from "./kit/menu";

// One choice in a picker: a radio item of the menu it sits in, so the
// arrows, Home and End move through every section alike, and Enter or Space
// picks. The mode picker and the agent picker share it.
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

// A row of small radio items: the efforts of one model.
function Chip({ checked, children, ...props }: Omit<ComponentProps<"button">, "role"> & { checked: boolean }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={checked}
      className={cn(
        "effort shrink-0 cursor-pointer rounded-full px-2 py-0.5 text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
        checked && "bg-selected text-foreground hover:bg-selected",
      )}
      {...props}
    >
      {children}
    </button>
  );
}

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

// The one agent picker, for every agent alike: the agent (when the picker
// starts one; a lane's picker is for its own), its model, then the model's
// reasoning efforts, each section shown only when it has a choice. The
// radios only choose; the action item applies the choice. Under it, Refresh
// lists every agent's models again. The lists come from the catalog
// (agentCatalog.ts), already loaded: opening a picker asks nothing.
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
    <div className="agent-picker flex flex-col" data-kind={k}>
      {kind === null && (
        <>
          <MenuLabel>Agent</MenuLabel>
          <div className="flex flex-col" role="group" aria-label="Agent">
            {kinds.map((kk) => (
              <PickRow key={kk} checked={kk === k} lead={mark(kk)} label={kk} onClick={() => pickKind(kk)} />
            ))}
            {kinds.length === 0 && <p className="px-2 py-1.5 text-muted-foreground text-xs">This Deck names no agents.</p>}
          </div>
        </>
      )}
      {k !== "" && (
        <>
          <MenuLabel>Model</MenuLabel>
          <div className="flex max-h-56 flex-col overflow-y-auto" role="group" aria-label="Model">
            {rows.map((m) => (
              <PickRow
                key={m.id}
                checked={m.id === model?.id}
                label={m.label}
                detail={m.id === "" ? `The model ${k} picks itself` : m.label.toLowerCase() !== m.id.toLowerCase() ? m.id : undefined}
                onClick={() => pickModel(m.id)}
              />
            ))}
          </div>
          {agents === null && loading && (
            <p className="flex items-center gap-2 px-2 py-1.5 text-muted-foreground text-xs">
              <Loader2 className="size-3 animate-spin" /> Listing models…
            </p>
          )}
          {entry?.failed === true && <p className="px-2 py-1.5 text-muted-foreground text-xs">Deck could not list {k}'s models.</p>}
          {efforts.length > 0 && (
            <>
              <MenuLabel>Reasoning effort</MenuLabel>
              <div className="flex flex-wrap gap-1 px-1.5 pb-1" role="group" aria-label="Reasoning effort">
                <Chip checked={choice.effort === ""} onClick={() => setChoice({ ...choice, effort: "" })}>
                  {DEFAULT_LABEL}
                </Chip>
                {efforts.map((e) => (
                  <Chip key={e} checked={choice.effort === e} onClick={() => setChoice({ ...choice, effort: e })}>
                    {e}
                  </Chip>
                ))}
              </div>
            </>
          )}
          <MenuSeparator />
          <MenuItem
            className="pick-apply font-medium"
            disabled={needsChange && !changed}
            onClick={() => onPick({ kind: k, model: model?.id ?? "", effort: efforts.includes(choice.effort) ? choice.effort : "", changed })}
          >
            {action(k)}
          </MenuItem>
        </>
      )}
      <MenuItem className="text-muted-foreground" aria-label="Refresh agents" disabled={loading} onClick={onRefresh}>
        {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
        <span className="flex-1">Refresh agents</span>
        {listedAt !== null && !loading && <span className="text-2xs text-faint">{ago(listedAt, Date.now())}</span>}
      </MenuItem>
    </div>
  );
}
