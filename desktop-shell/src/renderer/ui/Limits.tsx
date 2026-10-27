import type { AgentLine } from "@web/protocol";
import { useMemo, useState } from "react";
import { cn } from "../lib/cn";
import {
  type AgentWait,
  canStartWindow,
  checkedAgo,
  kindName,
  type Level,
  type Limits,
  nextReset,
  type Quota,
  quotaOf,
  type ScheduledSend,
  span,
  waitLabel,
  whenOf,
} from "../limits";
import { pokeLimits, refreshLimits, useLimits, useNow } from "../limitsState";
import type { Command } from "../palette";
import { limitThresholds, usePrefs } from "../prefs";
import type { ProjectApi } from "../project";
import { CornerDownLeft, Gauge, Hourglass, Loader2, Play, RefreshCw } from "./icons";
import { Button } from "./kit/button";

// What the window shows of the agents' plan limits: the quota mark on a
// lane chip and in the composer, the room's banner while an agent waits for
// its limit to reset, the scheduled messages, and the Usage page's check
// and start-window controls (limits.ts has the rules).

const TONE: Record<Level, string> = {
  ok: "bg-muted-foreground/45",
  warn: "bg-warning",
  crit: "bg-destructive",
  out: "bg-destructive",
};

const TEXT: Record<Level, string> = {
  ok: "text-faint",
  warn: "text-warning-foreground",
  crit: "text-destructive-foreground",
  out: "text-destructive-foreground",
};

function quotaTitle(q: Quota, now: number, h12: boolean): string {
  const reset = q.resetsAt > 0 ? ` · resets ${whenOf(q.resetsAt, now, h12)} (in ${span(q.resetsAt - now)})` : "";
  return `${kindName(q.kind)}: ${Math.round(q.used)}% of its ${q.label} window used${reset}`;
}

// The quota of one agent kind, as a lane chip shows it: a short meter that
// turns amber, then red, near the limit; an hourglass while it waits.
export function QuotaMark({ api, kind, agent, sessionId }: { api: ProjectApi | undefined; kind: string; agent: string; sessionId: string }) {
  const prefs = usePrefs();
  const lim = useLimits(api).data;
  const now = useNow();
  if (lim === null) return null;
  const wait = lim.waits.find((w) => w.sessionId === sessionId && w.agent === agent);
  if (wait !== undefined) {
    return (
      <span className="lane-quota flex items-center text-warning-foreground" data-quota="waiting" title={`${agent}: ${waitLabel(wait, now, prefs.clock === "12h")}`}>
        <Hourglass aria-hidden className="size-3" />
      </span>
    );
  }
  const q = quotaOf(lim.limits, kind, now, limitThresholds(prefs));
  if (q === null) return null;
  return (
    <span className="lane-quota flex items-center" data-quota={q.level} title={quotaTitle(q, now, prefs.clock === "12h")}>
      <span aria-hidden className="h-1 w-3.5 overflow-hidden rounded-full bg-muted-foreground/20">
        <span className={cn("block h-full rounded-full", TONE[q.level])} style={{ width: `${Math.max(8, q.used)}%` }} />
      </span>
    </span>
  );
}

// The composer's note when an agent of the room nears its limit: the
// fullest kind, only once it passes the warning threshold.
export function RoomQuota({ api, roster }: { api: ProjectApi | undefined; roster: readonly AgentLine[] }) {
  const prefs = usePrefs();
  const lim = useLimits(api).data;
  const now = useNow();
  if (lim === null) return null;
  const th = limitThresholds(prefs);
  const kinds = [...new Set(roster.map((a) => a.kind))];
  const q = kinds
    .map((k) => quotaOf(lim.limits, k, now, th))
    .filter((x): x is Quota => x !== null && x.level !== "ok")
    .sort((a, b) => b.used - a.used)[0];
  if (q === undefined) return null;
  return (
    <span className={cn("room-quota flex h-7 shrink-0 items-center gap-1 px-1.5 text-2xs tabular-nums", TEXT[q.level])} data-quota={q.level} title={quotaTitle(q, now, prefs.clock === "12h")}>
      <Gauge aria-hidden className="size-3.5" />
      {kindName(q.kind)} {Math.round(q.used)}%
    </span>
  );
}

