import { Search } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { cn } from "../lib/cn";
import { useFocusTrap } from "./kit/focus";

export interface Command {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
}

// Ctrl/Cmd+K: type to filter, arrows to move, Enter to run, Escape to close.
export function Palette({ commands, onClose }: { commands: Command[]; onClose: () => void }) {
  const [q, setQ] = useState("");
  const [pick, setPick] = useState(0);
  const box = useRef<HTMLDivElement>(null);
  useFocusTrap(box);
  const shown = useMemo(() => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    return commands.filter((c) => words.every((w) => c.label.toLowerCase().includes(w))).slice(0, 12);
  }, [q, commands]);
  const run = (c: Command | undefined) => {
    if (c === undefined) return;
    onClose();
    c.run();
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
            placeholder="Search sessions and commands…"
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
          {shown.map((c, i) => (
            <li
              key={c.id}
              role="option"
              aria-selected={i === pick}
              className={cn(
                "flex cursor-pointer items-center justify-between gap-3 rounded-lg px-3 py-2 text-sm",
                i === pick && "on bg-selected",
              )}
              onMouseEnter={() => setPick(i)}
              onClick={() => run(c)}
            >
              <span className="truncate">{c.label}</span>
              {c.hint !== undefined && <span className="shrink-0 font-mono text-2xs text-faint">{c.hint}</span>}
            </li>
          ))}
          {shown.length === 0 && <li className="px-3 py-6 text-center text-muted-foreground text-sm">Nothing matches.</li>}
        </ul>
      </div>
    </div>
  );
}
