import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { useState } from "react";
import type { ProfileRef } from "../completion";
import { cn } from "../lib/cn";
import {
  addEntrant,
  canAdd,
  type Entrant,
  entrantDetail,
  newRace,
  nextEntrant,
  RACE_MAX,
  RACE_MIN,
  type RaceDraft,
  raceProblem,
  raceSummary,
  removeEntrant,
  replaceEntrant,
} from "../race";
import { AgentPicker, type Pick, type PickerSource } from "./AgentPicker";
import { Flag, Plus, X } from "./icons";
import { Button } from "./kit/button";
import { fieldClass } from "./kit/dialog";
import { Popover } from "./kit/menu";
import { Switch } from "./kit/switch";

// The race builder: what /race opens over the composer, and the Tasks tab's
// race form. The task, then the entrants as compact chips (each its agent,
// model and effort; a click edits it, × takes it out), Add agent, and
// Start. Each entrant is picked with the one agent picker, so a race mixes
// kinds (codex, claude, pi…), named agents, and the same kind twice on the
// same model or on two (race.ts).

// Wide and short, as the lanes' picker.
const POPOVER = "w-[26rem] max-w-[calc(100vw-2rem)]";

const entrantOf = (p: Pick, named: readonly ProfileRef[]): Entrant => ({
  agent: p.agent,
  kind: p.kind,
  model: p.model,
  effort: p.effort,
  profile: named.some((n) => n.name === p.agent),
});

function EntrantChip({
  e,
  index,
  colour,
  detail,
  theme,
  side,
  picker,
  kinds,
  named,
  onChange,
  onRemove,
}: {
  e: Entrant;
  index: number;
  colour: string;
  detail: string;
  theme: Theme | null;
  side: "top" | "bottom";
  picker: PickerSource;
  kinds: readonly string[];
  named: readonly ProfileRef[];
  onChange: (e: Entrant) => void;
  onRemove: () => void;
}) {
  const [open, setOpen] = useState(false);
  const name = e.profile ? `@${e.agent}` : e.agent;
  return (
    <li className="race-entrant flex h-7 items-center rounded-full border border-hairline bg-hover/60 pr-0.5 text-xs" data-agent={e.agent}>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        side={side}
        label={`Entrant ${index + 1}`}
        className={POPOVER}
        trigger={
          <button
            type="button"
            className="flex h-7 min-w-0 max-w-56 cursor-pointer items-center gap-1.5 rounded-full pr-1 pl-2 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={`Entrant ${index + 1}: ${name}, ${detail}. Change it`}
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
          >
            <span aria-hidden className="font-mono text-[11px] leading-none" style={{ color: colour }}>
              {kindMark(theme, e.kind) || "●"}
            </span>
            <span className="truncate font-medium font-mono text-foreground">{name}</span>
            <span className="race-entrant-detail truncate text-muted-foreground">{detail}</span>
          </button>
        }
      >
        <AgentPicker
          source={picker}
          kinds={kinds}
          named={named}
          kind={null}
          start={e.agent}
          running={(a) => (a === e.agent ? { model: e.model, effort: e.effort } : {})}
          theme={theme}
          action={() => "Update"}
          onPick={(p) => {
            setOpen(false);
            onChange(entrantOf(p, named));
          }}
        />
      </Popover>
      <button
        type="button"
        className="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full text-faint outline-none hover:bg-selected hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Remove entrant ${index + 1} (${name})`}
        onClick={onRemove}
      >
        <X className="size-2.5" weight="bold" />
      </button>
    </li>
  );
}

