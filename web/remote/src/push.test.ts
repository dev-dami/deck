import { describe, expect, test } from "bun:test";
import {
  base64UrlToBytes,
  DEFAULT_STORED_PUSH,
  gateMessage,
  isIos,
  type LocalSub,
  loadStoredPush,
  type PushEnv,
  parseStoredPush,
  planSync,
  pushGate,
  sameBytes,
  saveStoredPush,
  toWireSubscription,
} from "./push";

const env: PushEnv = {
  secure: true,
  serviceWorker: true,
  pushManager: true,
  notification: true,
  ios: false,
  standalone: false,
};

describe("pushGate", () => {
  test("a capable page on a push-enabled server is ok", () => {
    expect(pushGate(env, "default", "KEY")).toBe("ok");
    expect(pushGate(env, "granted", "KEY")).toBe("ok");
  });

  test("a server without a key is reported first", () => {
    expect(pushGate({ ...env, secure: false }, "default", "")).toBe("server-off");
  });

  test("plain http is called out before missing APIs", () => {
    const bare = { ...env, secure: false, serviceWorker: false, pushManager: false };
    expect(pushGate(bare, "default", "KEY")).toBe("insecure");
  });

  test("iOS outside the Home Screen needs the install step, even without PushManager", () => {
    const ios = { ...env, ios: true, pushManager: false };
    expect(pushGate(ios, "default", "KEY")).toBe("ios-install");
    expect(pushGate({ ...ios, standalone: true }, "default", "KEY")).toBe("unsupported");
    expect(pushGate({ ...ios, standalone: true, pushManager: true }, "default", "KEY")).toBe("ok");
  });

  test("missing APIs are unsupported; a denied permission is its own state", () => {
    expect(pushGate({ ...env, notification: false }, "default", "KEY")).toBe("unsupported");
    expect(pushGate({ ...env, serviceWorker: false }, "default", "KEY")).toBe("unsupported");
    expect(pushGate(env, "denied", "KEY")).toBe("denied");
  });

  test("every blocked state explains itself", () => {
    expect(gateMessage("ok")).toBe("");
    expect(gateMessage("insecure")).toContain("https");
    expect(gateMessage("ios-install")).toContain("Home Screen");
    expect(gateMessage("denied")).toContain("settings");
    expect(gateMessage("unsupported")).not.toBe("");
    expect(gateMessage("server-off")).not.toBe("");
  });
});

describe("isIos", () => {
  test("detects iPhone, iPad and iPadOS posing as a Mac", () => {
    expect(isIos("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)", 5)).toBe(true);
    expect(isIos("Mozilla/5.0 (iPad; CPU OS 16_4 like Mac OS X)", 5)).toBe(true);
    expect(isIos("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", 5)).toBe(true);
    expect(isIos("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", 0)).toBe(false);
    expect(isIos("Mozilla/5.0 (Linux; Android 14; Pixel 8)", 5)).toBe(false);
  });
});

describe("base64UrlToBytes", () => {
  test("decodes unpadded base64url, including the - and _ alphabet entries", () => {
    expect(base64UrlToBytes("Pz8_Pj4-")).toEqual(new Uint8Array([63, 63, 63, 62, 62, 62]));
    expect(base64UrlToBytes("aGk")).toEqual(new Uint8Array([104, 105]));
    expect(base64UrlToBytes("")).toEqual(new Uint8Array());
  });

  test("rejects characters outside the alphabet and impossible lengths", () => {
    expect(base64UrlToBytes("a b")).toBeNull();
    expect(base64UrlToBytes("a")).toBeNull();
    expect(base64UrlToBytes("ab$d")).toBeNull();
  });

  test("sameBytes compares contents, and treats a missing key as different", () => {
    const k = new Uint8Array([1, 2, 3]);
    expect(sameBytes(k.buffer, new Uint8Array([1, 2, 3]))).toBe(true);
    expect(sameBytes(k.buffer, new Uint8Array([1, 2, 4]))).toBe(false);
    expect(sameBytes(k.buffer, new Uint8Array([1, 2]))).toBe(false);
    expect(sameBytes(null, k)).toBe(false);
  });
});