// The room's banner while its agents wait for a plan limit to reset: when,
// counting down, with Resume now and Cancel.
export function WaitBanner({
  api,
  sessionId,
  onError,
}: {
  api: ProjectApi | undefined;
  sessionId: string;
  onError: (text: string) => void;
}) {
  const prefs = usePrefs();
  const lim = useLimits(api).data;
  const now = useNow(15_000);
  const [busy, setBusy] = useState<string | null>(null);
  const waits = lim?.waits.filter((w) => w.sessionId === sessionId) ?? [];
  const scheduled = lim?.scheduled.filter((s) => s.sessionId === sessionId) ?? [];
  if (api === undefined || waits.length === 0) return null;
  const act = async (w: AgentWait, cancel: boolean) => {
    setBusy(w.agent);
    const err = await api.resolveWait(sessionId, w.agent, cancel);
    setBusy(null);
    if (err !== null) onError(err);
    await refreshLimits(api);
    pokeLimits(api);
  };
  return (
    <div className="flex flex-col gap-1" aria-label="Waiting for a plan limit" data-wait-banner="">
      {waits.map((w) => (
        <div key={w.agent} className="flex items-center gap-2 px-2 py-1.5 text-sm" data-wait={w.agent}>
          <Hourglass aria-hidden className="size-4 shrink-0 text-warning-foreground" />
          <span className="min-w-0 flex-1">
            <span className="font-medium">{w.agent}</span>
            <span className="text-muted-foreground"> · {waitLabel(w, now, prefs.clock === "12h")}</span>
            {scheduled.length > 0 && (
              <span className="text-faint text-xs">
                {" "}
                · {scheduled.length} scheduled {scheduled.length === 1 ? "message" : "messages"}
              </span>
            )}
          </span>
          <Button size="xs" variant="ghost-muted" disabled={busy !== null} onClick={() => void act(w, true)} title="Stop waiting, and drop the messages queued for it">
            Cancel
          </Button>
          <Button size="xs" variant="outline" disabled={busy !== null} onClick={() => void act(w, false)} title="Resume it now, before the limit resets">
            {busy === w.agent ? <Loader2 className="animate-spin" /> : <Play />}
            Resume now
          </Button>
        </div>
      ))}
    </div>
  );
}

// The room's scheduled messages, for the Tasks panel: when each goes, with
// Send now and Cancel.
export function ScheduledList({ api, sessionId, onError }: { api: ProjectApi | undefined; sessionId: string; onError: (text: string) => void }) {
  const prefs = usePrefs();
  const lim = useLimits(api).data;
  const now = useNow();
  const items = lim?.scheduled.filter((s) => s.sessionId === sessionId) ?? [];
  if (api === undefined || items.length === 0) return null;
  const act = async (s: ScheduledSend, now: boolean) => {
    const err = await api.cancelScheduled(sessionId, s.id, now);
    if (err !== null) onError(err);
    await refreshLimits(api);
  };
  return (
    <ul aria-label="Scheduled" className="flex flex-col gap-2">
      {items.map((s) => (
        <li key={s.id} className="scheduled flex flex-col gap-2 rounded-[14px] border border-hairline bg-surface-1 p-3" data-scheduled={s.id}>
          <div className="flex items-center gap-1 text-muted-foreground text-xs">
            <Hourglass aria-hidden className="size-3.5" />
            <span className="flex-1">
              Sends at {whenOf(s.at, now, prefs.clock === "12h")} <span className="text-faint">(in {span(s.at - now)})</span>
            </span>
          </div>
          <p className="whitespace-pre-wrap text-sm">{s.text}</p>
          <div className="flex justify-end gap-1.5">
            <Button size="xs" variant="ghost-muted" onClick={() => void act(s, false)}>
              Cancel
            </Button>
            <Button size="xs" variant="outline" onClick={() => void act(s, true)}>
              <CornerDownLeft />
              Send now
            </Button>
          </div>
        </li>
      ))}
    </ul>
  );
}

// The kind a draft goes to, for "Send at next reset": the agent it names,
// else one that waits in the room, else the room's first agent.
export function draftKind(draft: string, roster: readonly AgentLine[], waits: readonly AgentWait[], sessionId: string): string | null {
  const named = roster.find((a) => new RegExp(`(^|\\s)[@$]${a.name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?![\\w-])`, "i").test(draft));
  if (named !== undefined) return named.kind;
  const w = waits.find((x) => x.sessionId === sessionId);
  if (w !== undefined) return w.kind;
  return roster[0]?.kind ?? null;
}

// When "Send at next reset" would send a draft, or null when no reset is
// known for its agent.
export function sendAtReset(lim: Limits | null, draft: string, roster: readonly AgentLine[], sessionId: string, now: number): { kind: string; at: number } | null {
  if (lim === null) return null;
  const kind = draftKind(draft, roster, lim.waits, sessionId);
  if (kind === null) return null;
  const wait = lim.waits.find((w) => w.sessionId === sessionId && w.kind === kind && w.until > now);
  const at = wait?.until ?? nextReset(lim.limits, kind, now);
  return at === null ? null : { kind, at };
}

// ---- the Usage page ---------------------------------------------------------

// The header's "Check limits": asks every agent for its limits now.
export function CheckLimitsButton({ api, onDone }: { api: ProjectApi; onDone: (text: string, error: boolean) => void }) {
  const [busy, setBusy] = useState(false);
  const go = async () => {
    setBusy(true);
    const r = await api.checkLimits("");
    setBusy(false);
    await refreshLimits(api);
    if (typeof r === "string") return onDone(r, true);
    const failed = r.filter((c) => c.error !== "");
    onDone(
      failed.length === 0
        ? "Checked every agent's limits."
        : failed.map((c) => (c.unsupported ? `${kindName(c.kind)} can't be checked: ${c.error}.` : `${kindName(c.kind)}: ${c.error}`)).join(" "),
      failed.length > 0 && failed.length === r.length,
    );
  };
  return (
    <Button variant="outline" size="xs" disabled={busy} onClick={() => void go()} title="Ask each agent's CLI for its plan limits now" data-check-limits="">
      {busy ? <Loader2 className="animate-spin" /> : <RefreshCw />}
      Check limits
    </Button>
  );
}

