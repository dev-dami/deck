// Service worker: caches the app shell only. Chat data, tokens and API
// responses never touch the cache, and v1 has no push handling (§8).
import { shellPaths, strategyFor } from "./sw-logic";

// Injected by build.ts: the shell file list and a cache name derived from its content.
declare const __SHELL__: string[];
declare const __CACHE__: string;

interface ExtendableEventLike {
  waitUntil(p: Promise<unknown>): void;
}

interface FetchEventLike extends ExtendableEventLike {
  request: Request;
  respondWith(r: Promise<Response>): void;
}

interface WorkerScope {
  addEventListener(type: "install" | "activate", fn: (e: ExtendableEventLike) => void): void;
  addEventListener(type: "fetch", fn: (e: FetchEventLike) => void): void;
  skipWaiting(): Promise<void>;
  clients: { claim(): Promise<void> };
  registration: { scope: string };
  location: { origin: string };
}

const sw = self as unknown as WorkerScope;
const CACHE_PREFIX = "deck-shell-";
const cacheName = `${CACHE_PREFIX}${__CACHE__}`;
const shell = shellPaths(__SHELL__, sw.registration.scope);

sw.addEventListener("install", (e) => {
  e.waitUntil(
    caches
      .open(cacheName)
      .then((c) => c.addAll(__SHELL__))
      .then(() => sw.skipWaiting()),
  );
});

sw.addEventListener("activate", (e) => {
  e.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter((k) => k.startsWith(CACHE_PREFIX) && k !== cacheName)
            .map((k) => caches.delete(k)),
        ),
      )
      .then(() => sw.clients.claim()),
  );
});

sw.addEventListener("fetch", (e) => {
  const { request } = e;
  const strategy = strategyFor(request, sw.location.origin, shell);
  if (strategy === "bypass") return;
  if (strategy === "cache-first") {
    e.respondWith(caches.match(request).then((hit) => hit ?? fetch(request)));
    return;
  }
  e.respondWith(
    fetch(request).catch(async () => {
      const cached = await caches.match("./");
      return cached ?? Response.error();
    }),
  );
});
