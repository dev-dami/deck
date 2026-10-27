// The fake Deck's analytics record (UsageResponse.hours and limit_history):
// weeks of working hours for claude (two agents), codex and opencode, and
// the plan windows Claude Code and Codex would have reported over them, as
// pkg/rpc writes them. Deterministic for a given now, and busy in the last
// hours so a screenshot always shows a burn rate.

type Obj = Record<string, unknown>;

const H = 3_600_000;

// A repeatable 0-1 sequence.
const rnd = (i: number) => (((i * 9301 + 49297) % 233280) + 233280) % 233280 / 233280;

interface Window {
  kind: string;
  id: string;
  wkind: string;
  label: string;
  mins: number;
  plan: string;
  // Tokens that use the whole window.
  cap: number;
  // A rolling window starts at the first use after a reset (Claude's five
  // hours); a fixed one resets on its own clock.
  rolling: boolean;
}

const WINDOWS: Window[] = [
  { kind: "claude", id: "five_hour", wkind: "session", label: "5-hour", mins: 300, plan: "max", cap: 1_400_000, rolling: true },
  { kind: "claude", id: "seven_day", wkind: "weekly", label: "Weekly", mins: 10_080, plan: "max", cap: 30_000_000, rolling: false },
  { kind: "codex", id: "primary", wkind: "session", label: "5-hour", mins: 300, plan: "pro", cap: 900_000, rolling: true },
  { kind: "codex", id: "secondary", wkind: "weekly", label: "Weekly", mins: 10_080, plan: "pro", cap: 9_000_000, rolling: false },
];

export function analyticsRecord(now: number, days: number): { hours: Obj[]; limit_history: Obj[]; limits: Obj[] } {
  const lastHour = Math.floor(now / H) * H;
  const start = lastHour - days * 24 * H;
  const hours: Obj[] = [];
  const used = new Map<Window, { used: number; resetsAt: number; last: number }>();
  // Fixed windows reset on a weekday morning, a few days from now.
  for (const w of WINDOWS) used.set(w, { used: 0, resetsAt: w.rolling ? 0 : Math.floor(now / H) * H + (w.kind === "claude" ? 58 : 100) * H - 7 * 24 * H * Math.ceil(days / 7), last: -1 });
  const history: Obj[] = [];
  const note = (w: Window, at: number) => {
    const s = used.get(w)!;
    if (Math.abs(s.used - s.last) < 1) return;
    s.last = s.used;
    history.push({ agent_kind: w.kind, window_id: w.id, kind: w.wkind, label: w.label, used_pct: Math.round(s.used * 10) / 10, resets_at: s.resetsAt, duration_mins: w.mins, plan: w.plan, at });
  };

  for (let t = start, i = 0; t <= now; t += H, i++) {
    const d = new Date(t);
    const hr = d.getHours();
    const weekend = d.getDay() === 0 || d.getDay() === 6;
    const recent = now - t < 6 * H;
    let work = hr >= 9 && hr <= 18 && !weekend ? 1 : hr >= 20 && hr <= 23 ? 0.35 : 0;
    if (recent) work = 0.8;
    // Claude has had a busy few hours: at this rate its 5-hour window runs
    // out before it resets.
    const surge = now - t < 3 * H ? 1.9 : 1;
    const tokens: Record<string, number> = { claude: 0, codex: 0, opencode: 0 };
    if (work > 0 && (recent || rnd(i) > 0.22)) {
      const c = Math.round(surge * work * (0.35 + rnd(i + 3)) * 150_000);
      const reviewer = Math.round(c * 0.28);
      tokens["claude"] = c;
      hours.push({ at: t, kind: "claude", agent_id: "claude-1", turns: 2 + Math.round(rnd(i + 5) * 6), tokens: c - reviewer, cost_usd: (c - reviewer) * 0.000011 });
      hours.push({ at: t, kind: "claude", agent_id: "reviewer", turns: 1 + Math.round(rnd(i + 6) * 2), tokens: reviewer, cost_usd: reviewer * 0.000011 });
      if (recent || rnd(i + 11) > 0.4) {
        const x = Math.round(work * (0.3 + rnd(i + 13)) * 90_000);
        tokens["codex"] = x;
        hours.push({ at: t, kind: "codex", agent_id: "codex-1", turns: 1 + Math.round(rnd(i + 17) * 4), tokens: x, cost_usd: 0 });
      }
      if (rnd(i + 23) > 0.82) {
        const o = Math.round(work * rnd(i + 29) * 60_000);
        tokens["opencode"] = o;
        hours.push({ at: t, kind: "opencode", agent_id: "opencode-1", turns: 1, tokens: o, cost_usd: o * 0.000004 });
      }
    }
    // Plan windows: only the last ten days were reported (Deck had been
    // running that long), the way a real record starts.
    const reporting = now - t < 10 * 24 * H;
    for (const w of WINDOWS) {
      const s = used.get(w)!;
      const tk = tokens[w.kind] ?? 0;
      // The hour now reports a few minutes before now.
      const at = Math.min(t + 30 * 60_000, now - 4 * 60_000);
      if (s.resetsAt > 0 && at >= s.resetsAt) {
        s.used = 0;
        s.resetsAt = w.rolling ? 0 : s.resetsAt + w.mins * 60_000;
      }
      // The current rolling window began two hours ago, whatever the clock
      // says, so the page has a window in progress to project.
      if (w.rolling && t === lastHour - 2 * H) {
        s.used = 0;
        s.resetsAt = t + w.mins * 60_000;
      }
      if (tk <= 0) continue;
      if (s.resetsAt === 0) s.resetsAt = t + w.mins * 60_000;
      s.used = Math.min(100, s.used + (tk / w.cap) * 100);
      if (reporting && at <= now) note(w, at);
    }
  }
  const latest = new Map<string, Obj>();
  for (const h of history) latest.set(`${h["agent_kind"]}/${h["window_id"]}`, h);
  return { hours, limit_history: history, limits: [...latest.values()] };
}
