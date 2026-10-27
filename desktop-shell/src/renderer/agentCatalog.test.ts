import type { ModelChoice } from "@web/protocol";
import { describe, expect, test } from "vitest";
import {
  type Catalog,
  CatalogStore,
  currentModel,
  type DeckSignals,
  fetchReason,
  type KV,
  type ListModels,
  loadCatalog,
  modelRows,
  normalizeEfforts,
  normalizeModels,
  readCatalog,
} from "./agentCatalog";

// Each CLI's list as the Deck hands it over (internal/deck/agent/*/models.go).
const CLAUDE: ModelChoice[] = [
  { id: "", label: "Default (recommended)", efforts: ["low", "medium", "high", "xhigh", "max"], selected: true },
  { id: "opus", label: "Opus", efforts: ["max", "low", "high"] },
  { id: "haiku", label: "Haiku" },
];
const CODEX: ModelChoice[] = [
  { id: "gpt-5-codex", label: "GPT-5 Codex", efforts: ["low", "medium", "high"] },
  { id: "gpt-5", label: "gpt-5", efforts: ["minimal", "low", "medium", "high"], selected: true },
];
const OPENCODE: ModelChoice[] = [{ id: "anthropic/sonnet", label: "Sonnet", efforts: ["High", "none", "high", "default"] }];
const FAKE: ModelChoice[] = [
  { id: "default", label: "Default", selected: true },
  { id: "fast", label: "Fast" },
];

describe("normalizeModels", () => {
  test("every CLI gets one shape: the default first as \"\", then each model once", () => {
    expect(normalizeModels(CLAUDE)).toEqual([
      { id: "", label: "Default", efforts: ["low", "medium", "high", "xhigh", "max"] },
      { id: "opus", label: "Opus", efforts: ["low", "high", "max"] },
      { id: "haiku", label: "Haiku", efforts: [] },
    ]);
    // Codex names a real model as its default: the default row is still first.
    expect(normalizeModels(CODEX).map((m) => m.id)).toEqual(["", "gpt-5-codex", "gpt-5"]);
    expect(normalizeModels(FAKE)).toEqual([
      { id: "", label: "Default", efforts: [] },
      { id: "fast", label: "Fast", efforts: [] },
    ]);
    expect(normalizeModels(null)).toEqual([{ id: "", label: "Default", efforts: [] }]);
  });

  test("a thread's selection is not part of the list", () => {
    expect(normalizeModels(CODEX).some((m) => "selected" in m)).toBe(false);
  });

  test("duplicate models and blank labels", () => {
    expect(normalizeModels([{ id: "a", label: " " }, { id: "a", label: "Again" }, { id: " b ", label: "B" }])).toEqual([
      { id: "", label: "Default", efforts: [] },
      { id: "a", label: "a", efforts: [] },
      { id: "b", label: "B", efforts: [] },
    ]);
  });

  test("efforts: lowercase, once, lightest first, without a 'default' level", () => {
    expect(normalizeEfforts(OPENCODE[0]?.efforts)).toEqual(["none", "high"]);
    expect(normalizeEfforts(["max", "turbo", "low"])).toEqual(["low", "max", "turbo"]);
    expect(normalizeEfforts(undefined)).toEqual([]);
  });
});

describe("modelRows", () => {
  const models = normalizeModels(CLAUDE);
  test("an agent on its CLI's default checks the default row", () => {
    expect(currentModel(undefined)).toBe("");
    expect(currentModel("default")).toBe("");
    expect(currentModel("opus")).toBe("opus");
    expect(modelRows(models, undefined)).toEqual(models);
  });
  test("a model the CLI did not list is shown after the list", () => {
    expect(modelRows(models, "claude-custom").at(-1)).toEqual({ id: "claude-custom", label: "claude-custom", efforts: [] });
  });
  test("before any listing there is the default", () => {
    expect(modelRows([], "").map((m) => m.id)).toEqual([""]);
  });
});

const sig = (over: Partial<DeckSignals> = {}): DeckSignals => ({ deck: "d1", version: "1.5.0", kinds: ["claude", "codex"], ...over });
const kept = (over: Partial<Catalog> = {}): Catalog => ({ deck: "d1", version: "1.5.0", kinds: ["codex", "claude"], at: 1, agents: [], ...over });

describe("fetchReason: when the pickers ask the Deck", () => {
  test("nothing kept, or kept for another Deck", () => {
    expect(fetchReason(null, sig(), false)).toBe("missing");
    expect(fetchReason(kept({ deck: "d2" }), sig(), true)).toBe("missing");
  });
  test("the Deck was upgraded, or runs other agent CLIs", () => {
    expect(fetchReason(kept({ version: "1.4.0" }), sig(), true)).toBe("version");
    expect(fetchReason(kept(), sig({ kinds: ["claude", "codex", "opencode"] }), true)).toBe("kinds");
  });
  test("kept from an earlier launch: listed once more in this one", () => {
    expect(fetchReason(kept(), sig(), false)).toBe("launch");
  });
  test("listed in this run: never again by itself, whatever the order of the kinds", () => {
    expect(fetchReason(kept(), sig(), true)).toBeNull();
    expect(fetchReason(kept(), sig({ kinds: ["codex", "claude"] }), true)).toBeNull();
  });
});

// A Deck that answers each kind from lists, counting the calls.
function deck(lists: Record<string, ModelChoice[] | null>) {
  const calls: Array<{ kind: string; refresh: boolean }> = [];
  const list: ListModels = async (kind, refresh) => {
    calls.push({ kind, refresh });
    return lists[kind] ?? null;
  };
  return { calls, list };
}

