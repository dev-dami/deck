import type { Theme } from "@web/protocol";
import { useState } from "react";
import { createPortal } from "react-dom";
import type { FileAsk, FileEdit, FileTrack } from "../../fileWork";
import { cn } from "../../lib/cn";
import { DiffView } from "../DiffView";
import { FileDiff, X } from "../icons";
import { Button } from "../kit/button";
import { Dialog } from "../kit/dialog";
import { Glyph, hueOf } from "./Nodes";

// The card of a file picked on the graph, beside the agent's (Detail.tsx):
// who has it now and who waits on them, then its timeline, the edits and
// the asks between agents, oldest first. An edit opens its diff; an ask
// jumps to its line in the conversation.

const clock = (ms: number) => (ms > 0 ? new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) : "");

type Item = { k: "edit"; e: FileEdit } | { k: "ask"; a: FileAsk };

// What an ask came to, in a few words.
export function askOutcome(a: FileAsk): string {
  if (a.state === "pending") return "waiting";
  if (a.state === "held") return `${a.by ?? a.to}: wait, ${a.answer}`;
  return a.by === null ? a.answer : `${a.by}: ${a.answer}`;
}

export function FileDetail({
  path,
  work,
  changed,
  theme,
  kindOf,
  compact,
  onOpenMessage,
  onOpenFile,
  onClose,
}: {
  path: string;
  work: FileTrack;
  changed: boolean;
  theme: Theme | null;
  kindOf: (agent: string) => string;
  compact: boolean;
  onOpenMessage: ((mid: string) => void) | undefined;
  onOpenFile: (path: string) => void;
  onClose: () => void;
}) {
  const [diff, setDiff] = useState<FileEdit | null>(null);
  const slash = path.lastIndexOf("/");
  const items: Item[] = [...work.edits.map((e): Item => ({ k: "edit", e })), ...work.asks.map((a): Item => ({ k: "ask", a }))].sort(
    (x, y) => (x.k === "edit" ? x.e.at : x.a.at) - (y.k === "edit" ? y.e.at : y.a.at),
  );
  const waiting = work.asks.filter((a) => a.state === "pending");
  const Who = ({ name }: { name: string }) => (
    <span className="inline-flex shrink-0 items-center gap-1" style={hueOf(name, kindOf(name))}>
      <Glyph kind={kindOf(name)} theme={theme} className="w-auto text-2xs" />
      <span className="font-medium text-(--hue)">{name}</span>
    </span>
  );
  return (
    <section
      data-graph-control=""
      aria-label={`${path} timeline`}
      className={cn("graph-detail material absolute z-20 flex animate-enter flex-col rounded-[14px]", compact ? "inset-x-2 bottom-2 max-h-[60%]" : "top-13 right-3 max-h-[70%] w-[312px]")}
    >
      <header className="flex items-start gap-2 px-3.5 pt-3 pb-2.5">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h3 className="truncate font-medium font-mono text-sm leading-tight">{path.slice(slash + 1)}</h3>
          {slash > 0 && <p className="truncate font-mono text-2xs text-faint">{path.slice(0, slash)}</p>}
        </div>
        <Button variant="ghost-muted" size="icon-xs" className="-mt-0.5 -mr-1.5" aria-label="Close" onClick={onClose}>
          <X />
        </Button>
      </header>
      <dl className="flex flex-col gap-1.5 border-hairline border-t px-3.5 py-2.5 text-xs">
        <div className="flex min-w-0 items-baseline gap-3">
          <dt className="w-14 shrink-0 text-2xs text-faint">Owner</dt>
          <dd className="flex min-w-0 flex-1 items-baseline gap-1.5" data-owner={work.owner ?? ""}>
            {work.owner === null ? <span className="text-muted-foreground">nobody</span> : <Who name={work.owner} />}
            {work.owner !== null && work.holders.includes(work.owner) && <span className="text-2xs text-faint">holds a lease</span>}
          </dd>
        </div>
        {waiting.map((a) => (
          <div key={a.mid} className="flex min-w-0 items-baseline gap-3">
            <dt className="w-14 shrink-0 text-2xs text-faint">Waiting</dt>
            <dd className="flex min-w-0 flex-1 items-baseline gap-1.5 text-warning-foreground">
              <Who name={a.from} /> asks {a.to}
            </dd>
          </div>
        ))}
      </dl>
      <ol aria-label={`Edits and asks of ${path}`} className="flex min-h-0 flex-col gap-0.5 overflow-y-auto border-hairline border-t px-1.5 py-1.5">
        {items.map((it) =>
          it.k === "edit" ? (
            <li key={`e:${it.e.mid}:${it.e.at}:${it.e.tool}`}>
              <button
                type="button"
                data-edit={it.e.agent}
                disabled={it.e.diff === ""}
                className="flex w-full min-w-0 cursor-pointer items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs outline-none hover:bg-hover focus-visible:ring-1 focus-visible:ring-primary disabled:cursor-default disabled:hover:bg-transparent"
                onClick={() => setDiff(it.e)}
              >
                <Who name={it.e.agent} />
                <span className="text-muted-foreground">edited</span>
                {it.e.diff !== "" && (
                  <span className="font-mono text-3xs tabular-nums">
                    <span className="text-diff-addition-foreground">+{it.e.added}</span> <span className="text-diff-deletion-foreground">−{it.e.removed}</span>
                  </span>
                )}
                <time className="ml-auto shrink-0 text-2xs text-faint tabular-nums">{clock(it.e.at)}</time>
              </button>
            </li>
          ) : (
            <li key={`a:${it.a.mid}`}>
              <button
                type="button"
                data-ask={it.a.state}
                disabled={onOpenMessage === undefined}
                className="flex w-full min-w-0 cursor-pointer flex-col gap-0.5 rounded-md px-2 py-1 text-left text-xs outline-none hover:bg-hover focus-visible:ring-1 focus-visible:ring-primary disabled:cursor-default"
                onClick={() => onOpenMessage?.(it.a.mid)}
              >
                <span className="flex min-w-0 items-center gap-1.5">
                  <Who name={it.a.from} />
                  <span className="text-muted-foreground">asked</span>
                  <Who name={it.a.to} />
                  <time className="ml-auto shrink-0 text-2xs text-faint tabular-nums">{clock(it.a.at)}</time>
                </span>
                <span className={cn("truncate pl-4 text-2xs", it.a.state === "pending" ? "text-warning-foreground" : "text-muted-foreground")}>
                  → {askOutcome(it.a)}
                </span>
              </button>
            </li>
          ),
        )}
      </ol>
      {changed && (
        <footer className="flex items-center border-hairline border-t px-1.5 py-1.5">
          <Button variant="ghost" size="xs" onClick={() => onOpenFile(path)}>
            <FileDiff />
            Open in Changes
          </Button>
        </footer>
      )}
      {diff !== null && <EditDiff edit={diff} path={path} onClose={() => setDiff(null)} />}
    </section>
  );
}

// One edit's diff, over the window: in the body, out of the graph's panels,
// whose transforms would hold a fixed dialog inside them.
export function EditDiff({ edit, path, onClose }: { edit: { agent: string; diff: string; at: number }; path: string; onClose: () => void }) {
  return createPortal(
    <Dialog title={`${edit.agent} edited ${path.slice(path.lastIndexOf("/") + 1)}`} description={`${path}${edit.at > 0 ? ` · ${clock(edit.at)}` : ""}`} onClose={onClose} className="w-[min(90vw,760px)]">
      <div className="max-h-[60vh] overflow-auto rounded-md border border-hairline font-mono text-code">
        <DiffView diff={edit.diff} />
      </div>
    </Dialog>,
    document.body,
  );
}
