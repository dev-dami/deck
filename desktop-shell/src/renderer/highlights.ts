import { type AgentSummary, HIGHLIGHT_LIMITS, type HighlightItem, type HighlightKind } from "../shared/bridge";

// What the Highlights tab, its pill and the strip above the composer show,
// derived from the items the Deck stores. Pure, so the tests drive it.
// Seeing an item only clears its unread flag; an item leaves "open" only
// when an agent or the person resolves or dismisses it.

export const KIND_ORDER: readonly HighlightKind[] = ["needs_you", "blocker", "finding"];

export const KIND_LABEL: Record<HighlightKind, string> = {
  needs_you: "Needs you",
  blocker: "Blockers",
  finding: "Key findings",
};

export interface HighlightGroup {
  kind: HighlightKind;
  label: string;
  items: HighlightItem[];
}

const isOpen = (i: HighlightItem) => i.state === "open";

// Unread first, then newest update first, so what changed is on top.
const byAttention = (a: HighlightItem, b: HighlightItem) => Number(b.unread) - Number(a.unread) || b.updated_at - a.updated_at;

export function groupHighlights(items: readonly HighlightItem[]): HighlightGroup[] {
  return KIND_ORDER.map((kind) => ({
    kind,
    label: KIND_LABEL[kind],
    items: items.filter((i) => isOpen(i) && i.kind === kind).sort(byAttention),
  })).filter((g) => g.items.length > 0);
}

// Resolved and dismissed items, newest first: the tab's History.
export function closedItems(items: readonly HighlightItem[]): HighlightItem[] {
  return items.filter((i) => !isOpen(i)).sort((a, b) => b.updated_at - a.updated_at);
}

export interface HighlightCounts {
  // Unresolved items: the pill's number.
  open: number;
  // An open item the person has not seen: the pill's dot.
  unread: boolean;
}

export function highlightCounts(items: readonly HighlightItem[]): HighlightCounts {
  const open = items.filter(isOpen);
  return { open: open.length, unread: open.some((i) => i.unread) };
}

// The one line above the composer: the most urgent open item. Findings
// never take it, and it never stacks.
export function stripItem(items: readonly HighlightItem[]): HighlightItem | null {
  for (const kind of ["needs_you", "blocker"] as const) {
    const first = items.filter((i) => isOpen(i) && i.kind === kind).sort(byAttention)[0];
    if (first !== undefined) return first;
  }
  return null;
}

const clip = (text: string, max: number) => (text.length <= max ? text : `${text.slice(0, max - 1).trimEnd()}…`);

// The title of a pinned message: its first non-empty line, with the heading,
// quote, list and emphasis marks off, clipped to what a Highlight holds.
export function pinTitle(text: string): string {
  const line =
    text
      .split("\n")
      .map((l) => l.replace(/^[\s>#-]+/, "").replace(/[*`]/g, "").trim())
      .find((l) => l !== "") ?? "Pinned message";
  return clip(line, HIGHLIGHT_LIMITS.title);
}

// Every line is one an agent wrote with an explicit update; the tab never
// builds a recap of its own.
export interface SummaryRow {
  agent: string;
  text: string;
  at: number;
}

// The latest line of each agent, newest first. An empty line is not shown.
export function summaryRows(summaries: readonly AgentSummary[]): SummaryRow[] {
  const latest = new Map<string, AgentSummary>();
  for (const s of summaries) {
    const have = latest.get(s.agent);
    if (have === undefined || s.updated_at > have.updated_at) latest.set(s.agent, s);
  }
  return [...latest.values()]
    .filter((s) => s.text.trim() !== "")
    .sort((a, b) => b.updated_at - a.updated_at)
    .map((s) => ({ agent: s.agent, text: clip(s.text.trim(), HIGHLIGHT_LIMITS.summary), at: s.updated_at }));
}
