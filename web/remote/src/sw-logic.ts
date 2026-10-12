import { isRecord } from "./protocol";
import { formatRoute } from "./route";

export interface FetchInfo {
  method: string;
  url: string;
  mode: string;
}

// "navigate" is network-first with the cached shell as offline fallback;
// "cache-first" is for the content-hashed shell files; everything else
// (the API, other origins, non-GET) goes straight to the network untouched.
export type Strategy = "bypass" | "cache-first" | "navigate";

export function strategyFor(req: FetchInfo, origin: string, shell: ReadonlySet<string>): Strategy {
  if (req.method !== "GET") return "bypass";
  const u = new URL(req.url);
  if (u.origin !== origin) return "bypass";
  if (u.pathname.startsWith("/api/")) return "bypass";
  if (req.mode === "navigate") return "navigate";
  return shell.has(u.pathname) ? "cache-first" : "bypass";
}

export function shellPaths(entries: readonly string[], scope: string): Set<string> {
  return new Set(entries.map((e) => new URL(e, scope).pathname));
}

// ---- push --------------------------------------------------------------

export interface PushPayload {
  title: string;
  body: string;
  tag: string;
  session_id: string;
  approval_id: string;
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// A push the browser delivers must still produce a notification, so an unreadable
// payload degrades to a generic one instead of failing.
export function parsePushPayload(text: string | null): PushPayload {
  let v: unknown = null;
  if (text !== null && text !== "") {
    try {
      v = JSON.parse(text);
    } catch {
      v = null;
    }
  }
  const r = isRecord(v) ? v : {};
  return {
    title: str(r["title"]).trim() || "Deck",
    body: str(r["body"]),
    tag: str(r["tag"]),
    session_id: str(r["session_id"]),
    approval_id: str(r["approval_id"]),
  };
}

export interface NotificationData {
  session_id: string;
  approval_id: string;
}

export interface NotificationSpec {
  body: string;
  renotify: false;
  data: NotificationData;
  tag?: string;
}

// renotify:false so a repeat of the same tag updates quietly instead of buzzing again.
export function notificationOptions(p: PushPayload): NotificationSpec {
  const base: NotificationSpec = {
    body: p.body,
    renotify: false,
    data: { session_id: p.session_id, approval_id: p.approval_id },
  };
  return p.tag === "" ? base : { ...base, tag: p.tag };
}

export interface ClientInfo {
  url: string;
  focused: boolean;
  visibilityState: string;
}

// The in-app UI (toast, vibration, badge) already tells a user who is looking.
export function appIsInView(clients: readonly ClientInfo[]): boolean {
  return clients.some((c) => c.focused || c.visibilityState === "visible");
}

export function notificationSession(data: unknown): string {
  return isRecord(data) ? str(data["session_id"]) : "";
}

// Same hash format the app routes on, resolved against the worker's scope.
export function clickUrl(scope: string, data: unknown): string {
  const id = notificationSession(data);
  const hash = formatRoute(id === "" ? { name: "sessions" } : { name: "session", id });
  return new URL(hash, scope).href;
}

export type ClickPlan =
  | { action: "focus"; index: number; url: string }
  | { action: "open"; url: string };

// Prefers the window the user is already looking at, then any visible one, then
// any window of the app; only opens a new one when none exist.
export function planClick(clients: readonly ClientInfo[], scope: string, data: unknown): ClickPlan {
  const url = clickUrl(scope, data);
  const inScope = clients
    .map((c, index) => ({ c, index }))
    .filter(({ c }) => c.url.startsWith(scope));
  const pick =
    inScope.find(({ c }) => c.focused) ??
    inScope.find(({ c }) => c.visibilityState === "visible") ??
    inScope[0];
  return pick === undefined ? { action: "open", url } : { action: "focus", index: pick.index, url };
}
