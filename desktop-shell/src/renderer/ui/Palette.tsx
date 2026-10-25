import { Search } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { cn } from "../lib/cn";
import { type Command, type PaletteItem, type PaletteRoom, searchPalette } from "../palette";
import { useFocusTrap } from "./kit/focus";

export type { Command } from "../palette";

// Ctrl/Cmd+K: type to filter, arrows to move, Enter to run, Escape to close.
// It finds rooms of every open project by title, branch and the text of
// loaded messages; "> " lists the actions alone. rooms is read once, as the
// palette opens.
export function Palette({
  commands,
  rooms,
  onOpenRoom,
  onClose,
}: {
  commands: Command[];
  rooms: () => PaletteRoom[];
  onOpenRoom: (project: string, room: string) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const [pick, setPick] = useState(0);
  const [all] = useState(rooms);
  const box = useRef<HTMLDivElement>(null);
  useFocusTrap(box);
  const shown = useMemo(() => searchPalette(q, commands, all), [q, commands, all]);
  const run = (item: PaletteItem | undefined) => {
    if (item === undefined) return;
    onClose();
    if (item.type === "action") item.c.run();
    else onOpenRoom(item.room.project, item.room.id);
  };
  return (
    <div
      className="overlay fixed inset-0 z-50 flex justify-center bg-black/40 pt-[14vh] backdrop-blur-[2px]"
      role="presentation"
      onMouseDown={onClose}
    >
      <div
        ref={box}
        className="palette flex h-fit max-h-[60vh] w-[560px] max-w-[calc(100vw-2rem)] animate-enter flex-col overflow-hidden rounded-2xl border border-hairline-strong bg-surface-2 shadow-popover inset-shadow-hl"
        role="dialog"
        aria-modal="true"
        aria-label="Commands"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2.5 border-hairline border-b px-4">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            aria-label="Search commands and sessions"
            placeholder="Search threads and messages, or > for commands…"
            value={q}
            autoFocus
            className="h-12 w-full bg-transparent text-[14px] outline-none placeholder:text-faint"
            onChange={(e) => {
              setQ(e.target.value);
              setPick(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") onClose();
              else if (e.key === "ArrowDown") setPick((p) => Math.min(p + 1, shown.length - 1));
              else if (e.key === "ArrowUp") setPick((p) => Math.max(p - 1, 0));
              else if (e.key === "Enter") run(shown[pick]);
              else return;
              e.preventDefault();
            }}
          />
        </div>
        <ul role="listbox" className="overflow-y-auto p-1.5">
          {shown.map((item, i) => (
            <li
              key={item.type === "action" ? item.c.id : `${item.room.project}/${item.room.id}`}
              role="option"
              aria-selected={i === pick}
              data-kind={item.type}
              className={cn(
                "flex cursor-pointer items-center justify-between gap-3 rounded-lg px-3 py-2 text-sm",
                i === pick && "on bg-selected",
              )}
              onMouseEnter={() => setPick(i)}
              onClick={() => run(item)}
            >
              {item.type === "action" ? (
                <>
                  <span className="truncate">{item.c.label}</span>
                  {item.c.hint !== undefined && <span className="shrink-0 font-mono text-2xs text-faint">{item.c.hint}</span>}
                </>
              ) : (
                <>
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate">{item.room.title || "Untitled"}</span>
                    {item.line !== null && <span className="palette-line truncate text-[11px] text-faint">{item.line}</span>}
                  </span>
                  <span className="shrink-0 text-2xs text-faint">{item.room.name}</span>
                </>
              )}
            </li>
          ))}
          {shown.length === 0 && <li className="px-3 py-6 text-center text-muted-foreground text-sm">Nothing matches.</li>}
        </ul>
      </div>
    </div>
  );
}
