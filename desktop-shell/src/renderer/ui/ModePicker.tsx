import { availableModes, needsConfirm } from "@web/modes";
import type { Mode } from "@web/protocol";
import { Check, ChevronDown, Flame, ListChecks, MessageCircleQuestion } from "lucide-react";
import { useState } from "react";
import { cn } from "../lib/cn";
import { MenuLabel, Popover } from "./kit/menu";

const ICONS = { plan: ListChecks, ask: MessageCircleQuestion, yolo: Flame } as const;

// Plan, Ask or Yolo for the open session. Yolo asks first.
export function ModePicker({
  mode,
  allowYolo,
  confirmYolo,
  onSet,
}: {
  mode: Mode | null;
  allowYolo: boolean;
  confirmYolo: boolean;
  onSet: (m: Mode) => void;
}) {
  const [open, setOpen] = useState(false);
  const modes = availableModes(allowYolo);
  const current = modes.find((m) => m.mode === mode);
  const Icon = mode === null ? MessageCircleQuestion : ICONS[mode];
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      label="Mode"
      role="dialog"
      className="w-72"
      trigger={
        <button
          type="button"
          className={cn(
            "mode-picker flex h-6 cursor-pointer items-center gap-1.5 rounded-md px-1.5 text-muted-foreground/80 text-xs hover:bg-accent hover:text-foreground aria-expanded:bg-accent",
            mode === "yolo" && "text-destructive-foreground",
          )}
          aria-expanded={open}
          title={current?.blurb ?? "Mode"}
          onClick={() => setOpen((o) => !o)}
        >
          <Icon className="size-3.5" />
          {current?.label ?? "Mode"}
          <ChevronDown className="size-3" />
        </button>
      }
    >
      <MenuLabel>Mode</MenuLabel>
      <div className="modes flex flex-col" role="radiogroup" aria-label="Mode">
        {modes.map((m) => {
          const MIcon = ICONS[m.mode];
          return (
            <button
              key={m.mode}
              type="button"
              role="radio"
              aria-checked={mode === m.mode}
              className={`mode mode-${m.mode} flex w-full cursor-pointer items-start gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-accent`}
              onClick={() => {
                setOpen(false);
                if (mode === m.mode) return;
                if (confirmYolo && needsConfirm(m.mode) && !window.confirm(`${m.blurb} Switch to ${m.label}?`)) return;
                onSet(m.mode);
              }}
            >
              <MIcon
                className={cn("mt-0.5 size-3.5 shrink-0", m.mode === "yolo" ? "text-destructive-foreground" : "text-muted-foreground")}
              />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="font-medium text-[13px]">{m.label}</span>
                <span className="text-muted-foreground text-xs leading-snug">{m.blurb}</span>
              </span>
              {mode === m.mode && <Check className="mt-0.5 size-3.5 shrink-0" />}
            </button>
          );
        })}
      </div>
    </Popover>
  );
}
