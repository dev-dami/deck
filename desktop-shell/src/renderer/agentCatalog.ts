import type { ModelChoice } from "@web/protocol";
import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";

// What every agent picker offers: each agent kind the Deck runs, its models
// and each model's reasoning efforts, in one shape whatever the CLI. The
// CLIs answer differently (Claude names its default model "" or "default"
// and gives efforts only to some models; Codex, OpenCode and Antigravity
// name a real model as their default and may list efforts in any order),
// so normalizeModels makes one list of them: the CLI's default first, as
// "", then each model once.
//
// The lists are loaded once, all kinds in one pass, and kept: in memory for
// the window's life and in localStorage per Deck, so the next launch draws
// the pickers at once. Opening a picker or switching threads never asks the
// Deck again. fetchReason is the whole rule for when it does.

export interface PickModel {
  // "" is the CLI's own default.
  id: string;
  label: string;
  // Lowercase, each once, lightest first; [] when the model takes none.
  efforts: string[];
}

export interface AgentModels {
  kind: string;
  models: PickModel[];
  // The Deck could not list them (and none were known): the picker offers
  // the default and Refresh.
  failed: boolean;
}

export interface Catalog {
  // The Deck it came from (ServerInfo.server_id), its version and kinds.
  deck: string;
  version: string;
  kinds: string[];
  // When it was listed, Unix ms.
  at: number;
  agents: AgentModels[];
}

export const DEFAULT_LABEL = "Default";

// Efforts in the order the pickers show them; others follow as listed.
const EFFORT_ORDER = ["none", "minimal", "low", "medium", "high", "xhigh", "max"];

export function normalizeEfforts(raw: readonly string[] | undefined): string[] {
  const out: string[] = [];
  for (const e of raw ?? []) {
    const v = e.trim().toLowerCase();
    if (v !== "" && v !== "default" && !out.includes(v)) out.push(v);
  }
  const rank = (e: string) => {
    const i = EFFORT_ORDER.indexOf(e);
    return i < 0 ? EFFORT_ORDER.length : i;
  };
  return out.map((e, i) => ({ e, i })).sort((a, b) => rank(a.e) - rank(b.e) || a.i - b.i).map((x) => x.e);
}

const isDefaultId = (id: string) => id === "" || id === "default";

// One list for every CLI: the default row first (id "", labelled Default,
// with the efforts of the CLI's own default entry when it lists one), then
// each other model once, in the CLI's order. Which model a thread runs
// ("selected") belongs to the thread, not the list, and is dropped.
export function normalizeModels(raw: readonly ModelChoice[] | null | undefined): PickModel[] {
  const def: PickModel = { id: "", label: DEFAULT_LABEL, efforts: [] };
  const out: PickModel[] = [def];
  for (const m of raw ?? []) {
    const id = m.id.trim();
    if (isDefaultId(id)) {
      if (def.efforts.length === 0) def.efforts = normalizeEfforts(m.efforts);
      continue;
    }
    if (out.some((o) => o.id === id)) continue;
    out.push({ id, label: m.label.trim() || id, efforts: normalizeEfforts(m.efforts) });
  }
  return out;
}

// The model id a picker marks for an agent that runs model: "" (the
// default) for none.
export function currentModel(model: string | undefined): string {
  const id = (model ?? "").trim();
  return isDefaultId(id) ? "" : id;
}

// The rows a picker shows for an agent that runs model: the list, and the
// model itself after it when the CLI did not list it (one set by hand).
export function modelRows(models: readonly PickModel[], model: string | undefined): PickModel[] {
  const id = currentModel(model);
  const rows = models.length === 0 ? normalizeModels([]) : [...models];
  if (!rows.some((m) => m.id === id)) rows.push({ id, label: id, efforts: [] });
  return rows;
}

// ---- when to ask the Deck ----------------------------------------------

export interface DeckSignals {
  deck: string;
  version: string;
  kinds: readonly string[];
}

