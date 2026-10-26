import type { Session } from "@web/protocol";
import { relativeTime } from "@web/time";
import { kindColour } from "@web/theme";
import { Check, Eye, Handshake, Loader2, MessageSquarePlus as ChatCircleText, RefreshCw, Send, Users, X } from "./icons";
import { type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Team, Teammate, TeamThread } from "../crew";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { Button } from "./kit/button";
import { EmptyState } from "./kit/empty";
import { fieldClass } from "./kit/dialog";
import { Popover } from "./kit/menu";
import { Ring } from "./kit/ring";
import { segmentItem, segmentTrack } from "./kit/segmented";

// Working with teammates' Decks (docs/multiplayer-design.md §8): who is
// there and what their agents do, the team chat and contracts both ways.
// The room header shares a thread (ShareStack); a thread a teammate shares
// opens in the room view (SharedRoom).

const ROLES = [
  { role: "", label: "Not shared" },
  { role: "observer", label: "Observer" },
  { role: "participant", label: "Participant" },
  { role: "editor", label: "Editor" },
] as const;

const LINK_DOT: Record<string, string> = {
  online: "bg-success",
  connecting: "bg-warning animate-deck-pulse",
  offline: "bg-muted-foreground/40",
  "": "bg-muted-foreground/25",
};

// A contract's state as a dot: waiting on someone, under way, done, or not.
const CONTRACT_DOT: Record<string, string> = {
  proposed: "bg-warning",
  accepted: "bg-primary",
  running: "bg-primary animate-deck-pulse",
  done: "bg-success",
  delivered: "bg-success",
  declined: "bg-faint",
  failed: "bg-destructive",
};

// A card on the page: its title and glyph, an action at the trailing edge.
function Card({
  title,
  icon,
  action,
  className,
  children,
}: {
  title: string;
  icon?: ReactNode;
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section
      aria-label={title}
      className={cn("flex flex-col overflow-hidden rounded-(--sheet-radius) bg-surface-1 shadow-[0_1px_2px_rgb(0_0_0/0.04)] ring-1 ring-hairline", className)}
    >
      <div className="flex min-h-11 items-center justify-between gap-2 px-4 pt-1">
        <h2 className="flex items-center gap-2 font-semibold text-[13px] [&_svg]:size-4 [&_svg]:text-muted-foreground">
          {icon}
          {title}
        </h2>
        {action}
      </div>
      {children}
    </section>
  );
}

// A teammate's face: their initials on glass, with a dot for their link.
function Avatar({ name, link }: { name: string; link: string }) {
  return (
    <span aria-hidden className="liquid-clear relative grid size-9 shrink-0 place-items-center rounded-full font-semibold text-2xs text-muted-foreground">
      {initials(name)}
      <span className={cn("absolute -right-px -bottom-px size-2.5 rounded-full ring-2 ring-surface-1", LINK_DOT[link] ?? LINK_DOT[""])} />
    </span>
  );
}

// The team as one poll every few seconds while it shows.
export function useTeam(api: ProjectApi | undefined, every = 5000) {
  const [team, setTeam] = useState<Team | string | null>(null);
  const load = useCallback(async () => {
    if (api === undefined) return;
    setTeam(await api.team());
  }, [api]);
  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), every);
    return () => clearInterval(t);
  }, [load, every]);
  return { team, load };
}

