import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { ArrowUp, Loader2, PanelLeft, ShieldQuestion } from "./icons";
import { type CSSProperties, useEffect, useLayoutEffect, useRef, useState } from "react";
import { agentColour, agentKind } from "../agents";
import { mirrorRow, type MirrorRow, type SharedMirror, type TeamThread } from "../crew";
import type { ProjectApi } from "../project";
import { Button } from "./kit/button";
import { Markdown } from "./Markdown";
import { useDockHeight } from "./Room";

// A room a teammate shares with you, in the room view: its lines as
// read-only rows in the timeline's type and spacing, the approvals it waits
// for (an editor answers them) and a composer for a participant or editor.

// The engine holds a read up to 10 seconds for news. A read that comes back
// sooner with nothing new (an older engine) waits this long before the next.
const QUIET_MS = 2000;
const RETRY_MS = 5000;

interface Group {
  who: string;
  rows: MirrorRow[];
}

function group(lines: string[]): Group[] {
  const out: Group[] = [];
  for (const l of lines) {
    const r = mirrorRow(l);
    const last = out.at(-1);
    if (last !== undefined && last.who === r.who) last.rows.push(r);
    else out.push({ who: r.who, rows: [r] });
  }
  return out;
}

// What an agent changed or ran, as the timeline's tool rows: mono, a muted $.
function Note({ text }: { text: string }) {
  return (
    <p className="shared-note flex h-7 min-w-0 items-center gap-2 font-mono text-code text-muted-foreground">
      <span aria-hidden className="shrink-0 text-faint">
        $
      </span>
      <span className="min-w-0 truncate">{text}</span>
    </p>
  );
}

function Rows({ g, theme }: { g: Group; theme: Theme | null }) {
  const kind = agentKind(g.who, []);
  const mark = kindMark(theme, kind);
  if (mark === "") {
    // A person: the bubble, as the timeline shows the person's turn.
    return (
      <article className="msg msg-user shared-row flex flex-col gap-1" data-who={g.who}>
        {g.rows.some((r) => !r.note) && (
          <div className="user-card text-message-foreground">
            {g.rows
              .filter((r) => !r.note)
              .map((r, i) => (
                <p key={i} className="whitespace-pre-wrap text-pretty break-words text-prose">
                  {r.text}
                </p>
              ))}
          </div>
        )}
        {g.rows.map((r, i) => r.note && <Note key={i} text={r.text} />)}
        <header className="msg-head flex items-center gap-1.5 pe-1 text-2xs text-faint">
          <span>{g.who || "note"}</span>
          {g.rows[0]?.to !== "" && <span className="font-mono">→ {g.rows[0]?.to}</span>}
        </header>
      </article>
    );
  }
  const colour = agentColour(g.who, kind);
  return (
    <article className="msg msg-agent shared-row flex min-w-0 flex-col gap-2" data-who={g.who} style={{ "--hue": colour } as CSSProperties}>
      <header className="msg-head flex h-6 items-center gap-2">
        <span className="msg-index inline-flex items-center gap-2">
          <span
            aria-hidden
            className="grid size-[22px] place-items-center rounded-full bg-(--hue)/14 font-mono text-(--hue) text-[10.5px] ring-(--hue)/28 ring-1 ring-inset"
          >
            {mark}
          </span>
          <span className="font-semibold text-(--hue) text-[13px]">{g.who}</span>
        </span>
      </header>
      <div className="flex flex-col gap-2 text-foreground text-prose">
        {g.rows.map((r, i) => (r.note ? <Note key={i} text={r.text} /> : <Markdown key={i} text={r.text} />))}
      </div>
    </article>
  );
}

