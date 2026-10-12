// Timestamps on the wire are RFC 3339 strings (§3.7).

export function toMillis(t: string | undefined): number {
  if (t === undefined) return 0;
  const ms = Date.parse(t);
  return Number.isNaN(ms) ? 0 : ms;
}

// Newer first: a later timestamp wins; ties and unparseable values fall back
// to the id so the order is stable.
export function compareNewest(aTime: string, aId: string, bTime: string, bId: string): number {
  return toMillis(bTime) - toMillis(aTime) || aId.localeCompare(bId);
}

export function relativeTime(t: string | undefined, now: number): string {
  const ms = toMillis(t);
  if (ms <= 0) return "";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 45) return "now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  const d = Math.round(h / 24);
  return d < 30 ? `${d}d` : new Date(ms).toLocaleDateString();
}

export function daysUntil(t: string | undefined, now: number): number | null {
  const ms = toMillis(t);
  if (ms <= 0) return null;
  return Math.ceil((ms - now) / 86_400_000);
}
