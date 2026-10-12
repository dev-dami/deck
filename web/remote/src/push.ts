// Pure push-notification logic: what the browser can do, what the server wants,
// and what to do about the difference. No DOM access, so it is all testable.
import type { KV } from "./auth";
import { isRecord, type PushStatus, type PushSubscriptionWire } from "./protocol";

export interface PushEnv {
  secure: boolean;
  serviceWorker: boolean;
  pushManager: boolean;
  notification: boolean;
  ios: boolean;
  standalone: boolean;
}

export type PushPermission = "default" | "granted" | "denied";

// Why push cannot be turned on right now, or "ok" when it can be asked for.
export type PushGate = "server-off" | "insecure" | "ios-install" | "unsupported" | "denied" | "ok";

// Order matters: the first problem the user can act on is the one to show.
export function pushGate(env: PushEnv, permission: PushPermission, pushKey: string): PushGate {
  if (pushKey === "") return "server-off";
  if (!env.secure) return "insecure";
  // iOS only exposes PushManager to a page opened from the Home Screen.
  if (env.ios && !env.standalone) return "ios-install";
  if (!env.serviceWorker || !env.pushManager || !env.notification) return "unsupported";
  if (permission === "denied") return "denied";
  return "ok";
}

export function gateMessage(gate: PushGate): string {
  switch (gate) {
    case "server-off":
      return "This Deck is not set up to send push notifications.";
    case "insecure":
      return "Notifications need a secure (https) address. Open Deck through a tunnel instead of plain http.";
    case "ios-install":
      return "On iPhone and iPad, add this page to your Home Screen first (Share, then Add to Home Screen), then open Deck from the new icon and come back here.";
    case "unsupported":
      return "This browser does not support push notifications.";
    case "denied":
      return "Notifications are blocked for this page. Allow them in your browser's site settings (on iPhone: Settings, Notifications, Deck), then return here.";
    case "ok":
      return "";
  }
}

export function isIos(userAgent: string, maxTouchPoints: number): boolean {
  if (/iPhone|iPad|iPod/.test(userAgent)) return true;
  // iPadOS reports itself as a Mac, but only the iPad has a touch screen.
  return /Macintosh/.test(userAgent) && maxTouchPoints > 1;
}

// The applicationServerKey is base64url; atob wants padded standard base64.
export function base64UrlToBytes(input: string): Uint8Array<ArrayBuffer> | null {
  const std = input.replaceAll("-", "+").replaceAll("_", "/");
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(std) || std.length % 4 === 1) return null;
  const padded = std.padEnd(Math.ceil(std.length / 4) * 4, "=");
  let bin: string;
  try {
    bin = atob(padded);
  } catch {
    return null;
  }
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function sameBytes(a: ArrayBuffer | Uint8Array | null, b: Uint8Array): boolean {
  if (a === null) return false;
  const x = a instanceof Uint8Array ? a : new Uint8Array(a);
  return x.length === b.length && x.every((v, i) => v === b[i]);
}

// PushSubscription.toJSON() makes every field optional; the server needs all three.
export function toWireSubscription(json: unknown): PushSubscriptionWire | null {
  if (!isRecord(json)) return null;
  const endpoint = json["endpoint"];
  const keys = json["keys"];
  if (typeof endpoint !== "string" || endpoint === "" || !isRecord(keys)) return null;
  const p256dh = keys["p256dh"];
  const auth = keys["auth"];
  if (typeof p256dh !== "string" || p256dh === "") return null;
  if (typeof auth !== "string" || auth === "") return null;
  return { endpoint, keys: { p256dh, auth } };
}

// ---- what this phone remembers about its own subscription ----------------

export interface StoredPush {
  // The user turned notifications on here and has not turned them off.
  wanted: boolean;
  // Endpoint the server last accepted; a different one means the browser rotated it.
  endpoint: string | null;
  approvals: boolean;
  done: boolean;
}

export const DEFAULT_STORED_PUSH: StoredPush = {
  wanted: false,
  endpoint: null,
  approvals: true,
  done: true,
};

export function parseStoredPush(raw: string | null): StoredPush {
  if (raw === null) return DEFAULT_STORED_PUSH;
  let v: unknown;
  try {
    v = JSON.parse(raw);
  } catch {
    return DEFAULT_STORED_PUSH;
  }
  if (!isRecord(v)) return DEFAULT_STORED_PUSH;
  const endpoint = v["endpoint"];
  return {
    wanted: v["wanted"] === true,
    endpoint: typeof endpoint === "string" && endpoint !== "" ? endpoint : null,
    approvals: v["approvals"] !== false,
    done: v["done"] !== false,
  };
}

// ---- reconciling browser, server and local memory ------------------------

export interface LocalSub {
  endpoint: string;
  // False when it was created with a different VAPID key than the server now uses.
  keyMatches: boolean;
}

export type SyncAction =
  | "none"
  // Make a fresh browser subscription with the current key, then tell the server.
  | "resubscribe"
  // The browser subscription is fine; tell the server about it again.
  | "register"
  // The browser has nothing but the server still thinks it does.
  | "unregister";

export function planSync(
  local: LocalSub | null,
  server: PushStatus,
  stored: StoredPush,
  permission: PushPermission,
): SyncAction {
  if (!server.available) return "none";
  if (local === null) {
    if (!server.enabled) return "none";
    // Permission still granted and the user never turned it off: the browser
    // dropped an expired subscription. Quietly make a new one.
    return stored.wanted && permission === "granted" ? "resubscribe" : "unregister";
  }
  if (!local.keyMatches) return stored.wanted || server.enabled ? "resubscribe" : "none";
  if (server.enabled) return stored.endpoint === local.endpoint ? "none" : "register";
  return stored.wanted ? "register" : "none";
}

const STORE_KEY = "deck.push";

export function loadStoredPush(store: KV | null): StoredPush {
  if (store === null) return DEFAULT_STORED_PUSH;
  try {
    return parseStoredPush(store.getItem(STORE_KEY));
  } catch {
    // Blocked storage: fall back to defaults; the server's own record still
    // decides whether notifications are on.
    return DEFAULT_STORED_PUSH;
  }
}

export function saveStoredPush(store: KV | null, v: StoredPush): void {
  if (store === null) return;
  try {
    store.setItem(STORE_KEY, JSON.stringify(v));
  } catch {
    // Same as above: nothing to persist to, and nothing depends on it surviving.
  }
}

export function clearStoredPush(store: KV | null): void {
  if (store === null) return;
  try {
    store.removeItem(STORE_KEY);
  } catch {
    // Storage is unavailable, so there is nothing stored to clear.
  }
}
