import type { Store } from "@web/controller";
import type { AgentLine, Message } from "@web/protocol";
import type { AppState } from "@web/state";
import { useRef, useSyncExternalStore } from "react";
import { agentKind } from "./agents";

// Slices of a store for React. The rpc stream sends every session's tokens
// into one AppState; a component that reads a slice re-renders only when
// that slice changes, so a busy thread in the background leaves the sidebar
// and the open timeline alone.

export function shallowEqual<T>(a: T, b: T): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  const ka = Object.keys(a);
  if (ka.length !== Object.keys(b).length) return false;
  return ka.every((k) => Object.is((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
}

// Props equal but for the functions: for a row whose handlers are new
// closures on each render but call through to the same place.
export function sameData<T extends object>(a: T, b: T): boolean {
  const ka = Object.keys(a);
  if (ka.length !== Object.keys(b).length) return false;
  return ka.every((k) => {
    const x = (a as Record<string, unknown>)[k];
    return typeof x === "function" || Object.is(x, (b as Record<string, unknown>)[k]);
  });
}

export interface Memo<S, T> {
  s: S;
  select: (s: S) => T;
  v: T;
}

// The slice of s, or the previous slice when it is equal, so that the
// caller gets the same object while nothing it reads changed.
export function pick<S, T>(memo: { current: Memo<S, T> | null }, s: S, select: (s: S) => T, equal: (a: T, b: T) => boolean): T {
  const m = memo.current;
  if (m !== null && m.s === s && m.select === select) return m.v;
  const next = select(s);
  const v = m !== null && equal(m.v, next) ? m.v : next;
  memo.current = { s, select, v };
  return v;
}

export function useSelect<S, T>(
  subscribe: (l: () => void) => () => void,
  get: () => S,
  select: (s: S) => T,
  equal: (a: T, b: T) => boolean = Object.is,
): T {
  const memo = useRef<Memo<S, T> | null>(null);
  return useSyncExternalStore(subscribe, () => pick(memo, get(), select, equal));
}

export function useAppSlice<T>(store: Store, select: (s: AppState) => T, equal: (a: T, b: T) => boolean = Object.is): T {
  return useSelect(store.subscribe, () => store.state, select, equal);
}

// What the shell itself reads. Messages and pages are left out: the
// timeline reads its own thread's.
export type ShellSlice = Pick<
  AppState,
  "sessions" | "sessionsLoaded" | "server" | "link" | "roster" | "approvals" | "queue" | "theme" | "toasts"
>;

export function shellSlice(s: AppState): ShellSlice {
  return {
    sessions: s.sessions,
    sessionsLoaded: s.sessionsLoaded,
    server: s.server,
    link: s.link,
    roster: s.roster,
    approvals: s.approvals,
    queue: s.queue,
    theme: s.theme,
    toasts: s.toasts,
  };
}

// What the side panel reads outside the side chat (RightPanel, Settings).
export function panelSlice(s: AppState): Pick<AppState, "sessions" | "server" | "link" | "approvals" | "queue" | "theme"> {
  return { sessions: s.sessions, server: s.server, link: s.link, approvals: s.approvals, queue: s.queue, theme: s.theme };
}

const NO_MESSAGES: Message[] = [];
const NO_ROSTER: AgentLine[] = [];

export interface ThreadSlice {
  messages: Message[];
  roster: AgentLine[];
  more: boolean;
}

export function threadSlice(id: string): (s: AppState) => ThreadSlice {
  return (s) => ({ messages: s.messages[id] ?? NO_MESSAGES, roster: s.roster[id] ?? NO_ROSTER, more: s.pages[id]?.more ?? false });
}

// The kinds of agent that wrote in each thread, for the sidebar's marks.
// One thread's result is kept while its roster and messages stay the same.
const kindsMemo = new WeakMap<Message[], { roster: AgentLine[] | undefined; kinds: string[] }>();

function kindsIn(roster: AgentLine[] | undefined, messages: Message[] | undefined): string[] {
  const hit = messages === undefined ? undefined : kindsMemo.get(messages);
  if (hit !== undefined && hit.roster === roster) return hit.kinds;
  const list = roster ?? NO_ROSTER;
  const seen = new Set(list.map((l) => l.kind));
  for (const m of messages ?? NO_MESSAGES) {
    if (m.role === "assistant" && m.agent !== undefined) seen.add(agentKind(m.agent, list));
  }
  const kinds = [...seen];
  if (messages !== undefined) kindsMemo.set(messages, { roster, kinds });
  return kinds;
}

export function kindsBySession(s: AppState): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const id of new Set([...Object.keys(s.roster), ...Object.keys(s.messages)])) {
    out[id] = kindsIn(s.roster[id], s.messages[id]);
  }
  return out;
}

const sameList = (a: string[] | undefined, b: string[] | undefined): boolean =>
  a === b || (a !== undefined && b !== undefined && a.length === b.length && a.every((x, i) => x === b[i]));

export function sameKinds(a: Record<string, string[]>, b: Record<string, string[]>): boolean {
  const ka = Object.keys(a);
  return ka.length === Object.keys(b).length && ka.every((k) => sameList(a[k], b[k]));
}
