import type { Session } from "@web/protocol";
import { relativeTime } from "@web/time";
import { Check, Eye, Handshake, Loader2, RefreshCw, Send, Users, X } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Team, Teammate, TeamThread } from "../crew";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { Button } from "./kit/button";
import { fieldClass } from "./kit/dialog";
import { Popover } from "./kit/menu";

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

function Card({ title, icon, action, children }: { title: string; icon?: ReactNode; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2.5">
      <div className="flex min-h-7 items-center justify-between px-1">
        <h2 className="flex items-center gap-2 text-foreground/70 text-sm">
          {icon}
          {title}
        </h2>
        {action}
      </div>
      <div className="rounded-xl border border-border/60 bg-card/40">{children}</div>
    </section>
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
    <li className="teammate flex flex-col gap-1.5 px-4 py-3" data-peer={m.name}>
      <div className="flex items-center gap-2 text-sm">
        <span className={cn("size-2 rounded-full", LINK_DOT[m.link] ?? LINK_DOT[""])} />
        <span className="font-medium">{m.name}</span>
        <span className="text-muted-foreground text-xs">{m.role}</span>
        <span className="ml-auto text-muted-foreground text-xs">
          {m.link === "online" ? "online" : m.lastSeen > 0 ? `seen ${relativeTime(new Date(m.lastSeen).toISOString(), now)} ago` : m.link || "never linked"}
        </span>
      </div>
      {m.agents.map((a) => (
        <div key={a.name} className="ml-4 flex min-w-0 items-center gap-2 text-xs">
          <span className={cn("size-1.5 shrink-0 rounded-full", a.status === "idle" ? "bg-muted-foreground/40" : "bg-info animate-deck-pulse")} />
          <span className="shrink-0">
            {m.name}/{a.name}
          </span>
          <span className="shrink-0 text-muted-foreground">{a.status}</span>
          {a.task !== "" && <span className="min-w-0 truncate text-muted-foreground">· {a.task}</span>}
          {a.contextPct > 0 && <span className="ml-auto shrink-0 text-muted-foreground/70 tabular-nums">{a.contextPct}%</span>}
        </div>
      ))}
      {m.threads.map((t) => (
        <div key={t.id} className="ml-4 flex min-w-0 items-center gap-2 text-xs">
          <Eye className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 truncate">{t.title}</span>
          <span className="shrink-0 text-muted-foreground">shared with you as {t.role}</span>
          <Button size="xs" variant="outline" className="ml-auto" onClick={() => onOpen(m.name, t)}>
            Open
          </Button>
        </div>
      ))}
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
    <div className="flex flex-col">
      <div ref={list} className="team-chat flex max-h-96 min-h-40 flex-col gap-2 overflow-y-auto p-4">
        {team.chat.length === 0 && <p className="text-muted-foreground text-xs">No messages yet.</p>}
        {team.chat.map((m) => (
          <div key={m.seq} className={cn("flex flex-col gap-0.5", m.mine && "items-end")}>
            <span className="text-2xs text-muted-foreground">
              {m.mine ? "You" : m.from}
              {m.agent !== "" ? `/${m.agent}` : ""} · {new Date(m.at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
            </span>
            <p className={cn("max-w-[85%] whitespace-pre-wrap rounded-2xl px-3 py-1.5 text-sm", m.mine ? "bg-primary text-primary-foreground" : "bg-message")}>{m.text}</p>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-2 border-t p-2">
        <input
          aria-label="Message the team"
          placeholder="Message the team…"
          value={text}
          className={cn(fieldClass, "h-8 py-1")}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void post();
          }}
        />
        <Button size="icon-sm" aria-label="Send to the team" disabled={text.trim() === ""} onClick={() => void post()}>
          <Send />
        </Button>
      </div>
    </div>
  );
}

function Contracts({ team, api, onError, onDone }: { team: Team; api: ProjectApi; onError: (t: string) => void; onDone: () => void }) {
  const [agent, setAgent] = useState(team.contractKinds[0] ?? "");
  if (team.contracts.length === 0) return <p className="px-4 py-3 text-muted-foreground text-xs">No contracts.</p>;
  const answer = async (peer: string, id: string, accept: boolean) => {
    const err = await api.answerContract(peer, id, accept, agent);
    if (err !== null) onError(err);
    onDone();
  };
  return (
    <ul className="divide-y divide-border/50">
      {[...team.contracts].reverse().map((c) => (
        <li key={c.id} className="contract flex flex-col gap-1 px-4 py-3" data-contract={c.id}>
          <div className="flex items-center gap-2 text-sm">
            <span className="font-medium">{c.title || c.id}</span>
            <span className="text-muted-foreground text-xs">
              {c.inbound ? `from ${c.peer}` : `to ${c.peer}`} · {c.state}
              {c.agent !== "" ? ` · ${c.agent}` : ""}
            </span>
          </div>
          {c.brief !== "" && <p className="whitespace-pre-wrap text-muted-foreground text-xs">{c.brief}</p>}
          {c.summary !== "" && <p className="whitespace-pre-wrap text-xs">{c.summary}</p>}
          {c.note !== "" && <p className="text-warning-foreground text-xs">{c.note}</p>}
          {c.inbound && c.state === "proposed" && (
            <div className="flex items-center gap-2 pt-1">
              <span className="text-muted-foreground text-xs">Run it with</span>
              <select
                aria-label="Agent for the contract"
                value={agent}
                onChange={(e) => setAgent(e.target.value)}
                className="h-7 rounded-md border bg-background px-2 text-xs"
              >
                {team.contractKinds.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
              <Button size="xs" className="ml-auto" onClick={() => void answer(c.peer, c.id, true)}>
                <Check /> Accept
              </Button>
              <Button size="xs" variant="ghost-muted" onClick={() => void answer(c.peer, c.id, false)}>
                <X /> Decline
              </Button>
            </div>
          )}
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
  return (
    <div className="flex flex-col gap-2 p-4">
      <p className="text-muted-foreground text-xs leading-relaxed">
        Ask a teammate's Deck to do a task for "{thread.title || "this thread"}". It runs there on a copy of the tree, under their plan;
        what it changed comes back as a capsule on your task board.
      </p>
      <div className="flex items-center gap-2">
        <select aria-label="Teammate" value={peer} onChange={(e) => setPeer(e.target.value)} className="h-8 rounded-md border bg-background px-2 text-sm">
          {team.teammates.map((m) => (
            <option key={m.name} value={m.name}>
              {m.name}
            </option>
          ))}
        </select>
        <input aria-label="Contract brief" placeholder="What should they do?" value={brief} className={cn(fieldClass, "h-8 py-1")} onChange={(e) => setBrief(e.target.value)} />
        <Button size="sm" disabled={peer === "" || brief.trim() === ""} onClick={() => void go()}>
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
    <header className="topbar drag flex h-12 shrink-0 items-center gap-2 border-b px-5 text-sm">
      <Users className="size-4 text-muted-foreground" />
      <h1 className="title font-medium">Team</h1>
      {typeof team === "object" && team !== null && team.available && (
        <span className="text-muted-foreground text-xs">
          you are {team.name || "unnamed"}
          {team.enabled ? "" : " · the link is off"}
        </span>
      )}
      <Button variant="ghost-muted" size="icon-sm" className="ml-auto" aria-label="Refresh" title="Refresh" onClick={() => void load()}>
        <RefreshCw />
      </Button>
    </header>
  );

  let body: ReactNode;
  if (api === undefined) {
    body = <p className="py-20 text-center text-muted-foreground text-sm">Teammates link to a Deck on this computer. Open a project folder.</p>;
  } else if (team === null) {
    body = (
      <p className="flex items-center justify-center gap-2 py-20 text-muted-foreground text-sm">
        <Loader2 className="size-4 animate-spin" /> Reading the team link…
      </p>
    );
  } else if (typeof team === "string") {
    body = <p className="py-20 text-center text-destructive-foreground text-sm">{team}</p>;
  } else if (!team.available) {
    body = (
      <div className="mx-auto flex max-w-md flex-col items-center gap-2 py-20 text-center">
        <Users className="size-6 text-muted-foreground/60" />
        <p className="font-medium text-sm">No team link yet</p>
        <p className="text-muted-foreground text-xs leading-relaxed">
          Turn on peers in Deck's settings and pair with a teammate's Deck. Then you see what their agents do, chat, share threads
          and hand each other tasks.
        </p>
      </div>
    );
  } else {
    body = (
      <div className="grid gap-6 lg:grid-cols-[1fr_minmax(0,22rem)]">
        <div className="flex flex-col gap-6">
          <Card title="Teammates" icon={<Users className="size-4" />}>
            {team.teammates.length === 0 ? (
              <p className="px-4 py-3 text-muted-foreground text-xs">No teammates paired yet.</p>
            ) : (
              <ul className="divide-y divide-border/50">
                {team.teammates.map((m) => (
                  <Mate key={m.name} m={m} now={now} onOpen={onOpenShared} />
                ))}
              </ul>
            )}
          </Card>
          <Card title="Contracts" icon={<Handshake className="size-4" />}>
            <Contracts team={team} api={api} onError={onError} onDone={() => void load()} />
            {thread !== undefined && team.teammates.length > 0 && (
              <div className="border-t">
                <Propose team={team} api={api} thread={thread} onError={onError} onInfo={onInfo} />
              </div>
            )}
          </Card>
        </div>
        <Card title="Team chat">
          <Chat team={team} api={api} onError={onError} onDone={() => void load()} />
        </Card>
      </div>
    );
  }

  return (
    <div className="team flex min-h-0 flex-1 flex-col" data-team="">
      {header}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-6 py-8">{body}</div>
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
            <div className="inline-flex rounded-lg border bg-background p-0.5" role="radiogroup" aria-label={`Share with ${m.name}`}>
              {ROLES.map((r) => (
                <button
                  key={r.role}
                  type="button"
                  role="radio"
                  aria-checked={(shares[m.name] ?? "") === r.role}
                  onClick={() => void set(m.name, r.role)}
                  className={cn(
                    "h-6 cursor-pointer whitespace-nowrap rounded-md px-1.5 text-2xs",
                    (shares[m.name] ?? "") === r.role ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
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

