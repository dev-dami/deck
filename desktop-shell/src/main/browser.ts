import type { BrowserBounds, BrowserNav, BrowserOpen, BrowserOwner, BrowserState, BrowserTab } from "../shared/bridge";
import { type ControlledPage, PageControl, RefError } from "./pageControl";
import { judgeUrl, parseWebUrl, type Who } from "./preview";
import { DOING_MS, LIMITS, type Limits, type Page, type Tab, TabLimitError, TabManager } from "./tabs";

// The shared browser. One Chromium serves the person and every agent of
// every local Deck; an agent costs a tab. The person's tabs and the agents'
// are in one strip, and each tab's page is a view this class never sees
// directly: it asks a PageFactory (browserView.ts in the app) for one.
//
// Agents reach it from the engine: the engine sends each browser.* op as a
// request (decks.ts passes it to handle) and sends the answer back. Ops on
// one tab run one after another; the heavy ones (snapshot, screenshot) take
// turns across all tabs, so a dozen agents cost a dozen tabs of memory and
// at most HEAVY_AT_ONCE pages of work at a time.

/** A page of a tab, as the browser drives it. */
export interface BrowserPage extends Page, ControlledPage {
  info(): { url: string; title: string; loading: boolean; canBack: boolean; canForward: boolean };
  /** Puts the page at bounds in the window; null: where nobody sees it. */
  place(b: BrowserBounds | null): void;
  /** An agent is working on the page (true), or stopped a moment ago (false). */
  awake(on: boolean): Promise<void>;
}

export interface PageHooks {
  /** The page's address, title or loading state changed. */
  changed(): void;
  /** Who owns the page now, and so where it may go. */
  who(): Who;
  /** An agent's page was kept from going to url, which needs the person's word. */
  blocked(url: string): void;
  /** Something the agent should hear with its next answer. */
  note(text: string): void;
}

export interface PageFactory {
  create(tab: { id: string; deck: string; owner: BrowserOwner | null }, hooks: PageHooks): BrowserPage | null;
}

/** An op of an agent, as the engine sends it (deck.v1.BrowserRequest). */
export interface AgentRequest {
  id: string;
  agent: string;
  name: string;
  kind: string;
  op: string;
  args: string;
  free: boolean;
  allowed: string[];
  timeout_ms: number;
}

export interface AgentResult {
  text: string;
  isError: boolean;
  image?: Uint8Array;
  mime?: string;
}

/** Said to the agent as it is. */
export class OpError extends Error {}

export const HEAVY_AT_ONCE = 2;
export const SWEEP_MS = 5000;
const MAX_ALLOWED = 50;

class Gate {
  private free: number;
  private readonly queue: (() => void)[] = [];

  constructor(n: number) {
    this.free = n;
  }

  async acquire(): Promise<() => void> {
    if (this.free > 0) this.free--;
    else await new Promise<void>((r) => this.queue.push(r));
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

const host = (url: string): string => {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
};

const clip = (s: string, n: number): string => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

export interface BrowserDeps {
  factory: PageFactory;
  /** Tells the window how the tabs stand. */
  send: (s: BrowserState) => void;
  now?: () => number;
  limits?: Limits;
}

interface Controlled {
  page: BrowserPage;
  control: PageControl;
}

export class Browser {
  readonly tabs: TabManager<BrowserPage>;
  private readonly now: () => number;
  private readonly heavy = new Gate(HEAVY_AT_ONCE);
  private readonly queues = new Map<string, Promise<unknown>>();
  private readonly controls = new Map<string, Controlled>();
  private readonly blockedAt = new Map<string, string>();
  private readonly notes = new Map<string, string[]>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private sweeper: ReturnType<typeof setInterval> | null = null;

  constructor(private readonly d: BrowserDeps) {
    this.now = d.now ?? Date.now;
    this.tabs = new TabManager<BrowserPage>(
      (tab) => d.factory.create({ id: tab.id, deck: tab.deck, owner: tab.owner }, this.hooks(tab.id)),
      () => this.notify(),
      this.now,
      d.limits ?? LIMITS,
    );
  }

  /** Starts letting go of idle pages; stop() ends it. */
  start(): void {
    this.sweeper ??= setInterval(() => this.sweep(), SWEEP_MS);
    this.sweeper.unref?.();
  }

  stop(): void {
    if (this.sweeper !== null) clearInterval(this.sweeper);
    this.sweeper = null;
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
  }

  /** Discards idle pages, and clears what an agent did a moment ago. */
  sweep(): string[] {
    const gone = this.tabs.sweep();
    for (const id of gone) this.controls.delete(id);
    this.tabs.settle();
    return gone;
  }

  private hooks(id: string): PageHooks {
    return {
      changed: () => {
        this.sync(id);
        this.notify();
      },
      who: () => {
        const tab = this.tabs.get(id);
        if (tab === undefined || tab.owner === null) return { kind: "person" };
        return { kind: "agent", at: tab.page?.info().url || tab.url, allowed: tab.allowed, free: tab.free };
      },
      blocked: (url) => this.blockedAt.set(id, url),
      note: (text) => this.notes.set(id, [...(this.notes.get(id) ?? []), text].slice(-3)),
    };
  }

  /** Copies the live page's address and title to the tab, which keeps them when the page goes. */
  private sync(id: string): void {
    const tab = this.tabs.get(id);
    const info = tab?.page?.info();
    if (tab === undefined || info === undefined) return;
    if (info.url !== "" && info.url !== "about:blank") tab.url = info.url;
    if (info.title !== "") tab.title = info.title;
  }

  private notify(): void {
    if (this.timer !== null) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      this.d.send(this.state());
    }, 30);
  }

