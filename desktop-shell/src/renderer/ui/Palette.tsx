import { useMemo, useState } from "react";

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
    <div className="overlay" role="presentation" onMouseDown={onClose}>
      <div className="palette" role="dialog" aria-label="Commands" onMouseDown={(e) => e.stopPropagation()}>
        <input
          aria-label="Search commands and sessions"
          placeholder="Switch session, new session…"
          value={q}
          autoFocus
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
        <ul role="listbox">
          {shown.map((c, i) => (
            <li
              key={c.id}
              role="option"
              aria-selected={i === pick}
              className={i === pick ? "on" : ""}
              onMouseEnter={() => setPick(i)}
              onClick={() => run(c)}
            >
              <span>{c.label}</span>
              {c.hint !== undefined && <span className="muted">{c.hint}</span>}
            </li>
          ))}
          {shown.length === 0 && <li className="muted">Nothing matches.</li>}
        </ul>
      </div>
    </div>
  );
}
