import { useState } from "react";
import { refreshCrew, useRoomCrew } from "../crewState";
import { cn } from "../lib/cn";
import { LEVELS, type Level, levelInfo } from "../orchestration";
import type { ProjectApi } from "../project";
import { PickRow } from "./AgentPicker";
import { ChevronDown, Handshake, Network, User, Users } from "./icons";
import { MenuLabel, Popover } from "./kit/menu";

const ICONS = { off: User, assist: Handshake, auto: Users, full: Network } as const;

// The thread's orchestration level (orchestration.ts), as a menu of the
// same rows as the mode picker (ModePicker.tsx): each level with what it
// lets the agents do. null while the board has not come yet.
export function LevelPicker({
  level,
  onSet,
  side = "top",
  align = "start",
}: {
  level: Level | null;
  onSet: (l: Level) => void;
  side?: "top" | "bottom";
  align?: "start" | "end";
}) {
  const [open, setOpen] = useState(false);
  const current = level === null ? null : levelInfo(level);
  const Icon = level === null ? Users : ICONS[level];
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side={side}
      align={align}
      label="Orchestration"
      className="w-80"
      trigger={
        <button
          type="button"
          className="level-picker flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full pr-2 pl-2.5 font-medium text-muted-foreground text-xs outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected aria-expanded:text-foreground"
          aria-expanded={open}
          aria-label={`Orchestration: ${current?.label ?? "loading"}`}
          data-level={level ?? undefined}
          title={current === null ? "Orchestration" : `Orchestration ${current.label}: ${current.blurb}`}
          disabled={level === null}
          onClick={() => setOpen((o) => !o)}
        >
          <Icon className="size-3.5" />
          {current?.label ?? "Orchestration"}
          <ChevronDown className="size-3 text-faint" />
        </button>
      }
    >
      <MenuLabel>Orchestration</MenuLabel>
      <div className="levels flex flex-col" role="group" aria-label="Orchestration">
        {LEVELS.map((l) => {
          const LIcon = ICONS[l.level];
          return (
            <PickRow
              key={l.level}
              className={cn("level py-2", `level-${l.level}`)}
              checked={level === l.level}
              lead={<LIcon className="size-3.5 text-muted-foreground" />}
              label={l.label}
              detail={l.blurb}
              onClick={() => {
                setOpen(false);
                if (level !== l.level) onSet(l.level);
              }}
            />
          );
        })}
      </div>
    </Popover>
  );
}

// The level picker of one room of a local project: the level its board
// says (crewState.ts), set through the Deck.
export function RoomLevel({
  api,
  sessionId,
  onError,
  side,
  align,
}: {
  api: ProjectApi;
  sessionId: string;
  onError: (text: string) => void;
  side?: "top" | "bottom";
  align?: "start" | "end";
}) {
  const level = useRoomCrew(api, sessionId)?.board?.level ?? null;
  const set = async (l: Level) => {
    const err = await api.setLevel(sessionId, l);
    if (err !== null) onError(err);
    await refreshCrew(api, sessionId);
  };
  return <LevelPicker level={level} onSet={(l) => void set(l)} {...(side === undefined ? {} : { side })} {...(align === undefined ? {} : { align })} />;
}