const START_TIP =
  "Starts this agent's rolling window now by sending it one tiny prompt (\"Reply with OK\") on its cheapest model, in a throwaway session no room shows. The window then resets that much sooner. It uses a little of your plan.";

// One kind's line on the Usage page: when it was last checked (or why it
// can't be), Check, and Start window now while no window runs.
export function KindLimitControls({ api, kind, onDone }: { api: ProjectApi | undefined; kind: string; onDone: (text: string, error: boolean) => void }) {
  const lim = useLimits(api).data;
  const now = useNow();
  const [busy, setBusy] = useState<"check" | "start" | null>(null);
  if (api === undefined || lim === null) return null;
  const check = lim.checks.find((c) => c.kind === kind);
  const go = async (what: "check" | "start") => {
    setBusy(what);
    if (what === "check") {
      const r = await api.checkLimits(kind);
      const c = typeof r === "string" ? null : r[0];
      if (typeof r === "string") onDone(r, true);
      else if (c !== undefined && c !== null && c.error !== "") onDone(c.unsupported ? `${kindName(kind)} can't be checked: ${c.error}.` : `${kindName(kind)}: ${c.error}`, true);
    } else {
      const r = await api.startWindow(kind);
      if (typeof r === "string") onDone(r, true);
      else onDone(`Started ${kindName(kind)}'s window${r.model !== "" ? ` with ${r.model}` : ""}.`, false);
    }
    setBusy(null);
    await refreshLimits(api);
  };
  const status = check === undefined ? null : check.unsupported ? "can't be checked: it does not report plan limits" : check.error !== "" ? `check failed: ${check.error}` : checkedAgo(check.checkedAt, now);
  return (
    <div className="flex items-center gap-1.5" data-limit-controls={kind}>
      {status !== null && (
        <span className={cn("text-2xs", check?.error !== "" ? "text-warning-foreground" : "text-faint")} data-checked={kind}>
          {status}
        </span>
      )}
      <Button variant="ghost-muted" size="xs" disabled={busy !== null} onClick={() => void go("check")} title={`Ask ${kindName(kind)} for its limits now`} data-check-kind={kind}>
        {busy === "check" ? <Loader2 className="animate-spin" /> : <RefreshCw />}
        Check
      </Button>
      {canStartWindow(lim.limits, kind, now) && (
        <Button variant="outline" size="xs" disabled={busy !== null} onClick={() => void go("start")} title={START_TIP} data-start-window={kind}>
          {busy === "start" ? <Loader2 className="animate-spin" /> : <Play />}
          Start window now
        </Button>
      )}
    </div>
  );
}

// ---- the palette ------------------------------------------------------------

// The palette's limit commands for the current project: check every agent
// or one, start a rolling window that is not running, and resume an agent
// that waits.
export function useLimitCommands(api: ProjectApi | undefined, toast: (t: string) => void, info: (t: string) => void): Command[] {
  const lim = useLimits(api).data;
  const minute = Math.floor(Date.now() / 60_000);
  return useMemo(() => {
    if (api === undefined || lim === null) return [];
    const now = minute * 60_000;
    const say = (text: string, error: boolean) => (error ? toast(text) : info(text));
    const check = async (kind: string) => {
      const r = await api.checkLimits(kind);
      await refreshLimits(api);
      if (typeof r === "string") return toast(r);
      const failed = r.filter((c) => c.error !== "");
      if (failed.length === 0) return info(kind === "" ? "Checked every agent's limits." : `Checked ${kindName(kind)}'s limits.`);
      say(failed.map((c) => (c.unsupported ? `${kindName(c.kind)} can't be checked: ${c.error}.` : `${kindName(c.kind)}: ${c.error}`)).join(" "), failed.length === r.length);
    };
    const begin = async (kind: string) => {
      const r = await api.startWindow(kind);
      await refreshLimits(api);
      say(typeof r === "string" ? r : `Started ${kindName(kind)}'s window${r.model !== "" ? ` with ${r.model}` : ""}.`, typeof r === "string");
    };
    const out: Command[] = [{ id: "limits-check", label: "Check plan limits", run: () => void check("") }];
    for (const k of lim.kinds) {
      out.push({ id: `limits-check-${k}`, label: `Check ${kindName(k)}'s limits`, run: () => void check(k) });
      if (canStartWindow(lim.limits, k, now)) out.push({ id: `limits-window-${k}`, label: `Start ${kindName(k)}'s window now`, hint: "uses a little quota", run: () => void begin(k) });
    }
    for (const w of lim.waits) {
      out.push({
        id: `limits-resume-${w.sessionId}-${w.agent}`,
        label: `Resume ${w.agent} now (waiting for its limit)`,
        run: () =>
          void api.resolveWait(w.sessionId, w.agent, false).then((err) => {
            if (err !== null) toast(err);
            void refreshLimits(api);
          }),
      });
    }
    return out;
  }, [api, lim, minute, toast, info]);
}
