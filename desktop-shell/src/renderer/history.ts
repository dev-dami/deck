// What the person sent in each room, newest first, for ↑ in an empty
// composer. Like the drafts (drafts.ts), it lives in this window's
// localStorage, per project source and room.

const PREFIX = "deck.desktop.history:";
export const HISTORY_MAX = 50;

function keyOf(project: string, room: string): string {
  return PREFIX + JSON.stringify([project, room]);
}

export function loadHistory(project: string, room: string): string[] {
  try {
    const v: unknown = JSON.parse(window.localStorage.getItem(keyOf(project, room)) ?? "[]");
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string").slice(0, HISTORY_MAX) : [];
  } catch {
    return [];
  }
}

// The list with text in front. The same text twice in a row is kept once.
export function remember(list: readonly string[], text: string): string[] {
  if (text.trim() === "") return [...list];
  return (list[0] === text ? [...list] : [text, ...list]).slice(0, HISTORY_MAX);
}

export function pushHistory(project: string, room: string, text: string): void {
  try {
    window.localStorage.setItem(keyOf(project, room), JSON.stringify(remember(loadHistory(project, room), text)));
  } catch {
    // A private, full or locked storage keeps no history.
  }
}

// One step through the history. at is the entry the composer shows, -1 for
// none. Up goes to older entries, down to newer ones and past the newest
// back to an empty composer. null: there is nowhere to go.
export function walk(list: readonly string[], at: number, dir: "up" | "down"): { at: number; text: string } | null {
  if (dir === "up") {
    const next = list[at + 1];
    return next === undefined ? null : { at: at + 1, text: next };
  }
  if (at < 0) return null;
  return { at: at - 1, text: at - 1 < 0 ? "" : (list[at - 1] ?? "") };
}
