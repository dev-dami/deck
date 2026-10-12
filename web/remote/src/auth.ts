import { isPairResponse, type PairResponse } from "./protocol";

const TOKEN_KEY = "deck.token";

export interface KV {
  getItem(k: string): string | null;
  setItem(k: string, v: string): void;
  removeItem(k: string): void;
}

// localStorage throws in private modes and when storage is blocked; the app
// then works for this page load but cannot stay paired.
export function loadToken(store: KV | null): string | null {
  if (store === null) return null;
  try {
    const t = store.getItem(TOKEN_KEY);
    return t === null || t === "" ? null : t;
  } catch {
    return null;
  }
}

export function saveToken(store: KV | null, token: string): boolean {
  if (store === null) return false;
  try {
    store.setItem(TOKEN_KEY, token);
    return true;
  } catch {
    return false;
  }
}

export function clearToken(store: KV | null): void {
  if (store === null) return;
  try {
    store.removeItem(TOKEN_KEY);
  } catch {
    // Storage is unavailable, so there is no token to clear either.
  }
}

export function defaultStorage(): KV | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}

export function pairCodeFromHash(hash: string): string | null {
  const m = /^#pair=([^&]+)/.exec(hash);
  if (m?.[1] === undefined) return null;
  try {
    const code = decodeURIComponent(m[1]).trim();
    return code === "" ? null : code;
  } catch {
    return null;
  }
}

export function wsUrl(loc: { protocol: string; host: string }): string {
  const scheme = loc.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${loc.host}/api/v1/ws`;
}

export function deviceName(ua: string): string {
  if (/iPhone/i.test(ua)) return "iPhone";
  if (/iPad/i.test(ua)) return "iPad";
  if (/Android/i.test(ua)) {
    const model = /Android[^;)]*;\s*([^;)]+?)(?:\sBuild|\)|;|$)/i.exec(ua)?.[1]?.trim();
    // Chrome's reduced UA reports the model as a bare "K".
    return model !== undefined && model !== "" && model !== "K" ? model : "Android phone";
  }
  if (/Windows/i.test(ua)) return "Windows browser";
  if (/Macintosh|Mac OS X/i.test(ua)) return "Mac browser";
  if (/Linux/i.test(ua)) return "Linux browser";
  return "Phone";
}

export type PairResult =
  | { ok: true; response: PairResponse }
  | { ok: false; message: string; retryable: boolean };

export type FetchFn = (input: string, init: RequestInit) => Promise<Response>;

export async function pair(fetchFn: FetchFn, code: string, name: string): Promise<PairResult> {
  let res: Response;
  try {
    res = await fetchFn("/api/v1/pair", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ code, device_name: name }),
    });
  } catch {
    return {
      ok: false,
      message: "Could not reach Deck. Check that you are on the same network.",
      retryable: true,
    };
  }
  if (!res.ok) {
    if (res.status === 429) {
      return {
        ok: false,
        message: "Too many attempts. Wait a minute and try again.",
        retryable: true,
      };
    }
    if (res.status === 400 || res.status === 401 || res.status === 403 || res.status === 404) {
      return {
        ok: false,
        message: "That code is invalid or has expired. Show a new QR code in Deck.",
        retryable: false,
      };
    }
    return { ok: false, message: `Pairing failed (HTTP ${res.status}).`, retryable: true };
  }
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    return { ok: false, message: "Deck sent an unreadable pairing response.", retryable: true };
  }
  if (!isPairResponse(body)) {
    return { ok: false, message: "Deck sent an unexpected pairing response.", retryable: false };
  }
  return { ok: true, response: body };
}