describe("toWireSubscription", () => {
  test("keeps the endpoint and both keys", () => {
    const json = {
      endpoint: "https://p/1",
      expirationTime: null,
      keys: { p256dh: "P", auth: "A" },
    };
    expect(toWireSubscription(json)).toEqual({
      endpoint: "https://p/1",
      keys: { p256dh: "P", auth: "A" },
    });
  });

  test("an incomplete subscription is refused", () => {
    expect(toWireSubscription(null)).toBeNull();
    expect(toWireSubscription({ endpoint: "https://p/1" })).toBeNull();
    expect(toWireSubscription({ endpoint: "https://p/1", keys: { p256dh: "P" } })).toBeNull();
    expect(toWireSubscription({ endpoint: "", keys: { p256dh: "P", auth: "A" } })).toBeNull();
  });
});

describe("stored preferences", () => {
  test("garbage falls back to defaults", () => {
    expect(parseStoredPush(null)).toEqual(DEFAULT_STORED_PUSH);
    expect(parseStoredPush("{nope")).toEqual(DEFAULT_STORED_PUSH);
    expect(parseStoredPush("[]")).toEqual(DEFAULT_STORED_PUSH);
  });

  test("round trips through storage and survives blocked storage", () => {
    const data = new Map<string, string>();
    const kv = {
      getItem: (k: string) => data.get(k) ?? null,
      setItem: (k: string, v: string) => void data.set(k, v),
      removeItem: (k: string) => void data.delete(k),
    };
    const v = { wanted: true, endpoint: "https://p/1", approvals: false, done: true };
    saveStoredPush(kv, v);
    expect(loadStoredPush(kv)).toEqual(v);
    const blocked = {
      getItem: (): string | null => {
        throw new Error("blocked");
      },
      setItem: (): void => {
        throw new Error("blocked");
      },
      removeItem: (): void => {
        throw new Error("blocked");
      },
    };
    expect(loadStoredPush(blocked)).toEqual(DEFAULT_STORED_PUSH);
    expect(() => saveStoredPush(blocked, v)).not.toThrow();
    expect(loadStoredPush(null)).toEqual(DEFAULT_STORED_PUSH);
  });
});

describe("planSync", () => {
  const on = { available: true, enabled: true, approvals: true, done: true };
  const off = { ...on, enabled: false };
  const stored = { wanted: true, endpoint: "e1", approvals: true, done: true };
  const local: LocalSub = { endpoint: "e1", keyMatches: true };

  test("nothing to do when the server cannot push", () => {
    expect(planSync(local, { ...on, available: false }, stored, "granted")).toBe("none");
  });

  test("in sync means no action", () => {
    expect(planSync(local, on, stored, "granted")).toBe("none");
    expect(planSync(null, off, DEFAULT_STORED_PUSH, "default")).toBe("none");
  });

  test("an expired browser subscription is replaced quietly", () => {
    expect(planSync(null, on, stored, "granted")).toBe("resubscribe");
  });

  test("a server record with no way to deliver is dropped", () => {
    expect(planSync(null, on, stored, "denied")).toBe("unregister");
    expect(planSync(null, on, { ...stored, wanted: false }, "granted")).toBe("unregister");
  });

  test("a rotated endpoint is re-registered", () => {
    expect(planSync({ endpoint: "e2", keyMatches: true }, on, stored, "granted")).toBe("register");
  });

  test("a server that forgot a wanted subscription gets it again", () => {
    expect(planSync(local, off, stored, "granted")).toBe("register");
  });

  test("a leftover browser subscription is not enabled without the user asking", () => {
    expect(planSync(local, off, { ...stored, wanted: false }, "granted")).toBe("none");
  });

  test("a subscription made with an old key is replaced", () => {
    const old = { endpoint: "e1", keyMatches: false };
    expect(planSync(old, on, stored, "granted")).toBe("resubscribe");
    expect(planSync(old, off, { ...stored, wanted: false }, "granted")).toBe("none");
  });
});
