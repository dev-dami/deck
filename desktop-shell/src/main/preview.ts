import { connect } from "node:net";
import type { Session, WebContents, WebPreferences } from "electron";

// The rules of the browser's pages, whoever drives them. A page gets nothing
// of the app: a session that lives in memory only, the sandbox, no preload
// and no IPC, no permission it asks for and no download. Where it may go
// depends on who opened it: the person's tab goes anywhere on the web, an
// agent's tab only where the person let it.

// No "persist:" prefix: a session keeps its cookies and cache in memory. Each
// local Deck has one, shared by its agents' tabs and the person's.
export const browserPartition = (deck: string): string => `browser-${deck}`;

const PROBE_MS = 300;

// Whether something accepts a TCP connection on host:port within ms.
export function listening(port: number, host: string, ms = PROBE_MS): Promise<boolean> {
  return new Promise((done) => {
    const s = connect({ port, host });
    const end = (ok: boolean) => {
      s.destroy();
      done(ok);
    };
    s.setTimeout(ms, () => end(false));
    s.once("connect", () => end(true));
    s.once("error", () => end(false));
  });
}

// The ports that answer on 127.0.0.1 or [::1]: a dev server that binds
// "localhost" may take either.
export async function openPorts(ports: number[]): Promise<number[]> {
  const up = await Promise.all(ports.map(async (p) => (await Promise.all([listening(p, "127.0.0.1"), listening(p, "::1")])).some(Boolean)));
  return ports.filter((_, i) => up[i]);
}

export type Verdict = "load" | "ask" | "deny";

// Who asks for a page. An agent's tab may go to a page of this computer
// (a dev server), to the site it is on (a redirect to www. or https), to a
// site in allowed (origins the person approved) and, when free is set (the
// room is in yolo mode), to any web page.
export type Who = { kind: "person" } | { kind: "agent"; at: string; allowed: readonly string[]; free: boolean };

const isLoopback = (host: string): boolean => host === "localhost" || host === "[::1]" || host === "::1" || /^127(\.\d{1,3}){3}$/.test(host);

// A page address typed by a person or named by an agent: http(s) only, no
// credentials. "example.com/a" is https and "localhost:3000" is http.
export function parseWebUrl(raw: string): URL | null {
  let s = raw.trim();
  if (s === "" || /\s/.test(s)) return null;
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) {
    // A scheme without slashes (javascript:, data:, mailto:) is no host.
    if (/^[a-z][a-z0-9+.-]*:(?!\d+(\/|$))/i.test(s)) return null;
    s = `${isLoopback(s.replace(/[:/].*$/, "").toLowerCase()) ? "http" : "https"}://${s}`;
  }
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return null;
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  if (u.hostname === "" || u.username !== "" || u.password !== "") return null;
  return u;
}

const bare = (host: string): string => host.replace(/^www\./, "");

// Whether two addresses are the same site for a redirect: the same host, with
// or without www., on http or https.
function sameSite(a: URL, b: URL): boolean {
  return bare(a.hostname) === bare(b.hostname) && (a.port === b.port || a.protocol !== b.protocol);
}

// What happens to a URL a tab is asked to show, by the person, an agent or
// the page itself: "load", "ask" (an agent needs the person's word for this
// site) or "deny" (not a web page, or one with credentials in its address).
export function judgeUrl(raw: string, who: Who): Verdict {
  const u = parseWebUrl(raw);
  if (u === null || !/^[a-z][a-z0-9+.-]*:\/\//i.test(raw.trim())) return "deny";
  if (who.kind === "person") return "load";
  if (who.free || isLoopback(u.hostname)) return "load";
  if (who.allowed.includes(u.origin)) return "load";
  const here = parseWebUrl(who.at);
  if (here !== null && sameSite(here, u)) return "load";
  return "ask";
}

// A page runs with no preload, no Node and the sandbox on.
export function pagePrefs(session: Session): WebPreferences {
  return {
    session,
    sandbox: true,
    contextIsolation: true,
    nodeIntegration: false,
    nodeIntegrationInSubFrames: false,
    webSecurity: true,
    webviewTag: false,
  };
}

// The session asks for nothing: every permission is denied and every
// download is cancelled.
export function lockSession(ses: Session): void {
  ses.setPermissionRequestHandler((_wc, _perm, cb) => cb(false));
  ses.setPermissionCheckHandler(() => false);
  ses.setDevicePermissionHandler(() => false);
  ses.on("will-download", (e) => e.preventDefault());
}

export interface GuardHooks {
  /** Who owns the page now, and so where it may go. */
  who: () => Who;
  /** An agent's page tried to go where it may not without asking. */
  blocked: (url: string) => void;
}

// Keeps a page where its owner may go: main checks each navigation,
// redirect and new window against judgeUrl.
export function guardPage(wc: WebContents, h: GuardHooks): void {
  const go = (url: string): boolean => {
    const v = judgeUrl(url, h.who());
    if (v === "ask") h.blocked(url);
    return v === "load";
  };
  wc.on("will-navigate", (e) => {
    if (!go(e.url)) e.preventDefault();
  });
  wc.on("will-redirect", (e) => {
    if (!go(e.url)) e.preventDefault();
  });
  // A page never opens a window of its own: where it asks, this tab goes.
  wc.setWindowOpenHandler(({ url }) => {
    if (go(url)) void wc.loadURL(url).catch(() => {});
    return { action: "deny" };
  });
}