// Why the catalog must be listed now, or null to keep the one there is:
//   missing  nothing kept for this Deck: the first launch, or a Deck not
//            seen before (connecting to another Deck is this);
//   version  the Deck was upgraded;
//   kinds    the Deck runs other agent CLIs (one was installed or removed);
//   launch   kept from an earlier launch: listed once more, in the
//            background, the first time this window sees the Deck.
// Nothing else asks: not a picker opening, not a thread switch, not a
// reconnect to the same Deck. Refresh asks by hand ("manual").
export type FetchReason = "missing" | "version" | "kinds" | "launch" | "manual";

const sameKinds = (a: readonly string[], b: readonly string[]) => a.length === b.length && [...a].sort().join("\n") === [...b].sort().join("\n");

export function fetchReason(kept: Catalog | null, sig: DeckSignals, listedThisRun: boolean): FetchReason | null {
  if (kept === null || kept.deck !== sig.deck) return "missing";
  if (kept.version !== sig.version) return "version";
  if (!sameKinds(kept.kinds, sig.kinds)) return "kinds";
  if (!listedThisRun) return "launch";
  return null;
}

// ---- listing ------------------------------------------------------------

// Lists one kind's models; refresh asks the Deck to forget the lists it
// keeps first. null when the Deck could not answer.
export type ListModels = (kind: string, refresh: boolean) => Promise<ModelChoice[] | null>;

// Every kind's models in one pass. A kind the Deck cannot list keeps what
// prev knew of it. With refresh, the first kind's call asks the Deck to
// forget its lists (once, for every kind) and the rest follow it.
export async function loadCatalog(sig: DeckSignals, list: ListModels, opts: { refresh?: boolean; prev?: Catalog | null; now?: number } = {}): Promise<Catalog> {
  const kinds = [...sig.kinds];
  const answers: (ModelChoice[] | null)[] = [];
  let rest = kinds;
  if (opts.refresh === true && kinds.length > 0) {
    answers.push(await list(kinds[0] as string, true).catch(() => null));
    rest = kinds.slice(1);
  }
  answers.push(...(await Promise.all(rest.map((k) => list(k, false).catch(() => null)))));
  const agents = kinds.map((kind, i): AgentModels => {
    const got = answers[i] ?? null;
    if (got !== null) return { kind, models: normalizeModels(got), failed: false };
    const known = opts.prev?.deck === sig.deck ? opts.prev.agents.find((a) => a.kind === kind && !a.failed) : undefined;
    return known ?? { kind, models: normalizeModels([]), failed: true };
  });
  return { deck: sig.deck, version: sig.version, kinds, at: opts.now ?? Date.now(), agents };
}

// ---- keeping it ---------------------------------------------------------

export interface KV {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

const PREFIX = "deck.desktop.agents:";

function decodeCatalog(v: unknown, deck: string): Catalog | null {
  if (typeof v !== "object" || v === null) return null;
  const c = v as Partial<Catalog>;
  if (c.deck !== deck || typeof c.version !== "string" || typeof c.at !== "number" || !Array.isArray(c.kinds) || !Array.isArray(c.agents)) return null;
  const agents: AgentModels[] = [];
  for (const a of c.agents as unknown[]) {
    if (typeof a !== "object" || a === null) return null;
    const x = a as Partial<AgentModels>;
    if (typeof x.kind !== "string" || !Array.isArray(x.models)) return null;
    const models = (x.models as unknown[]).filter(
      (m): m is PickModel => typeof m === "object" && m !== null && typeof (m as PickModel).id === "string" && typeof (m as PickModel).label === "string" && Array.isArray((m as PickModel).efforts),
    );
    agents.push({ kind: x.kind, models, failed: x.failed === true });
  }
  return { deck, version: c.version, kinds: c.kinds.filter((k): k is string => typeof k === "string"), at: c.at, agents };
}

export function readCatalog(kv: KV | null, deck: string): Catalog | null {
  try {
    const raw = kv?.getItem(PREFIX + deck);
    return raw === null || raw === undefined ? null : decodeCatalog(JSON.parse(raw), deck);
  } catch {
    return null;
  }
}

export function writeCatalog(kv: KV | null, c: Catalog): void {
  try {
    kv?.setItem(PREFIX + c.deck, JSON.stringify(c));
  } catch {
    // A private, full or locked storage keeps the catalog for this run only.
  }
}

// ---- the store ----------------------------------------------------------

export interface CatalogView {
  catalog: Catalog | null;
  // A listing is under way (the first, or a refresh: the pickers keep
  // showing what they have meanwhile).
  loading: boolean;
}

const EMPTY: CatalogView = { catalog: null, loading: false };

// The catalogs of the Decks this window talks to, by Deck.
export class CatalogStore {
  private readonly views = new Map<string, CatalogView>();
  private readonly listed = new Set<string>();
  private readonly running = new Map<string, Promise<void>>();
  private readonly fns = new Set<() => void>();

