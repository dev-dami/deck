// What the service worker decides, kept free of worker globals so it can be
// tested. v1 caches the app shell only: no push, no API, no chat data.

export interface FetchInfo {
  method: string;
  url: string;
  mode: string;
}

// "navigate" is network-first with the cached shell as offline fallback;
// "cache-first" is for the content-hashed shell files; everything else
// (the API, the WebSocket, other origins, non-GET) goes straight to the
// network untouched.
export type Strategy = "bypass" | "cache-first" | "navigate";

export function strategyFor(req: FetchInfo, origin: string, shell: ReadonlySet<string>): Strategy {
  if (req.method !== "GET") return "bypass";
  let u: URL;
  try {
    u = new URL(req.url);
  } catch {
    return "bypass";
  }
  if (u.origin !== origin) return "bypass";
  if (u.pathname.startsWith("/api/")) return "bypass";
  if (req.mode === "navigate") return "navigate";
  return shell.has(u.pathname) ? "cache-first" : "bypass";
}

export function shellPaths(entries: readonly string[], scope: string): Set<string> {
  return new Set(entries.map((e) => new URL(e, scope).pathname));
}