describe("loadCatalog", () => {
  test("lists every kind in one pass", async () => {
    const d = deck({ claude: CLAUDE, codex: CODEX });
    const c = await loadCatalog(sig(), d.list, { now: 5 });
    expect(d.calls).toEqual([
      { kind: "claude", refresh: false },
      { kind: "codex", refresh: false },
    ]);
    expect(c).toMatchObject({ deck: "d1", version: "1.5.0", kinds: ["claude", "codex"], at: 5 });
    expect(c.agents.map((a) => [a.kind, a.models.length, a.failed])).toEqual([
      ["claude", 3, false],
      ["codex", 3, false],
    ]);
  });

  test("a refresh asks the Deck to forget its lists once, on the first kind", async () => {
    const d = deck({ claude: CLAUDE, codex: CODEX });
    await loadCatalog(sig(), d.list, { refresh: true });
    expect(d.calls).toEqual([
      { kind: "claude", refresh: true },
      { kind: "codex", refresh: false },
    ]);
  });

  test("a kind the Deck cannot list keeps what was known, else is marked failed", async () => {
    const prev = await loadCatalog(sig(), deck({ claude: CLAUDE, codex: CODEX }).list);
    const again = await loadCatalog(sig(), deck({ claude: CLAUDE, codex: null }).list, { prev });
    expect(again.agents[1]).toEqual(prev.agents[1]);
    const fresh = await loadCatalog(sig(), deck({ claude: CLAUDE }).list);
    expect(fresh.agents[1]).toEqual({ kind: "codex", models: [{ id: "", label: "Default", efforts: [] }], failed: true });
  });
});

function memoryKV(): KV & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v) };
}

const settle = () => new Promise((r) => setTimeout(r, 0));

describe("CatalogStore", () => {
  test("lists once, then pickers and thread switches ask nothing", async () => {
    const kv = memoryKV();
    const store = new CatalogStore(kv, () => 7);
    const d = deck({ claude: CLAUDE, codex: CODEX });
    expect(store.ensure(sig(), d.list)).toBe("missing");
    expect(store.view("d1").loading).toBe(true);
    // A second picker while the first listing runs joins it.
    expect(store.ensure(sig(), d.list)).toBeNull();
    await settle();
    expect(store.view("d1")).toMatchObject({ loading: false, catalog: { at: 7 } });
    expect(store.ensure(sig(), d.list)).toBeNull();
    expect(store.ensure(sig({ kinds: ["codex", "claude"] }), d.list)).toBeNull();
    expect(d.calls).toHaveLength(2);
    // Kept for the next launch.
    expect(readCatalog(kv, "d1")?.agents.map((a) => a.kind)).toEqual(["claude", "codex"]);
  });

  test("the next launch shows the kept catalog at once and lists it again once", async () => {
    const kv = memoryKV();
    const first = new CatalogStore(kv);
    first.ensure(sig(), deck({ claude: CLAUDE, codex: CODEX }).list);
    await settle();

    const next = new CatalogStore(kv);
    expect(next.view("d1").catalog?.agents).toHaveLength(2);
    const d = deck({ claude: CLAUDE, codex: CODEX });
    expect(next.ensure(sig(), d.list)).toBe("launch");
    // The kept catalog stays on show while it is listed.
    expect(next.view("d1").catalog?.agents).toHaveLength(2);
    await settle();
    expect(next.ensure(sig(), d.list)).toBeNull();
    expect(d.calls).toHaveLength(2);
  });

  test("another Deck, an upgrade or another set of kinds lists again", async () => {
    const store = new CatalogStore(memoryKV());
    const d = deck({ claude: CLAUDE, codex: CODEX, opencode: OPENCODE });
    store.ensure(sig(), d.list);
    await settle();
    expect(store.ensure(sig({ deck: "d2" }), d.list)).toBe("missing");
    await settle();
    expect(store.ensure(sig({ version: "1.6.0" }), d.list)).toBe("version");
    await settle();
    expect(store.ensure(sig({ version: "1.6.0", kinds: ["claude", "codex", "opencode"] }), d.list)).toBe("kinds");
    await settle();
    // Back to the first Deck: its catalog is kept apart, and still current.
    expect(store.view("d2").catalog?.deck).toBe("d2");
    expect(d.calls).toHaveLength(2 + 2 + 2 + 3);
  });

  test("Refresh lists again by hand and tells subscribers", async () => {
    const store = new CatalogStore(memoryKV());
    const d = deck({ claude: CLAUDE, codex: CODEX });
    store.ensure(sig(), d.list);
    await settle();
    let heard = 0;
    const off = store.subscribe(() => heard++);
    await store.refresh(sig(), d.list);
    off();
    expect(d.calls.slice(2)).toEqual([
      { kind: "claude", refresh: true },
      { kind: "codex", refresh: false },
    ]);
    expect(heard).toBe(2); // loading, then listed
  });

  test("a storage that throws keeps the catalog in memory", async () => {
    const broken: KV = {
      getItem: () => {
        throw new Error("locked");
      },
      setItem: () => {
        throw new Error("full");
      },
    };
    const store = new CatalogStore(broken);
    store.ensure(sig(), deck({ claude: CLAUDE, codex: CODEX }).list);
    await settle();
    expect(store.view("d1").catalog?.agents).toHaveLength(2);
  });

  test("a kept catalog that does not read back is ignored", () => {
    const kv = memoryKV();
    kv.setItem("deck.desktop.agents:d1", "{not json");
    expect(readCatalog(kv, "d1")).toBeNull();
    kv.setItem("deck.desktop.agents:d1", JSON.stringify({ deck: "other" }));
    expect(readCatalog(kv, "d1")).toBeNull();
  });
});
