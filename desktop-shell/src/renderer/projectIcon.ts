import { useEffect, useSyncExternalStore } from "react";
import type { DeckBridge } from "../shared/bridge";

// A project's tile, wherever a project shows (the sidebar, the project
// menu, the recent folders, the inbox, the notifications): the repo's own
// logo or favicon when it has one, else a monogram on a hue that is the
// project's own. The tile is generated from the project's source, so it is
// the same in every place and every run, and two folders of one name still
// differ.

// FNV-1a, 32 bits: small, stable and well spread.
export function hash(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

// 24 hues, 15° apart: near ones are still told apart at a glance.
export const HUES = 24;

export function projectHue(seed: string): number {
  return (hash(seed) % HUES) * (360 / HUES);
}

// The name's words: "my-app" and "myApp" are both my, app.
function words(name: string): string[] {
  return name
    .replace(/([\p{Ll}\d])(\p{Lu})/gu, "$1 $2")
    .split(/[^\p{L}\p{N}]+/u)
    .filter((w) => w !== "");
}

// One or two letters for the tile: the initials of the first two words,
// or the first letter of a name of one word. "#" for a name with none.
export function monogram(name: string, letters: 1 | 2 = 2): string {
  const ws = words(name);
  const first = (w: string | undefined) => (w === undefined ? "" : (Array.from(w)[0] ?? ""));
  const out = letters === 1 || ws.length < 2 ? first(ws[0]) : first(ws[0]) + first(ws[1]);
  return out === "" ? "#" : out.toLocaleUpperCase();
}

export interface Tile {
  hue: number;
  glyph: string;
  // A paired Deck has no folder: its tile shows a computer, not letters.
  paired: boolean;
}

export function projectTile(source: string, name: string, letters: 1 | 2 = 2): Tile {
  return { hue: projectHue(source), glyph: monogram(name, letters), paired: source.startsWith("paired:") };
}

// ---- the repos' own icons -------------------------------------------------

// Each folder's icon as main found it: a data: URL, null for none, or
// undefined while it is asked for.
const logos = new Map<string, string | null>();
const asked = new Set<string>();
const listeners = new Set<() => void>();
let bridge: Pick<DeckBridge, "projectIcon"> | null = null;

export function startProjectLogos(b: Pick<DeckBridge, "projectIcon">): void {
  bridge = b;
}

function ask(dir: string): void {
  if (asked.has(dir) || bridge === null) return;
  asked.add(dir);
  void bridge
    .projectIcon(dir)
    .catch(() => null)
    .then((url) => {
      logos.set(dir, typeof url === "string" && url.startsWith("data:image/") ? url : null);
      for (const l of listeners) l();
    });
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

// The logo of a folder project, or null (a paired Deck, a folder with none,
// or one still being looked for).
export function useProjectLogo(source: string): string | null {
  const dir = source.startsWith("folder:") ? source.slice("folder:".length) : null;
  useEffect(() => {
    if (dir !== null) ask(dir);
  }, [dir]);
  return useSyncExternalStore(subscribe, () => (dir === null ? null : (logos.get(dir) ?? null)));
}
