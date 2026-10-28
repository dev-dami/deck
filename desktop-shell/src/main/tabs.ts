import type { BrowserOwner } from "../shared/bridge";

// The shared browser's tabs: who owns each, how many pages are alive, and
// when a page is let go. There is one Chromium for every agent of every
// Deck; an agent costs a tab, not a process, and a tab costs memory only
// while its page is alive. This file is the bookkeeping and the policy;
// browser.ts gives the tabs their pages.

export interface Limits {
  /** Pages alive at once; the rest of the tabs are discarded (url and title kept). */
  live: number;
  /** Tabs one agent keeps; opening another closes its least recently used. */
  perAgent: number;
  /** Tabs in all, discarded ones included. */
  total: number;
  /** A tab nobody touched for this long is discarded. */
  idleMs: number;
}

/** How long what an agent did stays on its tab. */
export const DOING_MS = 2500;

export const LIMITS: Limits = { live: 8, perAgent: 6, total: 40, idleMs: 5 * 60_000 };

/** How a page behaves for its state: only the tab being looked at runs free. */
export interface Throttle {
  visible: boolean;
  /** Chromium may slow timers and animation of a page nobody sees. */
  throttling: boolean;
  muted: boolean;
  /** Frames per second to ask for (offscreen pages only; others ignore it). */
  frameRate: number;
}

export const SHOWN: Throttle = { visible: true, throttling: false, muted: false, frameRate: 60 };
export const HIDDEN: Throttle = { visible: false, throttling: true, muted: true, frameRate: 1 };

/** The throttle of a page: shown to the person, or in the background. */
export function throttleFor(shown: boolean): Throttle {
  return shown ? SHOWN : HIDDEN;
}

/** A page of a tab. */
export interface Page {
  destroy(): void;
  apply(t: Throttle): void;
}

export interface Tab<P extends Page = Page> {
  id: string;
  deck: string;
  /** null: the person's own tab. */
  owner: BrowserOwner | null;
  url: string;
  title: string;
  /** null once discarded: the next use makes a page again. */
  page: P | null;
  lastUsed: number;
  /** Ops running on the tab. */
  busy: number;
  /** What the agent is doing, for a moment after it did it. */
  doing: string | null;
  doingUntil: number;
  /** The person took the tab over: the agent's ops on it are refused. */
  paused: boolean;
  /** Origins its agent may go to, as of its last op. */
  allowed: readonly string[];
  /** Every origin the agent may go to, wherever it was told. */
  free: boolean;
}

/** What `pickDiscards` needs to know of a tab. */
export interface Candidate {
  id: string;
  alive: boolean;
  lastUsed: number;
  shown: boolean;
  busy: boolean;
}

/**
 * The tabs whose pages should go: those nobody touched for idleMs, then,
 * while more pages live than the limit allows, the least recently used.
 * A tab being looked at or working is never picked.
 */
export function pickDiscards(tabs: readonly Candidate[], now: number, l: Limits): string[] {
  const free = tabs.filter((t) => t.alive && !t.shown && !t.busy);
  const out = new Set(free.filter((t) => now - t.lastUsed >= l.idleMs).map((t) => t.id));
  let alive = tabs.filter((t) => t.alive).length - out.size;
  for (const t of free.filter((x) => !out.has(x.id)).sort((a, b) => a.lastUsed - b.lastUsed)) {
    if (alive <= l.live) break;
    out.add(t.id);
    alive--;
  }
  return [...out];
}

export class TabLimitError extends Error {}

export class TabManager<P extends Page = Page> {
  private readonly tabs = new Map<string, Tab<P>>();
  private next = 1;
  private shownId: string | null = null;

  constructor(
    /** Makes the page of a tab; null when it cannot (no window). */
    private readonly make: (tab: Tab<P>) => P | null,
    private readonly changed: () => void = () => {},
    readonly now: () => number = Date.now,
    readonly limits: Limits = LIMITS,
  ) {}

  get shown(): string | null {
    return this.shownId;
  }

  get(id: string): Tab<P> | undefined {
    return this.tabs.get(id);
  }

  list(deck?: string): Tab<P>[] {
    return [...this.tabs.values()].filter((t) => deck === undefined || t.deck === deck);
  }

  /** The tabs an agent owns, least recently used first. */
  ownedBy(deck: string, agent: string): Tab<P>[] {
    return this.list(deck)
      .filter((t) => t.owner?.agent === agent)
      .sort((a, b) => a.lastUsed - b.lastUsed);
  }

  /** The tab an agent used last. */
  latestOf(deck: string, agent: string): Tab<P> | undefined {
    return this.ownedBy(deck, agent).at(-1);
  }