export function RaceBuilder({
  brief = "",
  kinds,
  named = [],
  picker,
  theme,
  canJudge,
  side = "top",
  className,
  onStart,
  onClose,
}: {
  // The task it opens with: what followed /race.
  brief?: string;
  kinds: readonly string[];
  // The named agents (profiles), offered after the kinds.
  named?: readonly ProfileRef[] | undefined;
  picker: PickerSource;
  theme: Theme | null;
  // The Deck can have one more agent rank the entries.
  canJudge: boolean;
  // Where the pickers open: above, over the composer; below, in a panel.
  side?: "top" | "bottom";
  className?: string;
  // Starts the race; true once Deck took it.
  onStart: (d: RaceDraft) => Promise<boolean>;
  // Without it the builder stays (the Tasks tab) and clears after a start.
  onClose?: () => void;
}) {
  const [d, setD] = useState<RaceDraft>(() => newRace(brief));
  const [adding, setAdding] = useState(false);
  const [starting, setStarting] = useState(false);
  const problem = raceProblem(d);
  const next = nextEntrant(d, kinds);
  const colour = (e: Entrant) => (e.profile ? named.find((n) => n.name === e.agent)?.color : undefined) || kindColour(e.kind);

  const start = async () => {
    if (problem !== null || starting) return;
    setStarting(true);
    const ok = await onStart({ ...d, brief: d.brief.trim(), judge: canJudge && d.judge });
    setStarting(false);
    if (!ok) return;
    if (onClose !== undefined) onClose();
    else setD((cur) => ({ ...cur, brief: "" }));
  };

  return (
    <section className={cn("race-builder flex flex-col gap-2.5 p-3", className)} aria-label="Race builder">
      <div className="flex items-center gap-2">
        <Flag aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
        <h3 className="font-medium text-[13px] text-foreground">Race</h3>
        <span className="race-summary min-w-0 truncate text-faint text-xs">{raceSummary(d)}</span>
        {onClose !== undefined && (
          <button
            type="button"
            className="ml-auto grid size-6 shrink-0 cursor-pointer place-items-center rounded-full text-muted-foreground outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            aria-label="Close the race builder"
            onClick={onClose}
          >
            <X className="size-3" weight="bold" />
          </button>
        )}
      </div>
      <textarea
        aria-label="Race task"
        placeholder="What should they all do?"
        rows={2}
        value={d.brief}
        // A bare /race has no task yet: the builder starts there.
        autoFocus={brief === "" && onClose !== undefined}
        className={cn(fieldClass, "resize-none text-xs")}
        onChange={(e) => setD({ ...d, brief: e.target.value })}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            void start();
          } else if (e.key === "Escape" && onClose !== undefined) {
            e.preventDefault();
            onClose();
          }
        }}
      />
      <ul className="race-entrants flex flex-wrap items-center gap-1.5" aria-label="Entrants">
        {d.entrants.map((e, i) => (
          <EntrantChip
            // An entrant has no id of its own; its place is its name.
            key={i}
            e={e}
            index={i}
            colour={colour(e)}
            detail={entrantDetail(e, picker.agents)}
            theme={theme}
            side={side}
            picker={picker}
            kinds={kinds}
            named={named}
            onChange={(x) => setD(replaceEntrant(d, i, x))}
            onRemove={() => setD(removeEntrant(d, i))}
          />
        ))}
        {next !== null && (
          <li>
            <Popover
              open={adding}
              onClose={() => setAdding(false)}
              side={side}
              label="Add an entrant"
              className={POPOVER}
              trigger={
                <button
                  type="button"
                  className="race-add flex h-7 cursor-pointer items-center gap-1 rounded-full border border-hairline-strong border-dashed px-2.5 text-muted-foreground text-xs outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
                  aria-expanded={adding}
                  disabled={!canAdd(d)}
                  title={canAdd(d) ? "Add an agent: any kind, any model, the same one again" : `A race takes at most ${RACE_MAX} agents`}
                  onClick={() => setAdding((o) => !o)}
                >
                  <Plus className="size-3" weight="bold" />
                  Add agent
                </button>
              }
            >
              <AgentPicker
                // A fresh picker for each add, on the last entrant.
                key={d.entrants.length}
                source={picker}
                kinds={kinds}
                named={named}
                kind={null}
                start={next.agent}
                running={(a) => (a === next.agent ? { model: next.model, effort: next.effort } : {})}
                theme={theme}
                action={(a) => `Add ${a}`}
                onPick={(p) => {
                  setD(addEntrant(d, entrantOf(p, named)));
                  // Stays open while the race has room: three agents is three picks.
                  if (d.entrants.length + 1 >= RACE_MAX) setAdding(false);
                }}
              />
            </Popover>
          </li>
        )}
      </ul>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        {canJudge && (
          <span className="race-judge flex items-center gap-1.5 text-muted-foreground" title="One more agent, of the first entrant's kind, ranks the entries">
            <Switch label="Judge" checked={d.judge} onChange={(judge) => setD({ ...d, judge })} />
            Judge
          </span>
        )}
        <span className="min-w-0 flex-1 truncate text-faint">
          {RACE_MIN} to {RACE_MAX} agents, each on a worktree of its own. You keep one.
        </span>
        {onClose !== undefined && (
          <Button size="xs" variant="ghost-muted" onClick={onClose}>
            Cancel
          </Button>
        )}
        <Button size="xs" className="race-start" disabled={problem !== null || starting} title={problem ?? "Start the race"} onClick={() => void start()}>
          <Flag /> Start race{d.entrants.length > 0 ? ` (${d.entrants.length})` : ""}
        </Button>
      </div>
    </section>
  );
}