function Mate({ m, now, onOpen }: { m: Teammate; now: number; onOpen: (peer: string, t: TeamThread) => void }) {
  return (
    <li className="teammate flex gap-3 px-4 py-3.5" data-peer={m.name}>
      <Avatar name={m.name} link={m.link} />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div className="flex min-w-0 items-baseline gap-2 pt-0.5">
          <span className="font-medium text-[13px]">{m.name}</span>
          {m.role !== "" && <span className="text-faint text-xs">{m.role}</span>}
          <span className={cn("ml-auto shrink-0 text-xs", m.link === "online" ? "text-success-foreground" : "text-faint")}>
            {m.link === "online" ? "online" : m.lastSeen > 0 ? `seen ${relativeTime(new Date(m.lastSeen).toISOString(), now)} ago` : m.link || "never linked"}
          </span>
        </div>
        {(m.agents.length > 0 || m.threads.length > 0) && (
          <ul className="flex flex-col gap-1">
            {m.agents.map((a) => (
              <li key={a.name} className="flex min-w-0 items-center gap-2 rounded-[10px] bg-hover px-2.5 py-1.5 text-xs">
                <span
                  aria-hidden
                  className={cn("size-1.5 shrink-0 rounded-full", a.status !== "idle" && "animate-deck-pulse")}
                  style={{ background: a.status === "idle" ? "var(--text-faint)" : kindColour(a.kind) }}
                />
                <span className="shrink-0 font-medium">
                  {m.name}/{a.name}
                </span>
                <span className="shrink-0 text-muted-foreground">{a.status}</span>
                {a.task !== "" && <span className="min-w-0 truncate text-muted-foreground">· {a.task}</span>}
                {a.contextPct > 0 && (
                  <span className="ml-auto flex shrink-0 items-center gap-1 text-faint tabular-nums" title="Context used">
                    <Ring pct={a.contextPct} />
                    {a.contextPct}%
                  </span>
                )}
              </li>
            ))}
            {m.threads.map((t) => (
              <li key={t.id} className="flex min-w-0 items-center gap-2 rounded-[10px] px-2.5 py-1 text-xs">
                <Eye aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 truncate font-medium">{t.title}</span>
                <span className="shrink-0 text-faint">shared with you as {t.role}</span>
                <Button size="xs" variant="outline" className="ml-auto" onClick={() => onOpen(m.name, t)}>
                  Open
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </li>
  );
}

function Chat({ team, api, onError, onDone }: { team: Team; api: ProjectApi; onError: (t: string) => void; onDone: () => void }) {
  const [text, setText] = useState("");
  const list = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = list.current;
    if (el !== null) el.scrollTop = el.scrollHeight;
  }, [team.chat.length]);
  const post = async () => {
    const t = text.trim();
    if (t === "") return;
    const r = await api.teamPost(t);
    if (typeof r === "string") return onError(r);
    setText("");
    onDone();
  };
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div ref={list} className="team-chat flex max-h-[28rem] min-h-48 flex-1 flex-col gap-2.5 overflow-y-auto px-4 pt-1 pb-4">
        {team.chat.length === 0 && <p className="m-auto text-faint text-xs">No messages yet. Say hello to the team.</p>}
        {team.chat.map((m) => (
          <div key={m.seq} className={cn("flex flex-col gap-1", m.mine && "items-end")}>
            <span className="px-1 text-2xs text-faint">
              {m.mine ? "You" : m.from}
              {m.agent !== "" ? `/${m.agent}` : ""} · {new Date(m.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
            </span>
            <p
              className={cn(
                "max-w-[85%] whitespace-pre-wrap text-pretty px-3 py-1.5 text-[13px] inset-shadow-hl",
                m.mine ? "rounded-[18px_18px_6px_18px] bg-bubble" : "rounded-[18px_18px_18px_6px] bg-surface-2 ring-1 ring-hairline",
              )}
            >
              {m.text}
            </p>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-2 border-hairline border-t p-2.5">
        <input
          aria-label="Message the team"
          placeholder="Message the team…"
          value={text}
          className={cn(fieldClass, "h-8 rounded-full py-1")}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void post();
          }}
        />
        <Button
          size="icon-sm"
          variant={text.trim() === "" ? "outline" : "default"}
          aria-label="Send to the team"
          disabled={text.trim() === ""}
          onClick={() => void post()}
        >
          <Send />
        </Button>
      </div>
    </div>
  );
}

const selectClass =
  "h-8 cursor-pointer rounded-full border border-input bg-hover px-3 text-xs outline-none transition-[border-color,box-shadow] focus:border-primary/50 focus:ring-[3px] focus:ring-ring";

function Contracts({ team, api, onError, onDone }: { team: Team; api: ProjectApi; onError: (t: string) => void; onDone: () => void }) {
  const [agent, setAgent] = useState(team.contractKinds[0] ?? "");
  if (team.contracts.length === 0) return <p className="px-4 pb-4 text-faint text-xs">No contracts yet.</p>;
  const answer = async (peer: string, id: string, accept: boolean) => {
    const err = await api.answerContract(peer, id, accept, agent);
    if (err !== null) onError(err);
    onDone();
  };
  return (
    <ul className="flex flex-col px-1.5 pb-1.5 [&>li+li]:border-hairline [&>li+li]:border-t">
      {[...team.contracts].reverse().map((c) => (
        <li key={c.id} className="contract flex flex-col gap-1.5 px-2.5 py-3" data-contract={c.id}>
          <div className="flex items-center gap-2">
            <span aria-hidden className={cn("size-2 shrink-0 rounded-full", CONTRACT_DOT[c.state] ?? "bg-faint")} />
            <span className="min-w-0 truncate font-medium text-[13px]">{c.title || c.id}</span>
            <span className="shrink-0 text-faint text-xs">
              {c.inbound ? `from ${c.peer}` : `to ${c.peer}`} · {c.state}
              {c.agent !== "" ? ` · ${c.agent}` : ""}
            </span>
          </div>
          <div className="flex flex-col gap-1.5 pl-4">
            {c.brief !== "" && <p className="whitespace-pre-wrap text-pretty text-muted-foreground text-xs">{c.brief}</p>}
            {c.summary !== "" && <p className="whitespace-pre-wrap text-pretty text-xs">{c.summary}</p>}
            {c.note !== "" && <p className="text-pretty text-warning-foreground text-xs">{c.note}</p>}
            {c.inbound && c.state === "proposed" && (
              <div className="flex flex-wrap items-center gap-2 pt-1">
                <span className="text-muted-foreground text-xs">Run it with</span>
                <select aria-label="Agent for the contract" value={agent} onChange={(e) => setAgent(e.target.value)} className={cn(selectClass, "h-7")}>
                  {team.contractKinds.map((k) => (
                    <option key={k} value={k}>
                      {k}
                    </option>
                  ))}
                </select>
                <Button size="xs" variant="ghost-muted" className="ml-auto" onClick={() => void answer(c.peer, c.id, false)}>
                  <X /> Decline
                </Button>
                <Button size="xs" onClick={() => void answer(c.peer, c.id, true)}>
                  <Check /> Accept
                </Button>
              </div>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}

function Propose({ team, api, thread, onError, onInfo }: { team: Team; api: ProjectApi; thread: Session; onError: (t: string) => void; onInfo: (t: string) => void }) {
  const [peer, setPeer] = useState(team.teammates[0]?.name ?? "");
  const [brief, setBrief] = useState("");
  const go = async () => {
    const r = await api.proposeContract(thread.id, peer, brief);
    if (typeof r === "string") return onError(r);
    setBrief("");
    onInfo(r.queued ? `${peer} is away: the request goes when their link is back (task ${r.taskId}).` : `Asked ${peer} (task ${r.taskId}).`);
  };
  const ready = peer !== "" && brief.trim() !== "";
  return (
    <div className="flex flex-col gap-2.5 bg-hover/50 px-4 py-3.5">
      <p className="text-pretty text-muted-foreground text-xs leading-relaxed">
        Ask a teammate's Deck to do a task for <span className="text-foreground">"{thread.title || "this thread"}"</span>. It runs there on a
        copy of the tree, under their plan; what it changed comes back as a capsule on your task board.
      </p>
      <div className="flex items-center gap-2">
        <select aria-label="Teammate" value={peer} onChange={(e) => setPeer(e.target.value)} className={selectClass}>
          {team.teammates.map((m) => (
            <option key={m.name} value={m.name}>
              {m.name}
            </option>
          ))}
        </select>
        <input
          aria-label="Contract brief"
          placeholder="What should they do?"
          value={brief}
          className={cn(fieldClass, "h-8 rounded-full py-1")}
          onChange={(e) => setBrief(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && ready) void go();
          }}
        />
        <Button size="sm" variant="outline" disabled={!ready} onClick={() => void go()}>
          <Handshake /> Ask
        </Button>
      </div>
    </div>
  );
}

export function TeamPage({
  api,
  thread,
  onError,
  onInfo,
  onOpenShared,
}: {
  api: ProjectApi | undefined;
  thread: Session | undefined;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
  // Opens a thread a teammate shares in the room view.
  onOpenShared: (peer: string, t: TeamThread) => void;
}) {
  const { team, load } = useTeam(api);
  const now = Date.now();

  const header = (
    <header className="topbar drag flex h-13 shrink-0 items-center gap-2 px-5 text-sm">
      <Users className="size-4 text-muted-foreground" />
      <h1 className="title font-semibold">Team</h1>
      {typeof team === "object" && team !== null && team.available && (
        <span className="flex items-center gap-1.5 text-faint text-xs">
          you are <span className="font-medium text-muted-foreground">{team.name || "unnamed"}</span>
          {!team.enabled && (
            <span className="flex items-center gap-1 rounded-full bg-hover px-2 py-0.5 text-2xs">
              <span aria-hidden className="size-1.5 rounded-full bg-faint" />
              the link is off
            </span>
          )}
        </span>
      )}
      <Button variant="ghost-muted" size="icon-sm" className="ml-auto" aria-label="Refresh" title="Refresh" onClick={() => void load()}>
        <RefreshCw />
      </Button>
    </header>
  );

  let body: ReactNode;
  if (api === undefined) {
    body = <EmptyState icon={Users} title="No team here" hint="Teammates link to a Deck on this computer. Open a project folder." className="py-24" />;
  } else if (team === null) {
    body = (
      <p className="flex items-center justify-center gap-2 py-24 text-muted-foreground text-sm">
        <Loader2 className="size-4 animate-spin" /> Reading the team link…
      </p>
    );
  } else if (typeof team === "string") {
    body = <p className="py-24 text-center text-destructive-foreground text-sm">{team}</p>;
  } else if (!team.available) {
    body = (
      <EmptyState
        icon={Users}
        title="No team link yet"
        hint="Turn on peers in Deck's settings and pair with a teammate's Deck. Then you see what their agents do, chat, share threads and hand each other tasks."
        className="mx-auto max-w-md py-24"
      />
    );
  } else {
    const online = team.teammates.filter((m) => m.link === "online").length;
    body = (
      <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <div className="flex min-w-0 flex-col gap-3">
          <Card
            title="Teammates"
            icon={<Users weight="duotone" />}
            action={
              team.teammates.length > 0 && (
                <span className="text-faint text-xs tabular-nums">
                  {online} of {team.teammates.length} online
                </span>
              )
            }
          >
            {team.teammates.length === 0 ? (
              <EmptyState icon={Users} title="No teammates yet." hint="Pair with a Deck from Settings → Connection." className="py-8" />
            ) : (
              <ul className="[&>li+li]:border-hairline [&>li+li]:border-t">
                {team.teammates.map((m) => (
                  <Mate key={m.name} m={m} now={now} onOpen={onOpenShared} />
                ))}
              </ul>
            )}
          </Card>
          <Card title="Contracts" icon={<Handshake weight="duotone" />}>
            <Contracts team={team} api={api} onError={onError} onDone={() => void load()} />
            {thread !== undefined && team.teammates.length > 0 && (
              <div className="border-hairline border-t">
                <Propose team={team} api={api} thread={thread} onError={onError} onInfo={onInfo} />
              </div>
            )}
          </Card>
        </div>
        <Card title="Team chat" icon={<ChatCircleText weight="duotone" />} className="lg:sticky lg:top-0 lg:self-start">
          <Chat team={team} api={api} onError={onError} onDone={() => void load()} />
        </Card>
      </div>
    );
  }

  return (
    <div className="team flex min-h-0 flex-1 flex-col" data-team="">
      {header}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-3 px-6 pt-2 pb-10">{body}</div>
      </div>
    </div>
  );
}

// The teammates who follow a room, and as what (ThreadShares). set changes a
// role and keeps the local copy in step; it returns Deck's refusal.
export function useShares(api: ProjectApi | undefined, sessionId: string | null, every = 15_000) {
  const [got, setGot] = useState<{ id: string; shares: Record<string, string> } | null>(null);
  const load = useCallback(async () => {
    if (api === undefined || sessionId === null) return;
    const r = await api.threadShares(sessionId);
    if (typeof r !== "string") setGot({ id: sessionId, shares: r });
  }, [api, sessionId]);
  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), every);
    return () => clearInterval(t);
  }, [load, every]);
  const set = useCallback(
    async (peer: string, role: string): Promise<string | null> => {
      if (api === undefined || sessionId === null) return null;
      const err = await api.shareThread(sessionId, peer, role);
      if (err !== null) return err;
      setGot((g) => {
        const shares = { ...(g !== null && g.id === sessionId ? g.shares : {}) };
        if (role === "") delete shares[peer];
        else shares[peer] = role;
        return { id: sessionId, shares };
      });
      return null;
    },
    [api, sessionId],
  );
  const shares = got !== null && got.id === sessionId ? got.shares : NO_SHARES;
  return { shares, set };
}

const NO_SHARES: Record<string, string> = {};

// Two letters for an avatar: "vps" reads "VP", "ana.laptop" reads "AL".
export function initials(name: string): string {
  const words = name.split(/[^\p{L}\p{N}]+/u).filter((w) => w !== "");
  const two = words.length > 1 ? `${words[0]![0]}${words[1]![0]}` : (words[0] ?? name).slice(0, 2);
  return two.toUpperCase();
}

// The room header's share control: the followers as a small avatar stack
// (three, then "+N"), or a quiet Share button when nobody follows. It opens
// the teammates with a role each. Hidden until a team link has teammates.
export function ShareStack({
  team,
  shares,
  onSet,
  onError,
  ask = 0,
}: {
  team: Team | string | null;
  shares: Record<string, string>;
  onSet: (peer: string, role: string) => Promise<string | null>;
  onError: (t: string) => void;
  // Opens the popover each time it changes (the composer's /share).
  ask?: number;
}) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (ask > 0) setOpen(true);
  }, [ask]);
  if (typeof team !== "object" || team === null || !team.available || team.teammates.length === 0) return null;
  const followers = Object.keys(shares)
    .filter((p) => shares[p] !== "")
    .sort((a, b) => a.localeCompare(b));
  const shown = followers.slice(0, 3);
  const more = followers.length - shown.length;
  const set = async (peer: string, role: string) => {
    const err = await onSet(peer, role);
    if (err !== null) onError(err);
  };
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      align="end"
      label="Share"
      role="dialog"
      className="w-[26rem] p-3"
      trigger={
        followers.length === 0 ? (
          <Button
            variant="ghost-muted"
            size="sm"
            className="share font-normal"
            aria-label="Share"
            title="Share this room"
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
          >
            <Users />
            <span className="sr-only @3xl/top:not-sr-only">Share</span>
          </Button>
        ) : (
          <button
            type="button"
            className="share share-stack flex h-7 cursor-pointer items-center rounded-md px-1 outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected"
            aria-label="Share"
            title={`Shared with ${followers.map((p) => `${p} (${shares[p]})`).join(", ")}`}
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
          >
            {shown.map((p) => (
              <span
                key={p}
                data-peer={p}
                className="share-avatar -ml-1.5 grid size-6 place-items-center rounded-full border border-hairline-strong bg-surface-2 font-medium text-3xs text-muted-foreground first:ml-0"
              >
                {initials(p)}
              </span>
            ))}
            {more > 0 && <span className="ml-1 font-mono text-2xs text-faint tabular-nums">+{more}</span>}
          </button>
        )
      }
    >
      <p className="pb-2 text-muted-foreground text-xs leading-relaxed">
        Observers read the room. Participants can also write in it. Editors can also answer its approvals.
      </p>
      <ul className="flex flex-col gap-2">
        {team.teammates.map((m) => (
          <li key={m.name} className="flex items-center gap-2 text-sm">
            <span className={cn("size-2 shrink-0 rounded-full", LINK_DOT[m.link] ?? LINK_DOT[""])} />
            <span className="min-w-0 flex-1 truncate">{m.name}</span>
            <div className={segmentTrack()} role="radiogroup" aria-label={`Share with ${m.name}`}>
              {ROLES.map((r) => (
                <button
                  key={r.role}
                  type="button"
                  role="radio"
                  aria-checked={(shares[m.name] ?? "") === r.role}
                  onClick={() => void set(m.name, r.role)}
                  className={cn(segmentItem({ on: (shares[m.name] ?? "") === r.role }), "h-5.5 px-2 text-2xs")}
                >
                  {r.label}
                </button>
              ))}
            </div>
          </li>
        ))}
      </ul>
    </Popover>
  );
}