  /**
   * Makes a tab for owner (null: the person) and its page. An agent already
   * at its limit loses its least recently used tab, which is returned.
   */
  open(deck: string, owner: BrowserOwner | null, url: string): { tab: Tab<P>; closed: Tab<P>[] } {
    const closed: Tab<P>[] = [];
    if (owner !== null) {
      const mine = this.ownedBy(deck, owner.agent);
      while (mine.length >= this.limits.perAgent) {
        const old = mine.find((t) => t.busy === 0 && t.id !== this.shownId);
        if (old === undefined) throw new TabLimitError(`You have ${mine.length} tabs, all busy; close one with browser.close.`);
        mine.splice(mine.indexOf(old), 1);
        this.close(old.id);
        closed.push(old);
      }
    }
    if (this.tabs.size >= this.limits.total) throw new TabLimitError(`The browser has ${this.tabs.size} tabs; close some first.`);
    const tab: Tab<P> = {
      id: `t${this.next++}`,
      deck,
      owner,
      url,
      title: "",
      page: null,
      lastUsed: this.now(),
      busy: 0,
      doing: null,
      doingUntil: 0,
      paused: false,
      allowed: [],
      free: false,
    };
    this.tabs.set(tab.id, tab);
    this.live(tab);
    this.changed();
    return { tab, closed };
  }

  /** The tab's page, made again if it was discarded. */
  live(tab: Tab<P>): P | null {
    if (tab.page === null) {
      tab.page = this.make(tab);
      if (tab.page !== null) {
        tab.page.apply(throttleFor(tab.id === this.shownId));
        this.trim(tab.id);
      }
    }
    return tab.page;
  }

  /** Marks the tab used; the page it needs is made first. */
  touch(tab: Tab<P>): void {
    tab.lastUsed = this.now();
  }

  /** An op starts on the tab (what: "clicking e12"); end() ends it. */
  begin(tab: Tab<P>, what: string): void {
    tab.busy++;
    tab.lastUsed = this.now();
    tab.doing = what;
    tab.doingUntil = Number.POSITIVE_INFINITY;
    this.changed();
  }

  end(tab: Tab<P>, linger = DOING_MS): void {
    tab.busy = Math.max(0, tab.busy - 1);
    tab.lastUsed = this.now();
    if (tab.busy === 0) tab.doingUntil = this.now() + linger;
    this.changed();
  }

  /** Clears the "doing" of tabs whose moment passed; true when one changed. */
  settle(): boolean {
    let any = false;
    for (const t of this.tabs.values()) {
      if (t.doing !== null && t.busy === 0 && this.now() >= t.doingUntil) {
        t.doing = null;
        any = true;
      }
    }
    if (any) this.changed();
    return any;
  }

  /** The tab the person looks at (null: none). */
  show(id: string | null): void {
    if (id === this.shownId) return;
    const prev = this.tabs.get(this.shownId ?? "");
    this.shownId = id;
    prev?.page?.apply(HIDDEN);
    const tab = this.tabs.get(id ?? "");
    if (tab !== undefined) {
      tab.lastUsed = this.now();
      this.live(tab)?.apply(SHOWN);
    }
    this.changed();
  }

  discard(id: string): void {
    const tab = this.tabs.get(id);
    if (tab?.page == null) return;
    const page = tab.page;
    tab.page = null;
    page.destroy();
    this.changed();
  }

  close(id: string): void {
    const tab = this.tabs.get(id);
    if (tab === undefined) return;
    this.discard(id);
    this.tabs.delete(id);
    if (this.shownId === id) this.shownId = null;
    this.changed();
  }

  /** Lets go of the pages that are idle or over the limit; the ids. */
  sweep(): string[] {
    const gone = pickDiscards(
      this.list().map((t) => ({ id: t.id, alive: t.page !== null, lastUsed: t.lastUsed, shown: t.id === this.shownId, busy: t.busy > 0 })),
      this.now(),
      this.limits,
    );
    for (const id of gone) this.discard(id);
    return gone;
  }

  /** After a page was made: discards the least recently used past the limit. */
  private trim(keep: string): void {
    const gone = pickDiscards(
      this.list().map((t) => ({
        id: t.id,
        alive: t.page !== null,
        lastUsed: t.id === keep ? Number.POSITIVE_INFINITY : t.lastUsed,
        shown: t.id === this.shownId || t.id === keep,
        busy: t.busy > 0,
      })),
      this.now(),
      // Idleness is the sweep's business.
      { ...this.limits, idleMs: Number.POSITIVE_INFINITY },
    );
    for (const id of gone) this.discard(id);
  }
}