  state(): BrowserState {
    const tabs: BrowserTab[] = this.tabs.list().map((t) => {
      const info = t.page?.info();
      return {
        id: t.id,
        deck: t.deck,
        owner: t.owner,
        url: info !== undefined && info.url !== "" ? info.url : t.url,
        title: info !== undefined && info.title !== "" ? info.title : t.title,
        loading: info?.loading ?? false,
        canBack: info?.canBack ?? false,
        canForward: info?.canForward ?? false,
        live: t.page !== null,
        doing: t.doing,
        paused: t.paused,
      };
    });
    return { tabs, shown: this.tabs.shown };
  }

  // ---- the person ----

  /** Opens url as the person: in tab, else in the person's latest tab of deck. */
  open(deck: string, raw: string, tabId?: string, fresh = false): BrowserOpen {
    const u = parseWebUrl(raw);
    if (u === null || judgeUrl(u.href, { kind: "person" }) !== "load") return { ok: false, message: "Deck shows web pages (http and https) only." };
    try {
      let tab = tabId === undefined ? undefined : this.tabs.get(tabId);
      if (tabId !== undefined && (tab === undefined || tab.deck !== deck)) return { ok: false, message: "That tab is gone." };
      if (!fresh) {
        tab ??= this.tabs
          .list(deck)
          .filter((t) => t.owner === null)
          .sort((a, b) => b.lastUsed - a.lastUsed)[0];
      }
      if (tab === undefined) {
        tab = this.tabs.open(deck, null, u.href).tab;
      } else {
        tab.url = u.href;
        this.tabs.touch(tab);
      }
      const page = this.tabs.live(tab);
      if (page === null) return { ok: false, message: "Deck has no window to show the page in." };
      this.controlOf(tab, page);
      void page.navigate(u.href).catch(() => {});
      return { ok: true, tab: tab.id };
    } catch (e) {
      if (e instanceof TabLimitError) return { ok: false, message: e.message };
      throw e;
    }
  }

  nav(tabId: string, n: BrowserNav): void {
    const tab = this.tabs.get(tabId);
    if (tab === undefined) return;
    this.tabs.touch(tab);
    if (n.action === "go") {
      this.open(tab.deck, n.url, tabId);
      return;
    }
    const page = this.loaded(tab);
    void page?.history(n.action).catch(() => {});
  }

  /** The tab's page, made again (and loading its address) if it was discarded. */
  private loaded(tab: Tab<BrowserPage>): BrowserPage | null {
    const page = this.tabs.live(tab);
    if (page === null) return null;
    this.controlOf(tab, page);
    if (page.info().url === "" && tab.url !== "") void page.navigate(tab.url).catch(() => {});
    return page;
  }

  show(tabId: string, b: BrowserBounds): void {
    const tab = this.tabs.get(tabId);
    if (tab === undefined) return;
    this.tabs.show(tabId);
    const page = this.loaded(tab);
    page?.place(b);
  }

