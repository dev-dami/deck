// The shared browser's tab budget: how many pages stay alive, how many tabs
// one agent keeps, and how a page behaves for what it is doing. There is one
// Chromium for every agent of every Deck; an agent costs a tab, and a tab
// costs memory only while its page is alive. This file is the policy;
// browser.ts keeps the tabs and makes and lets go of their pages.

export interface Limits {
  /** Pages alive at once; the rest of the tabs are discarded (address and title kept). */
  live: number;
  /** Tabs one agent keeps; opening another closes its least recently used. */
  perAgent: number;
  /** A tab nobody touched for this long is discarded. */
  idleMs: number;
}

export const LIMITS: Limits = { live: 8, perAgent: 6, idleMs: 5 * 60_000 };

/** Heavy ops (snapshot, screenshot) running at once, across all tabs. */
export const HEAVY_AT_ONCE = 2;

/** How long what an agent did stays on its tab, and how long a page works on after its last op. */
export const DOING_MS = 2500;
export const REST_MS = 3000;
export const SWEEP_MS = 5000;

/** How a page behaves for its state: only the tab being looked at runs free. */
export interface Throttle {
  visible: boolean;
  /** Chromium may slow timers and animation of a page nobody sees. */
  throttling: boolean;
  muted: boolean;
}

export const SHOWN: Throttle = { visible: true, throttling: false, muted: false };
export const HIDDEN: Throttle = { visible: false, throttling: true, muted: true };

/** What `pickDiscards` needs to know of a tab. */
export interface Candidate {
  id: string;
  alive: boolean;
  /** Wall clock of the last use, for idleness only. */
  lastUsed: number;
  /** Order of use: a counter that only goes up, so ties of the clock never decide who goes. */
  seq: number;
  shown: boolean;
  busy: boolean;
}

/**
 * The tabs whose pages should go: those nobody touched for idleMs, then,
 * while more pages live than the limit allows, the least recently used (by
 * seq). A tab being looked at or working is never picked.
 */
export function pickDiscards(tabs: readonly Candidate[], now: number, l: Limits): string[] {
  const free = tabs.filter((t) => t.alive && !t.shown && !t.busy);
  const out = new Set(free.filter((t) => now - t.lastUsed >= l.idleMs).map((t) => t.id));
  let alive = tabs.filter((t) => t.alive).length - out.size;
  for (const t of free.filter((x) => !out.has(x.id)).sort((a, b) => a.seq - b.seq)) {
    if (alive <= l.live) break;
    out.add(t.id);
    alive--;
  }
  return [...out];
}

/** The tab an agent gives up to open another at its limit: least recently used, neither busy nor shown. */
export function pickEviction(owned: readonly Candidate[]): string | null {
  return [...owned].filter((t) => !t.busy && !t.shown).sort((a, b) => a.seq - b.seq)[0]?.id ?? null;
}

/** A counting gate: at most n holders at once, the rest wait in order. */
export class Gate {
  private free: number;
  private readonly queue: (() => void)[] = [];

  constructor(n: number) {
    this.free = n;
  }

  /** Resolves with the release; aborting while waiting rejects and gives nothing up. */
  async acquire(signal?: AbortSignal): Promise<() => void> {
    if (this.free > 0) this.free--;
    else {
      await new Promise<void>((resolve, reject) => {
        const turn = () => {
          signal?.removeEventListener("abort", gone);
          resolve();
        };
        const gone = () => {
          const i = this.queue.indexOf(turn);
          if (i >= 0) this.queue.splice(i, 1);
          reject(signal?.reason);
        };
        if (signal?.aborted === true) return gone();
        signal?.addEventListener("abort", gone, { once: true });
        this.queue.push(turn);
      });
    }
    let done = false;
    return () => {
      if (done) return;
      done = true;
      const next = this.queue.shift();
      if (next !== undefined) next();
      else this.free++;
    };
  }
}
