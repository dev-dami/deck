import { b64urlDecode, b64urlEncode } from "../b64";
import type { PushRegisterParams } from "../protocol3";
import type { ComputerRecord } from "../store/computers";

export async function registerHostedWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!("serviceWorker" in navigator)) return null;
  return navigator.serviceWorker.register("./sw.js", { scope: "./", type: "module" });
}

interface PushSession {
  call<T>(method: string, params?: unknown, pj?: string): Promise<T>;
}

export interface SubscribeOptions {
  approvals: boolean;
  done: boolean;
}

export async function subscribePush(
  computer: ComputerRecord,
  session: PushSession,
  registration: ServiceWorkerRegistration,
  choices: SubscribeOptions = { approvals: true, done: true },
  doFetch: typeof fetch = fetch,
): Promise<void> {
  if (!computer.relayToken) throw new Error("Pair this computer before enabling notifications.");
  if (!("PushManager" in globalThis) || !("Notification" in globalThis)) {
    throw new Error(
      "Push notifications are unavailable in this browser. On iPhone, add Deck to your Home Screen first.",
    );
  }
  // Called from the user's button click, before any network await.
  const permission = await Notification.requestPermission();
  if (permission !== "granted") throw new Error("Notification permission was not granted.");
  const base = computer.relayUrl.replace(/\/+$/, "");
  const vapid = await doFetch(`${base}/r/v1/vapid`);
  if (!vapid.ok) throw new Error("Could not load the notification server key.");
  const body: unknown = await vapid.json();
  const raw =
    typeof body === "object" && body !== null && "public_key" in body ? body.public_key : null;
  const key = typeof raw === "string" ? b64urlDecode(raw) : null;
  if (key?.length !== 65) throw new Error("Invalid notification server key.");
  const existing = await registration.pushManager.getSubscription();
  // A browser subscription has one VAPID key. Do not silently replace it and
  // break other paired computers if they use a different relay.
  if (existing?.options.applicationServerKey) {
    const previous = new Uint8Array(existing.options.applicationServerKey);
    if (previous.length !== key.length || previous.some((b, i) => b !== key[i])) {
      throw new Error("Notifications are already enabled through another relay on this device.");
    }
  }
  const subscription =
    existing ??
    (await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: new Uint8Array(key),
    }));
  const p256dh = subscription.getKey("p256dh");
  const auth = subscription.getKey("auth");
  if (!p256dh || !auth)
    throw new Error("The browser did not provide notification encryption keys.");
  const response = await doFetch(`${base}/r/v1/c/${encodeURIComponent(computer.cid)}/push`, {
    method: "PUT",
    headers: { "content-type": "application/json", authorization: `Bearer ${computer.relayToken}` },
    body: JSON.stringify({ endpoint: subscription.endpoint }),
  });
  if (!response.ok) throw new Error("The relay refused the notification subscription.");
  const params: PushRegisterParams = {
    p256dh: b64urlEncode(new Uint8Array(p256dh)),
    auth: b64urlEncode(new Uint8Array(auth)),
    ...choices,
  };
  await session.call("push.register", params);
}

export async function disablePush(session: PushSession): Promise<void> {
  // Keep the shared browser subscription for other paired computers.
  await session.call("push.register", { p256dh: "", auth: "", approvals: false, done: false });
}