  hide(): void {
    const prev = this.tabs.get(this.tabs.shown ?? "");
    this.tabs.show(null);
    prev?.page?.place(null);
  }

  close(tabId: string): void {
    this.tabs.get(tabId)?.page?.place(null);
    this.tabs.close(tabId);
    this.controls.delete(tabId);
    this.queues.delete(tabId);
    this.blockedAt.delete(tabId);
    this.notes.delete(tabId);
  }

  /** The person took the tab over (paused), or gave it back. */
  pause(tabId: string, paused: boolean): void {
    const tab = this.tabs.get(tabId);
    if (tab === undefined || tab.owner === null) return;
    tab.paused = paused;
    this.notify();
  }

  /** A Deck closed: its tabs go. */
  closeDeck(deck: string): void {
    for (const t of this.tabs.list(deck)) this.close(t.id);
  }

  // ---- agents ----

  private controlOf(tab: Tab<BrowserPage>, page: BrowserPage): Controlled {
    let c = this.controls.get(tab.id);
    if (c === undefined || c.page !== page) {
      c = { page, control: new PageControl(page) };
      this.controls.set(tab.id, c);
    }
    return c;
  }

  /** Runs one op of an agent and answers it. */
  async handle(deck: string, r: AgentRequest): Promise<AgentResult> {
    let args: Record<string, unknown>;
    try {
      const v: unknown = JSON.parse(r.args === "" ? "{}" : r.args);
      if (typeof v !== "object" || v === null || Array.isArray(v)) throw new Error("not an object");
      args = v as Record<string, unknown>;
    } catch {
      return { text: "The op's arguments are not a JSON object.", isError: true };
    }
    const owner: BrowserOwner = { agent: r.agent, name: r.name, kind: r.kind };
    const limit = Math.min(60_000, Math.max(1000, r.timeout_ms || 30_000));
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const run = this.run(deck, owner, r, args);
      // The slower of a hung page and the op: the agent hears of it either way.
      const timeout = new Promise<never>((_, no) => {
        timer = setTimeout(() => no(new OpError(`browser.${r.op} timed out after ${Math.round(limit / 1000)}s; the page is busy. Try browser.snapshot, browser.reload or browser.close.`)), limit);
      });
      run.catch(() => {});
      return await Promise.race([run, timeout]);
    } catch (e) {
      return { text: e instanceof OpError || e instanceof RefError ? e.message : `browser.${r.op} failed: ${e instanceof Error ? e.message : String(e)}`, isError: true };
    } finally {
      clearTimeout(timer);
    }
  }

  private async run(deck: string, owner: BrowserOwner, r: AgentRequest, a: Record<string, unknown>): Promise<AgentResult> {
    const ok = (text: string): AgentResult => ({ text, isError: false });
    const str = (k: string): string => (typeof a[k] === "string" ? (a[k] as string) : "");
    const num = (k: string): number | undefined => (typeof a[k] === "number" ? (a[k] as number) : undefined);
    const flag = (k: string): boolean => a[k] === true;

    if (r.op === "tabs") return ok(this.listTabs(deck, owner));

    let tab: Tab<BrowserPage>;
    if (r.op === "open") {
      tab = this.tabForOpen(deck, owner, str("tab"), flag("new_tab"), str("url"), r);
    } else {
      tab = this.ownTab(deck, owner, str("tab"));
    }
    tab.free = r.free;
    tab.allowed = [...new Set([...tab.allowed, ...r.allowed])].slice(-MAX_ALLOWED);

    const what: Record<string, string> = {
      open: `opening ${host(str("url"))}`,
      snapshot: "reading the page",
      click: `clicking ${str("ref")}`,
      type: `typing in ${str("ref")}`,
      press: `pressing ${str("key")}`,
      scroll: "scrolling",
      select: `choosing in ${str("ref")}`,
      wait: "waiting",
      back: "going back",
      forward: "going forward",
      reload: "reloading",
      eval: "running a script",
      screenshot: "taking a screenshot",
      close: "closing",
    };
    const heavy = r.op === "snapshot" || r.op === "screenshot";
    return this.serial(tab, async () => {
      if (tab.paused) throw new OpError(this.pausedText(tab));
      this.tabs.begin(tab, what[r.op] ?? r.op);
      const release = heavy ? await this.heavy.acquire() : null;
      try {
        const out = await this.exec(tab, r, { str, num, flag });
        return { ...out, text: this.withNotes(tab, out.text) };
      } finally {
        release?.();
        void tab.page?.awake(false);
        this.tabs.end(tab);
        // What the agent did shows for a moment, then goes.
        setTimeout(() => this.tabs.settle(), DOING_MS + 50).unref?.();
      }
    });
  }

  private pausedText(tab: Tab<BrowserPage>): string {
    return `The user took over ${tab.id}; it is theirs until they hand it back. Ask them, or work in another tab (browser.open {url, new_tab: true}).`;
  }

  /** What the page did on its own since the last answer. */
  private withNotes(tab: Tab<BrowserPage>, text: string): string {
    const out = [text];
    const blocked = this.blockedAt.get(tab.id);
    if (blocked !== undefined) {
      this.blockedAt.delete(tab.id);
      out.push(`note: the page tried to go to ${clip(blocked, 200)}, a site the user has not allowed. Call browser.open {url} to ask them.`);
    }
    for (const n of this.notes.get(tab.id) ?? []) out.push(`note: ${n}`);
    this.notes.delete(tab.id);
    return out.join("\n");
  }

  private serial<T>(tab: Tab<BrowserPage>, f: () => Promise<T>): Promise<T> {
    const prev = this.queues.get(tab.id) ?? Promise.resolve();
    const next = prev.then(f, f);
    const tail = next.catch(() => {});
    this.queues.set(tab.id, tail);
    void tail.then(() => {
      if (this.queues.get(tab.id) === tail) this.queues.delete(tab.id);
    });
    return next;
  }

  private listTabs(deck: string, me: BrowserOwner): string {
    const all = this.tabs.list(deck);
    if (all.length === 0) return "No tabs are open. browser.open {url} opens one for you.";
    return all
      .map((t) => {
        const who = t.owner === null ? "user" : t.owner.agent === me.agent ? "you" : `@${t.owner.name}`;
        const url = t.page?.info().url || t.url;
        return `${t.id} (${who}${t.paused ? ", taken over" : ""}${t.page === null ? ", asleep" : ""}) ${clip(t.title || "(untitled)", 60)}  ${clip(url, 120)}`;
      })
      .join("\n");
  }

  private ownTab(deck: string, owner: BrowserOwner, id: string): Tab<BrowserPage> {
    if (id !== "") {
      const t = this.tabs.get(id);
      if (t === undefined || t.deck !== deck) throw new OpError(`There is no tab ${id}. browser.tabs lists them.`);
      if (t.owner?.agent !== owner.agent) {
        throw new OpError(`${id} is ${t.owner === null ? "the user's" : `@${t.owner.name}'s`} tab; you can act only on your own. browser.open {url, new_tab: true} opens one for you.`);
      }
      return t;
    }
    const t = this.tabs.latestOf(deck, owner.agent);
    if (t === undefined) throw new OpError("You have no tab open. Call browser.open {url} first.");
    return t;
  }

  private tabForOpen(deck: string, owner: BrowserOwner, id: string, fresh: boolean, url: string, r: AgentRequest): Tab<BrowserPage> {
    const u = parseWebUrl(url);
    if (u === null) throw new OpError("Only http and https pages can be opened.");
    const mine = this.ownTab0(deck, owner, id);
    const target = mine === null || fresh ? null : mine;
    const who: Who = { kind: "agent", at: target?.url ?? "", allowed: r.allowed, free: r.free };
    // The engine asked the person already; this is the browser's own check.
    if (judgeUrl(u.href, who) === "ask" && !r.allowed.includes(u.origin)) {
      throw new OpError(`${u.origin} is not a site the user has allowed for you.`);
    }
    if (target !== null) {
      if (target.paused) throw new OpError(this.pausedText(target));
      target.url = u.href;
      target.allowed = [...new Set([...target.allowed, u.origin])];
      return target;
    }
    try {
      const { tab, closed } = this.tabs.open(deck, owner, u.href);
      tab.allowed = [u.origin];
      for (const c of closed) {
        this.controls.delete(c.id);
        this.queues.delete(c.id);
      }
      if (closed.length > 0) this.notes.set(tab.id, [`you were at the limit of ${this.tabs.limits.perAgent} tabs; ${closed.map((c) => c.id).join(", ")} was closed to make room.`]);
      return tab;
    } catch (e) {
      if (e instanceof TabLimitError) throw new OpError(e.message);
      throw e;
    }
  }

  /** The agent's tab for open: the one named, else its latest; null if it has none. */
  private ownTab0(deck: string, owner: BrowserOwner, id: string): Tab<BrowserPage> | null {
    if (id !== "") return this.ownTab(deck, owner, id);
    return this.tabs.latestOf(deck, owner.agent) ?? null;
  }

  /** The tab's page with its refs, loading the address first if the page is new. */
  private async ready(tab: Tab<BrowserPage>): Promise<{ page: BrowserPage; control: PageControl; restored: boolean }> {
    const page = this.tabs.live(tab);
    if (page === null) throw new OpError("Deck's window is closed; open it again.");
    const { control } = this.controlOf(tab, page);
    let restored = false;
    if (page.info().url === "" && tab.url !== "") {
      restored = true;
      const r = await page.navigate(tab.url);
      if (!r.ok) throw new OpError(`could not load ${tab.url}: ${r.error ?? "failed"}`);
    }
    return { page, control, restored };
  }

  private where(page: BrowserPage): string {
    const i = page.info();
    return `${i.title === "" ? "(untitled)" : clip(i.title, 80)}  ${i.url}`;
  }

  private async exec(
    tab: Tab<BrowserPage>,
    r: AgentRequest,
    g: { str: (k: string) => string; num: (k: string) => number | undefined; flag: (k: string) => boolean },
  ): Promise<AgentResult> {
    const ok = (text: string): AgentResult => ({ text, isError: false });
    if (r.op === "close") {
      const id = tab.id;
      this.close(id);
      return ok(`closed ${id}.`);
    }
    const { page, control, restored } = await this.ready(tab);
    this.tabs.touch(tab);
    await page.awake(true);
    switch (r.op) {
      case "open": {
        if (!restored) {
          const nav = await page.navigate(tab.url);
          if (!nav.ok) throw new OpError(`could not load ${tab.url}: ${nav.error ?? "failed"}`);
        }
        const loaded = page.info().loading ? " (still loading)" : "";
        return ok(`opened ${tab.id}: ${this.where(page)}${loaded}\nCall browser.snapshot to see it.`);
      }
      case "snapshot": {
        const max = g.num("max");
        return ok(await control.snapshot(tab.id, { interactive: g.flag("interactive"), ...(max === undefined ? {} : { max }) }));
      }
      case "click":
        return ok(await control.click(g.str("ref"), g.flag("double")));
      case "type":
        return ok(await control.type(g.str("ref"), g.str("text"), g.flag("submit"), g.flag("append")));
      case "press":
        return ok(await control.press(g.str("key")));
      case "scroll": {
        const dy = g.num("dy");
        return ok(await control.scroll({ ...(dy === undefined ? {} : { dy }), to: g.str("to"), ref: g.str("ref") }));
      }
      case "select":
        return ok(await control.select(g.str("ref"), g.str("value")));
      case "wait": {
        const ms = g.num("ms");
        const timeout = g.num("timeout");
        return ok(await control.wait({ text: g.str("text"), load: g.flag("load"), ...(ms === undefined ? {} : { ms }), ...(timeout === undefined ? {} : { timeout }) }));
      }
      case "back":
      case "forward":
      case "reload": {
        const moved = await page.history(r.op);
        if (!moved) throw new OpError(r.op === "back" ? "There is no earlier page in this tab." : "There is no later page in this tab.");
        await page.whenLoaded(10_000);
        return ok(`${r.op}: ${this.where(page)}`);
      }
      case "eval":
        return ok(await control.evaluate(g.str("js")));
      case "screenshot": {
        const shot = await control.screenshot(g.str("ref"));
        return { text: `screenshot of ${tab.id} (${shot.width}x${shot.height}, ${Math.round(shot.data.length / 1024)} KB)`, isError: false, image: shot.data, mime: shot.mime };
      }
      default:
        throw new OpError(`browser.${r.op} is not an op this browser knows.`);
    }
  }
}
