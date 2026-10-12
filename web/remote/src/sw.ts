// Service worker: caches the app shell only (chat data, tokens and API
// responses never touch the cache) and shows push notifications.
import {
  appIsInView,
  type ClientInfo,
  type NotificationSpec,
  notificationOptions,
  parsePushPayload,
  planClick,
  shellPaths,
  strategyFor,
} from "./sw-logic";

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

interface PushEventLike extends ExtendableEventLike {
  data: { text(): string } | null;
}

interface NotificationLike {
  data: unknown;
  close(): void;
}

interface NotificationEventLike extends ExtendableEventLike {
  notification: NotificationLike;
}

interface PushSubscriptionLike {
  options: { applicationServerKey: ArrayBuffer | null };
}

interface SubscriptionChangeEventLike extends ExtendableEventLike {
  oldSubscription: PushSubscriptionLike | null;
  newSubscription: PushSubscriptionLike | null;
}

interface WindowClientLike extends ClientInfo {
  focus(): Promise<WindowClientLike>;
  navigate(url: string): Promise<WindowClientLike | null>;
  postMessage(message: unknown): void;
}

interface WorkerScope {
  addEventListener(type: "install" | "activate", fn: (e: ExtendableEventLike) => void): void;
  addEventListener(type: "fetch", fn: (e: FetchEventLike) => void): void;
  addEventListener(type: "push", fn: (e: PushEventLike) => void): void;
  addEventListener(type: "notificationclick", fn: (e: NotificationEventLike) => void): void;
  addEventListener(
    type: "pushsubscriptionchange",
    fn: (e: SubscriptionChangeEventLike) => void,
  ): void;
  skipWaiting(): Promise<void>;
  clients: {
    claim(): Promise<void>;
    matchAll(opts: {
      type: "window";
      includeUncontrolled: boolean;
    }): Promise<readonly WindowClientLike[]>;
    openWindow(url: string): Promise<WindowClientLike | null>;
  };
  registration: {
    scope: string;
    showNotification(title: string, options: NotificationSpec): Promise<void>;
    pushManager: {
      subscribe(opts: {
        userVisibleOnly: boolean;
        applicationServerKey: ArrayBuffer;
      }): Promise<PushSubscriptionLike>;
    };
  };
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

function windows(): Promise<readonly WindowClientLike[]> {
  return sw.clients.matchAll({ type: "window", includeUncontrolled: true });
}

sw.addEventListener("push", (e) => {
  const payload = parsePushPayload(e.data === null ? null : e.data.text());
  e.waitUntil(
    windows().then((clients) => {
      if (appIsInView(clients)) return;
      return sw.registration.showNotification(payload.title, notificationOptions(payload));
    }),
  );
});

async function openFromNotification(data: unknown): Promise<void> {
  const clients = await windows();
  const plan = planClick(clients, sw.registration.scope, data);
  const client = plan.action === "focus" ? clients[plan.index] : undefined;
  if (client === undefined) {
    await sw.clients.openWindow(plan.url);
    return;
  }
  try {
    const focused = await client.focus();
    await focused.navigate(plan.url);
  } catch {
    // navigate() is refused for windows this worker does not control yet.
    await sw.clients.openWindow(plan.url);
  }
}

sw.addEventListener("notificationclick", (e) => {
  e.notification.close();
  e.waitUntil(openFromNotification(e.notification.data));
});

// Best effort: the worker has no pairing token, so it can only renew the
// browser side and ask open pages to re-register the new endpoint with Deck.
sw.addEventListener("pushsubscriptionchange", (e) => {
  const key = e.oldSubscription?.options.applicationServerKey ?? null;
  e.waitUntil(
    (async () => {
      if (e.newSubscription === null && key !== null) {
        await sw.registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: key,
        });
      }
      for (const c of await windows()) c.postMessage({ type: "push-subscription-changed" });
    })().catch((err: unknown) => {
      console.warn("push subscription renewal failed", err);
    }),
  );
});
