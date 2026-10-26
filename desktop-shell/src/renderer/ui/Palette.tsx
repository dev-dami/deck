import { Search } from "./icons";
import { useEffect, useMemo, useRef, useState } from "react";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import { type Command, focusMessage, markMatch, plainSnippet, type MessageHit, type PaletteItem, type PaletteRoom, searchPalette, TEXT_MIN } from "../palette";
import { useFocusTrap } from "./kit/focus";

export type { Command } from "../palette";

// A Deck the palette asks for messages (SearchMessages): an open project.
export interface SearchSource {
  key: string;
  c: DeckClient;
}

// The pause after a keystroke before the Decks are asked.
const SEARCH_MS = 150;


// Ctrl/Cmd+K: type to filter, arrows to move, Enter to run, Escape to close.
// It finds rooms of every open project by title, branch and the text of
// loaded messages, and from three letters on the messages each Deck finds
// in rooms not loaded too; "> " lists the actions alone. rooms is read
// once, as the palette opens.
export function Palette({
  commands,
  rooms,
  sources,
  onOpenRoom,
  onClose,
}: {
  commands: Command[];
  rooms: () => PaletteRoom[];
  sources: SearchSource[];
  onOpenRoom: (project: string, room: string) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const [pick, setPick] = useState(0);
  const [all] = useState(rooms);
  const [hits, setHits] = useState<MessageHit[]>([]);
  const box = useRef<HTMLDivElement>(null);
  const from = useRef(sources);
  from.current = sources;
  const asked = useRef(0);
  useFocusTrap(box);
  // Each Deck is asked after a pause; the latest question wins.
  useEffect(() => {
    const text = q.trim();
    const n = ++asked.current;
    if (text.startsWith(">") || text.length < TEXT_MIN) {
      setHits([]);
      return;
    }
    const t = setTimeout(() => {
      void Promise.all(
        from.current.map(async (p) =>
          ((await p.c.searchMessages?.(text)) ?? []).flatMap((h): MessageHit[] => {
            const room = all.find((r) => r.project === p.key && r.id === h.session_id);
            return room === undefined ? [] : [{ room, message: h.message_id, snippet: plainSnippet(h.snippet), at: h.at }];
          }),
        ),
      ).then((lists) => {
        if (n === asked.current) setHits(lists.flat().sort((a, b) => b.at - a.at));
      });
    }, SEARCH_MS);
    return () => clearTimeout(t);
  }, [q, all]);
  const shown = useMemo(() => searchPalette(q, commands, all, 12, hits), [q, commands, all, hits]);
  const run = (item: PaletteItem | undefined) => {
    if (item === undefined) return;
    onClose();
    if (item.type === "action") item.c.run();
    else if (item.type === "room") onOpenRoom(item.room.project, item.room.id);
    else {
      focusMessage(item.hit.room.id, item.hit.message);
      onOpenRoom(item.hit.room.project, item.hit.room.id);
    }
  };
  const keyOf = (item: PaletteItem) =>
    item.type === "action" ? item.c.id : item.type === "room" ? `${item.room.project}/${item.room.id}` : `${item.hit.room.project}/${item.hit.message}`;
  return (
    <div className="overlay scrim fixed inset-0 z-50 flex justify-center px-4 pt-[16vh]" role="presentation" onMouseDown={onClose}>
      {/* Spotlight: a sheet of thick glass, a big field, and rows whose
          chosen one is a capsule of the accent. */}
      <div
        ref={box}
        className="palette material flex h-fit max-h-[min(60vh,32rem)] w-[640px] max-w-full animate-enter flex-col overflow-hidden rounded-[22px]"
        role="dialog"
        aria-modal="true"
        aria-label="Commands"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 px-5">
          <Search weight="bold" className="size-5 shrink-0 text-muted-foreground" />
          <input
            aria-label="Search commands and sessions"
            placeholder="Search threads and messages, or > for commands…"
            value={q}
            autoFocus
            className="h-14 w-full bg-transparent text-[17px] text-foreground tracking-tight outline-none placeholder:text-faint"
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
        <ul role="listbox" className="overflow-y-auto border-hairline border-t p-2 empty:hidden">
          {shown.map((item, i) => {
            return (
              <li
                key={keyOf(item)}
                role="option"
                aria-selected={i === pick}
                data-kind={item.type}
                className={cn("flex min-h-9 cursor-pointer items-center gap-3 px-3 py-1.5 text-sm", i === pick && "on")}
                onMouseEnter={() => setPick(i)}
                onClick={() => run(item)}
              >
                {item.type === "action" ? (
                  <>
                    <span className="min-w-0 flex-1 truncate">{item.c.label}</span>
                    {item.c.hint !== undefined && <span className="shrink-0 text-2xs text-faint tabular-nums">{item.c.hint}</span>}
                  </>
                ) : (
                  <>
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate">{(item.type === "room" ? item.room : item.hit.room).title || "Untitled"}</span>
                      {item.type === "room" && item.line !== null && <span className="palette-line truncate text-2xs text-faint">{item.line}</span>}
                      {item.type === "hit" && <Snippet text={item.hit.snippet} query={q} />}
                    </span>
                    <span className="shrink-0 text-2xs text-faint">{(item.type === "room" ? item.room : item.hit.room).name}</span>
                  </>
                )}
              </li>
            );
          })}
        </ul>
        {shown.length === 0 && <p className="border-hairline border-t px-5 py-6 text-center text-muted-foreground text-sm">Nothing matches.</p>}
      </div>
    </div>
  );
}

// The text around a found message's match, the match in the text colour.
function Snippet({ text, query }: { text: string; query: string }) {
  const parts = markMatch(text, query);
  return (
    <span className="palette-line palette-snippet truncate text-[11px] text-faint">
      {parts === null ? (
        text
      ) : (
        <>
          {parts[0]}
          <mark className="bg-transparent text-foreground">{parts[1]}</mark>
          {parts[2]}
        </>
      )}
    </span>
  );
}
