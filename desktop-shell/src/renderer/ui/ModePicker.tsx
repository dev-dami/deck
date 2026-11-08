import { availableModes, needsConfirm } from "@web/modes";
import type { Mode } from "@web/protocol";
import { ChevronDown, Flame, ListChecks, MessageCircleQuestion } from "./icons";
import { useState } from "react";
import { cn } from "../lib/cn";
import { PickRow } from "./AgentPicker";
import { MenuLabel, Popover } from "./kit/menu";

const ICONS = { plan: ListChecks, ask: MessageCircleQuestion, yolo: Flame } as const;

// Plan, Ask or Yolo for the open session, as a menu of the same rows and
// keys as the agent picker (AgentPicker.tsx). Yolo asks first.
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
      className="w-72"
      trigger={
        <button
          type="button"
          className={cn(
            "mode-picker flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full pr-2 pl-2.5 font-medium text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected aria-expanded:text-foreground",
            mode === "yolo" && "text-destructive-foreground",
          )}
          aria-expanded={open}
          data-mode={mode ?? undefined}
          title={current?.blurb ?? "Mode"}
          onClick={() => setOpen((o) => !o)}
        >
          <Icon className="size-3.5" />
          {current?.label ?? "Mode"}
          <ChevronDown className="size-3 text-faint" />
        </button>
      }
    >
      <MenuLabel>Mode</MenuLabel>
      <div className="modes flex flex-col" role="group" aria-label="Mode">
        {modes.map((m) => {
          const MIcon = ICONS[m.mode];
          return (
            <PickRow
              key={m.mode}
              className={`mode mode-${m.mode} py-2`}
              checked={mode === m.mode}
              lead={<MIcon className={cn("size-3.5", m.mode === "yolo" ? "text-destructive-foreground" : "text-muted-foreground")} />}
              label={m.label}
              detail={m.blurb}
              onClick={() => {
                setOpen(false);
                if (mode === m.mode) return;
                if (confirmYolo && needsConfirm(m.mode) && !window.confirm(`${m.blurb} Switch to ${m.label}?`)) return;
                onSet(m.mode);
              }}
            />
          );
        })}
      </div>
    </Popover>
  );
}