  constructor(
    private readonly kv: KV | null,
    private readonly now: () => number = Date.now,
  ) {}

  subscribe = (fn: () => void): (() => void) => {
    this.fns.add(fn);
    return () => this.fns.delete(fn);
  };

  // What the pickers show for deck: kept in memory, else from storage.
  view(deck: string): CatalogView {
    const v = this.views.get(deck);
    if (v !== undefined) return v;
    const kept = readCatalog(this.kv, deck);
    const fresh = kept === null ? EMPTY : { catalog: kept, loading: false };
    this.views.set(deck, fresh);
    return fresh;
  }

  // Lists the catalog when fetchReason says to; the reason, or null.
  ensure(sig: DeckSignals, list: ListModels): FetchReason | null {
    if (this.running.has(sig.deck)) return null;
    const why = fetchReason(this.view(sig.deck).catalog, sig, this.listed.has(sig.deck));
    if (why !== null) void this.load(sig, list, false);
    return why;
  }

  // Lists it again, asking the Deck to ask its CLIs again too.
  refresh(sig: DeckSignals, list: ListModels): Promise<void> {
    return this.running.get(sig.deck) ?? this.load(sig, list, true);
  }

  private load(sig: DeckSignals, list: ListModels, refresh: boolean): Promise<void> {
    const prev = this.view(sig.deck).catalog;
    this.set(sig.deck, { catalog: prev, loading: true });
    const run = loadCatalog(sig, list, { refresh, prev, now: this.now() })
      .then((c) => {
        this.listed.add(sig.deck);
        writeCatalog(this.kv, c);
        this.set(sig.deck, { catalog: c, loading: false });
      })
      .finally(() => {
        this.running.delete(sig.deck);
        const v = this.views.get(sig.deck);
        if (v?.loading === true) this.set(sig.deck, { ...v, loading: false });
      });
    this.running.set(sig.deck, run);
    return run;
  }

  private set(deck: string, v: CatalogView): void {
    this.views.set(deck, v);
    for (const fn of this.fns) fn();
  }
}

function browserKV(): KV | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage;
  } catch {
    return null;
  }
}

export const catalogs = new CatalogStore(browserKV());

// The catalog of the Deck sig names, listed when fetchReason says so, and
// refresh to list it again by hand. list may change (another thread is
// open) without asking again.
export function useAgentCatalog(sig: DeckSignals | null, list: ListModels, store: CatalogStore = catalogs): CatalogView & { refresh: () => void } {
  const deck = sig?.deck ?? "";
  const view = useSyncExternalStore(store.subscribe, () => (deck === "" ? EMPTY : store.view(deck)));
  const listRef = useRef(list);
  listRef.current = list;
  const sigRef = useRef(sig);
  sigRef.current = sig;
  const call: ListModels = useCallback((k, r) => listRef.current(k, r), []);
  // Only the signals ask again: the Deck, its version and its kinds.
  const kinds = sig?.kinds.join("\n") ?? "";
  useEffect(() => {
    if (sigRef.current !== null) store.ensure(sigRef.current, call);
  }, [store, deck, sig?.version, kinds, call]);
  const refresh = useCallback(() => {
    if (sigRef.current !== null) void store.refresh(sigRef.current, call);
  }, [store, call]);
  return { ...view, refresh };
}
