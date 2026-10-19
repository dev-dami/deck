// Service worker: caches the app shell only. The hosted build also handles
// encrypted web push; chat data, tokens and API responses never enter caches.

import {
  answerNotification,
  notificationActions,
  notificationRoute,
  parsePush,
} from "./push/messages";
import { shellPaths, strategyFor } from "./sw-logic";

// Injected by build.ts: the shell file list and a cache name derived from its content.
declare const __SHELL__: string[];
declare const __CACHE__: string;
declare const __HOSTED__: boolean;

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
  addEventListener(
    type: "push",
    fn: (e: ExtendableEventLike & { data?: { json(): unknown } }) => void,
  ): void;
  addEventListener(
    type: "notificationclick",
    fn: (
      e: ExtendableEventLike & { action: string; notification: { data: unknown; close(): void } },
    ) => void,
  ): void;
  skipWaiting(): Promise<void>;
  clients: { claim(): Promise<void>; openWindow(url: string): Promise<unknown> };
  registration: {
    scope: string;
    showNotification(
      title: string,
      options: NotificationOptions & { actions?: { action: string; title: string }[] },
    ): Promise<void>;
  };
  location: { origin: string };
}

const sw = self as unknown as WorkerScope;
const CACHE_PREFIX = __HOSTED__ ? "deck-hosted-shell-" : "deck-shell-";
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

if (__HOSTED__) {
  sw.addEventListener("push", (e) => {
    let message: ReturnType<typeof parsePush>;
    try {
      message = parsePush(e.data?.json());
    } catch {
      return;
    }
    if (!message) return;
    e.waitUntil(
      sw.registration.showNotification(message.title, {
        body: message.body,
        icon: "./icon-192.png",
        data: message,
        tag: `${message.cid ?? "deck"}:${message.pj}:${message.session_id}:${message.approval_id ?? message.kind}`,
        actions: notificationActions(message),
      }),
    );
  });
  sw.addEventListener("notificationclick", (e) => {
    e.notification.close();
    const message = parsePush(e.notification.data);
    e.waitUntil(
      (async () => {
        if (message && e.action) {
          try {
            if (await answerNotification(message, e.action)) return;
          } catch {
            /* Review failures in app. */
          }
        }
        await sw.clients.openWindow(
          new URL(message ? notificationRoute(message) : "#computers", sw.registration.scope).href,
        );
      })(),
    );
  });
}

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
    e.respondWith(
      caches.open(cacheName).then(async (cache) => (await cache.match(request)) ?? fetch(request)),
    );
    return;
  }
  e.respondWith(
    fetch(request).catch(async () => {
      const cached = await (await caches.open(cacheName)).match("./");
      return cached ?? Response.error();
    }),
  );
});