export function SharedRoom({
  api,
  peer,
  thread,
  theme,
  sidebar,
  onShowSidebar,
  onError,
}: {
  api: ProjectApi;
  peer: string;
  thread: TeamThread;
  theme: Theme | null;
  sidebar: boolean;
  onShowSidebar: () => void;
  onError: (t: string) => void;
}) {
  const [mirror, setMirror] = useState<SharedMirror | null>(null);
  const [lines, setLines] = useState<string[] | null>(null);
  const [fault, setFault] = useState<string | null>(null);
  const [text, setText] = useState("");
  const after = useRef(0);
  const list = useRef<HTMLDivElement>(null);
  const dock = useDockHeight();

  useEffect(() => {
    let live = true;
    after.current = 0;
    setLines(null);
    setMirror(null);
    const pause = (ms: number) => new Promise((r) => setTimeout(r, ms));
    const loop = async () => {
      while (live) {
        const t0 = Date.now();
        const m = await api.readShared(peer, thread.id, after.current);
        if (!live) return;
        if (typeof m === "string") {
          setFault(m);
          await pause(RETRY_MS);
          continue;
        }
        setFault(null);
        after.current = m.next;
        setMirror(m);
        setLines((cur) => [...(cur ?? []), ...m.lines].slice(-2000));
        if (m.lines.length === 0 && Date.now() - t0 < 1000) await pause(QUIET_MS);
      }
    };
    void loop();
    return () => {
      live = false;
    };
  }, [api, peer, thread.id]);

  useLayoutEffect(() => {
    const el = list.current;
    if (el !== null) el.scrollTop = el.scrollHeight;
  }, [lines?.length]);

  const role = mirror?.thread.role || thread.role;
  const pending = mirror?.pending ?? [];
  const post = async () => {
    const t = text.trim();
    if (t === "") return;
    const err = await api.postShared(peer, thread.id, t);
    if (err !== null) onError(err);
    else setText("");
  };
  const answer = (id: string, allow: boolean) =>
    void api.answerShared(peer, thread.id, id, allow).then((e) => {
      if (e !== null) onError(e);
    });

  return (
    <div className="shared-room flex min-h-0 flex-1 flex-col" data-shared={`${peer}/${thread.id}`}>
      <header className="topbar drag flex h-13 shrink-0 items-center gap-2.5 px-4 text-sm">
        {!sidebar && (
          <Button variant="ghost-muted" size="icon-sm" aria-label="Show the sidebar" title="Show the sidebar" onClick={onShowSidebar}>
            <PanelLeft />
          </Button>
        )}
        <span className="room-index inline-flex shrink-0 items-center gap-1 font-mono text-2xs text-faint">
          <span aria-hidden>◇</span>
          <span>{peer}</span>
        </span>
        <h1 className="title min-w-0 flex-1 truncate font-medium text-foreground">{mirror?.thread.title || thread.title}</h1>
        <span className="shrink-0 text-2xs text-faint">
          you are {role} · {mirror?.thread.mode || thread.mode} mode
        </span>
      </header>
      {/* As in the room: the rows run under the composer, which floats over
          the sheet's bottom edge and measures itself into --composer-h. */}
      <div className="room-body relative flex min-h-0 flex-1 flex-col">
        <div ref={list} className="timeline min-h-0 flex-1 overflow-y-auto">
          <div className="timeline-inner mx-auto flex w-full max-w-(--chat-max) flex-col gap-7 px-6 pt-6 pb-[calc(var(--composer-h,0px)+1.5rem)]">
            {fault !== null && <p className="text-center text-destructive-foreground text-xs">{fault}</p>}
            {lines === null ? (
              <p className="flex items-center justify-center gap-2 py-24 text-faint text-sm">
                <Loader2 className="size-3.5 animate-spin" /> Reading {peer}'s room…
              </p>
            ) : lines.length === 0 ? (
              <p className="empty py-24 text-center text-faint text-sm">Nothing in this room yet.</p>
            ) : (
              group(lines).map((g, i) => <Rows key={i} g={g} theme={theme} />)
            )}
          </div>
        </div>
        <div ref={dock} className="composer-dock pointer-events-none absolute inset-x-0 bottom-0 z-20 px-4 pb-3 *:pointer-events-auto">
          <div className="mx-auto flex w-full max-w-(--chat-max) flex-col gap-2">
            {pending.map((p) => (
              <div key={p.id} className="shared-approval liquid-thick flex animate-enter items-center gap-3 rounded-[18px] py-2.5 pr-2.5 pl-3" data-approval={p.id}>
                <span aria-hidden className="grid size-6 shrink-0 place-items-center rounded-full bg-warning/15 text-warning-foreground">
                  <ShieldQuestion className="size-3.5" weight="fill" />
                </span>
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="text-2xs text-muted-foreground">
                    <span className="font-mono">{p.agent}</span> asks to {p.kind || "act"}
                  </span>
                  <span className="truncate font-mono text-code text-foreground">{p.summary}</span>
                </span>
                {role === "editor" ? (
                  <>
                    <Button size="sm" variant="ghost-muted" onClick={() => answer(p.id, false)}>
                      Decline
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => answer(p.id, true)}>
                      Approve
                    </Button>
                  </>
                ) : (
                  <span className="shrink-0 text-2xs text-faint">{peer} or an editor answers</span>
                )}
              </div>
            ))}
            {role === "participant" || role === "editor" ? (
              <div className="composer-box liquid flex items-end gap-2 rounded-[22px] p-2 pl-2.5 transition-[box-shadow] duration-200 focus-within:ring-[3px] focus-within:ring-primary/15 focus-within:[--glass-rim:color-mix(in_srgb,var(--primary)_55%,transparent)]">
                <textarea
                  aria-label="Post to the shared room"
                  placeholder={`Write in ${peer}'s room…`}
                  rows={2}
                  value={text}
                  className="min-h-11 flex-1 resize-none bg-transparent px-2 py-1.5 text-[14px] leading-[1.55] outline-none placeholder:text-faint"
                  onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !e.shiftKey) {
                      e.preventDefault();
                      void post();
                    }
                  }}
                />
                <Button
                  size="icon"
                  variant={text.trim() === "" ? "secondary" : "default"}
                  className={text.trim() === "" ? "text-faint disabled:opacity-100" : undefined}
                  aria-label="Post"
                  disabled={text.trim() === ""}
                  onClick={() => void post()}
                >
                  <ArrowUp className="size-4" weight="bold" />
                </Button>
              </div>
            ) : (
              <p className="liquid-clear mx-auto w-fit rounded-full px-3.5 py-1.5 text-center text-2xs text-muted-foreground">
                You follow this room as an observer. It is read-only.
              </p>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
