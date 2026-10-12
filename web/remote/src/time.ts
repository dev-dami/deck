// The wire does not pin the unit of timestamps; fixtures use seconds, but a
// value past 1e12 can only be milliseconds.
export function toMillis(t: number): number {
  return t > 1e12 ? t : t * 1000;
}

export function relativeTime(t: number, now: number): string {
  if (t <= 0) return "";
  const s = Math.max(0, Math.round((now - toMillis(t)) / 1000));
  if (s < 45) return "now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  const d = Math.round(h / 24);
  return d < 30 ? `${d}d` : new Date(toMillis(t)).toLocaleDateString();
}
