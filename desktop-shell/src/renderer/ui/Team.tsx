import { relativeTime } from "@web/time";
import { kindColour } from "@web/theme";
import { Eye, Loader2, RefreshCw, Users } from "./icons";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import type { Team, Teammate, TeamThread } from "../crew";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { Button } from "./kit/button";
import { EmptyPlate } from "./kit/plate";
import { PLATES } from "../plates";
import { Popover } from "./kit/menu";
import { Ring } from "./kit/ring";
import { segmentItem, segmentTrack } from "./kit/segmented";

// Working with teammates' Decks (docs/multiplayer-design.md §8): who is
// there and what their agents do. Team chat and contracts were removed in
// Deck v3 (docs/design/v3/02-cuts.md). The room header shares a thread (ShareStack); a thread a teammate shares
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

// A part of the page: its title, an action at the trailing edge, then what
// it holds. No box: the title leads and space parts one from the next.
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
    <section aria-label={title} className={cn("flex min-w-0 flex-col", className)}>
      <div className="flex items-baseline justify-between gap-2 border-hairline border-b pb-2">
        <h2 className="flex items-center gap-2 font-semibold text-[13px] [&_svg]:size-4 [&_svg]:self-center [&_svg]:text-muted-foreground">
          {icon}
          {title}
        </h2>
        {action}
      </div>
      {children}
    </section>
  );
}

// A teammate's face: their initials, with a dot for their link.
function Avatar({ name, link }: { name: string; link: string }) {
  return (
    <span aria-hidden className="relative grid size-8 shrink-0 place-items-center rounded-full bg-selected font-semibold text-2xs text-muted-foreground">
      {initials(name)}
      <span className={cn("absolute -right-px -bottom-px size-2.5 rounded-full ring-2 ring-background", LINK_DOT[link] ?? LINK_DOT[""])} />
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
    <li className="teammate flex gap-3 py-3.5" data-peer={m.name}>
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
              <li key={a.name} className="flex min-w-0 items-center gap-2 py-0.5 text-xs">
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
              <li key={t.id} className="flex min-w-0 items-center gap-2 py-0.5 text-xs">
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

export function TeamPage({
  api,
  onOpenShared,
  onConnection,
  onAddProject,
}: {
  api: ProjectApi | undefined;
  // Opens a thread a teammate shares in the room view.
  onOpenShared: (peer: string, t: TeamThread) => void;
  // The empty pages' next step, when the shell gives it: Settings at
  // Connection, where a teammate's Deck is paired, and Add project.
  onConnection?: () => void;
  onAddProject?: () => void;
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
            <span>
              <span aria-hidden>· </span>the link is off
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
    body = (
      <EmptyPlate
        art={PLATES.wave}
        title="No team here"
        hint="Teammates link to a Deck on this computer. Open a project folder."
        className="py-20"
        action={
          onAddProject !== undefined && (
            <Button variant="outline" size="sm" onClick={onAddProject}>
              Add project…
            </Button>
          )
        }
      />
    );
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
      <EmptyPlate
        art={PLATES.wave}
        title="No team link yet"
        hint="Turn on peers in Deck's settings and pair with a teammate's Deck. Then you see what their agents do and share threads."
        className="py-20"
        action={
          onConnection !== undefined && (
            <Button variant="outline" size="sm" onClick={onConnection}>
              Open Connection settings
            </Button>
          )
        }
      />
    );
  } else {
    const online = team.teammates.filter((m) => m.link === "online").length;
    body = (
      <div className="flex min-w-0 flex-col gap-12">
        <Card
          title="Teammates"
          action={
            team.teammates.length > 0 && (
              <span className="text-faint text-xs tabular-nums">
                {online} of {team.teammates.length} online
              </span>
            )
          }
        >
          {team.teammates.length === 0 ? (
            <p className="flex flex-wrap items-baseline gap-x-2 py-4 text-muted-foreground text-xs">
              <span className="font-medium text-foreground text-sm">No teammates yet.</span>
              Pair with a Deck from Settings → Connection.
              {onConnection !== undefined && (
                <Button variant="link" size="xs" className="px-0" onClick={onConnection}>
                  Open Connection
                </Button>
              )}
            </p>
          ) : (
            <ul className="[&>li+li]:border-hairline [&>li+li]:border-t">
              {team.teammates.map((m) => (
                <Mate key={m.name} m={m} now={now} onOpen={onOpenShared} />
              ))}
            </ul>
          )}
        </Card>
      </div>
    );
  }

  return (
    <div className="team flex min-h-0 flex-1 flex-col" data-team="">
      {header}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-3 px-8 pt-4 pb-14">{body}</div>
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

