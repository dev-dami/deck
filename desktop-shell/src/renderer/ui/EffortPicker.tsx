import { useState } from "react";
import { effortGlyph, effortName } from "../effort";
import { cn } from "../lib/cn";
import { EffortGlyph } from "./EffortGlyph";
import { Check } from "./icons";
import { Popover } from "./kit/menu";

// The lead's reasoning effort in the composer's bar, as T3 Code shows it:
// no word, only the effort glyph (rising bars, filled to the level), right
// after the agent's chip. A click opens the levels as a short column: the
// agent's default, then each level the model takes, lightest first, each
// with its glyph; a pick applies at once. The level's name is for the
// tooltip and assistive tech (design-glass.md, section 12).
export function EffortPicker({
  levels,
  effort,
  model,
  onPick,
}: {
  // The levels the model takes, lightest first (effort.ts effortLevels).
  levels: readonly string[];
  // The level it runs at; "" for the agent's own.
  effort: string;
  // The model's name, for the column's header.
  model: string;
  onPick: (effort: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const g = effortGlyph(levels, effort);
  const name = effort === "" ? "the agent's default" : effortName(effort);
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      label="Reasoning effort"
      className="effort-menu w-52 p-1"
      trigger={
        <button
          type="button"
          className="effort-picker -ml-1 grid h-7 shrink-0 cursor-pointer place-items-center rounded-full px-2 text-muted-foreground outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected aria-expanded:text-foreground"
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={`Reasoning effort: ${name}. Change it`}
          title={`Reasoning effort: ${effort === "" ? "Default" : effortName(effort)}`}
          data-effort={effort}
          onClick={() => setOpen((o) => !o)}
        >
          <EffortGlyph count={g.count} filled={g.filled} auto={g.auto} />
          <span className="sr-only">{effortName(effort)}</span>
        </button>
      }
    >
      <div className="flex items-baseline gap-2 border-hairline border-b px-2 pt-1 pb-1.5">
        <span className="text-muted-foreground text-xs">Effort</span>
        {model !== "" && <span className="ml-auto min-w-0 truncate text-2xs text-faint">{model}</span>}
      </div>
      <div className="effort-levels flex flex-col gap-px pt-1" role="group" aria-label="Reasoning effort">
        {["", ...levels].map((l) => {
          const lg = effortGlyph(levels, l);
          const on = l === effort;
          return (
            <button
              key={l}
              type="button"
              role="menuitemradio"
              aria-checked={on}
              className={cn(
                "effort-level flex h-7 w-full cursor-pointer items-center gap-2.5 rounded-[7px] px-2 text-left text-[13px] outline-none transition-colors hover:bg-hover focus-visible:bg-selected",
                on && "bg-selected hover:bg-selected",
              )}
              data-level={l}
              onClick={() => {
                setOpen(false);
                if (!on) onPick(l);
              }}
            >
              <span className="flex w-[26px] shrink-0 justify-start text-muted-foreground">
                <EffortGlyph count={lg.count} filled={lg.filled} auto={lg.auto} height={12} />
              </span>
              <span className="min-w-0 flex-1 truncate">{effortName(l)}</span>
              {on && <Check className="size-3.5 shrink-0 text-foreground" weight="bold" />}
            </button>
          );
        })}
      </div>
    </Popover>
  );
}
