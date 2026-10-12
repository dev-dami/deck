import { describe, expect, test } from "bun:test";
import type { KV } from "./auth";
import { FakePushBrowser, FakeSub } from "./fakes";
import type { PushStatus, PushSubscribeReq } from "./protocol";
import { loadStoredPush, saveStoredPush } from "./push";
import { initialPushUi, PushClient, type PushUi } from "./push-client";
import { RpcError } from "./rpc";

const KEY = "AQID"; // bytes 1,2,3
const KEY_BYTES = new Uint8Array([1, 2, 3]);

function memory(): KV {
  const data = new Map<string, string>();
  return {
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
}

function setup(over: { status?: PushStatus; key?: string } = {}) {
  const browser = new FakePushBrowser();
  const storage = memory();
  const calls: string[] = [];
  const subscribed: PushSubscribeReq[] = [];
  const notices: Array<[string, string]> = [];
  let ui: PushUi = initialPushUi;
  let status: PushStatus = over.status ?? {
    available: true,
    enabled: false,
    approvals: true,
    done: true,
  };
  const fail: { next: Error | null } = { next: null };
  const maybeFail = () => {
    const e = fail.next;
    fail.next = null;
    if (e !== null) throw e;
  };
  const client = new PushClient({
    browser,
    storage,
    pushKey: () => over.key ?? KEY,
    update: (patch) => {
      ui = { ...ui, ...patch };
    },
    notify: (text, kind) => notices.push([text, kind]),
    describe: (e) => (e instanceof Error ? e.message : "?"),
    api: {
      status: async () => {
        calls.push("status");
        maybeFail();
        return status;
      },
      subscribe: async (req) => {
        calls.push("subscribe");
        maybeFail();
        subscribed.push(req);
        status = { available: true, enabled: true, approvals: req.approvals, done: req.done };
      },
      unsubscribe: async () => {
        calls.push("unsubscribe");
        maybeFail();
        status = { ...status, enabled: false };
      },
      test: async () => {
        calls.push("test");
        maybeFail();
      },
    },
  });
  return {
    browser,
    storage,
    calls,
    subscribed,
    notices,
    client,
    fail,
    ui: () => ui,
    server: () => status,
    setServer: (s: PushStatus) => {
      status = s;
    },
  };
}

describe("PushClient.enable", () => {
  test("asks permission, subscribes with the decoded key, then registers", async () => {
    const t = setup();
    await t.client.enable();
    expect(t.browser.prompts).toBe(1);
    expect(t.browser.made).toBe(1);
    expect(new Uint8Array(t.browser.sub?.applicationServerKey ?? new ArrayBuffer(0))).toEqual(
      KEY_BYTES,
    );
    expect(t.subscribed).toEqual([
      {
        subscription: {
          endpoint: "https://push.example/1",
          keys: { p256dh: "p-https://push.example/1", auth: "a-https://push.example/1" },
        },
        approvals: true,
        done: true,
      },
    ]);
    expect(t.ui().enabled).toBe(true);
    expect(t.ui().busy).toBe(false);
    expect(loadStoredPush(t.storage)).toMatchObject({
      wanted: true,
      endpoint: "https://push.example/1",
    });
  });

  test("a refused prompt leaves nothing subscribed and says so", async () => {
    const t = setup();
    t.browser.answer = "denied";
    await t.client.enable();
    expect(t.browser.made).toBe(0);
    expect(t.calls).toEqual([]);
    expect(t.ui().gate).toBe("denied");
    expect(t.notices[0]?.[1]).toBe("error");
  });

  test("a dismissed prompt is not treated as a block", async () => {
    const t = setup();
    t.browser.answer = "default";
    await t.client.enable();
    expect(t.ui().gate).toBe("ok");
    expect(t.notices).toHaveLength(1);
    expect(t.ui().enabled).toBe(false);
  });

  test("an insecure page never prompts", async () => {
    const t = setup();
    t.browser.environment = { ...t.browser.environment, secure: false };
    await t.client.enable();
    expect(t.browser.prompts).toBe(0);
    expect(t.ui().gate).toBe("insecure");
  });

  test("a server failure is reported and does not mark the phone as wanted", async () => {
    const t = setup();
    t.fail.next = new RpcError("internal", "push is down");
    await t.client.enable();
    expect(t.notices).toEqual([["push is down", "error"]]);
    expect(t.ui().enabled).toBe(false);
    expect(t.ui().busy).toBe(false);
    expect(loadStoredPush(t.storage).wanted).toBe(false);
  });

  test("reuses an existing subscription that has the current key", async () => {
    const t = setup();
    t.browser.perm = "granted";
    t.browser.sub = new FakeSub("https://push.example/old", KEY_BYTES.buffer);
    await t.client.enable();
    expect(t.browser.made).toBe(0);
    expect(t.subscribed[0]?.subscription.endpoint).toBe("https://push.example/old");
  });
});

describe("PushClient preferences, test and disable", () => {
  test("toggling re-registers with the new flags", async () => {
    const t = setup();
    await t.client.enable();
    await t.client.setPrefs({ approvals: false, done: true });
    expect(t.subscribed[1]).toMatchObject({ approvals: false, done: true });
    expect(t.ui()).toMatchObject({ approvals: false, done: true, enabled: true });
    expect(loadStoredPush(t.storage)).toMatchObject({ approvals: false });
  });

  test("send test reports success", async () => {
    const t = setup();
    await t.client.test();
    expect(t.calls).toEqual(["test"]);
    expect(t.notices[0]?.[1]).toBe("info");
  });

  test("disable drops the browser subscription then the server record", async () => {
    const t = setup();
    await t.client.enable();
    const sub = t.browser.sub;
    await t.client.disable();
    expect(sub?.unsubscribed).toBe(true);
    expect(t.calls.at(-1)).toBe("unsubscribe");
    expect(t.ui().enabled).toBe(false);
    expect(loadStoredPush(t.storage)).toMatchObject({ wanted: false, endpoint: null });
  });

  test("if Deck is unreachable the next sync retries instead of re-subscribing", async () => {
    const t = setup();
    await t.client.enable();
    t.fail.next = new RpcError("offline", "not connected");
    await t.client.disable();
    expect(t.ui().enabled).toBe(true);
    expect(t.notices.at(-1)?.[1]).toBe("error");
    await t.client.sync();
    expect(t.calls.at(-1)).toBe("unsubscribe");
    expect(t.browser.made).toBe(1);
    expect(t.ui().enabled).toBe(false);
  });
});

describe("PushClient.sync", () => {
  test("repairs an expired browser subscription without prompting", async () => {
    const t = setup();
    await t.client.enable();
    t.browser.sub = null; // the browser dropped it
    await t.client.sync();
    expect(t.browser.prompts).toBe(1);
    expect(t.browser.made).toBe(2);
    expect(t.subscribed.at(-1)?.subscription.endpoint).toBe("https://push.example/2");
  });

  test("re-registers a rotated endpoint", async () => {
    const t = setup();
    await t.client.enable();
    t.browser.sub = new FakeSub("https://push.example/rotated", KEY_BYTES.buffer);
    await t.client.sync();
    expect(t.browser.made).toBe(1);
    expect(t.subscribed.at(-1)?.subscription.endpoint).toBe("https://push.example/rotated");
    expect(loadStoredPush(t.storage).endpoint).toBe("https://push.example/rotated");
  });

  test("replaces a subscription made with a different server key", async () => {
    const t = setup();
    await t.client.enable();
    const stale = new FakeSub("https://push.example/stale", new Uint8Array([9, 9]).buffer);
    t.browser.sub = stale;
    await t.client.sync();
    expect(stale.unsubscribed).toBe(true);
    expect(t.browser.made).toBe(2);
    expect(t.subscribed.at(-1)?.subscription.endpoint).toBe("https://push.example/2");
  });

  test("a server that lost the record gets it back when the user wanted it", async () => {
    const t = setup();
    await t.client.enable();
    t.setServer({ available: true, enabled: false, approvals: true, done: true });
    await t.client.sync();
    expect(t.subscribed).toHaveLength(2);
    expect(t.ui().enabled).toBe(true);
  });

  test("a revoked permission clears the server record", async () => {
    const t = setup();
    await t.client.enable();
    t.browser.perm = "denied";
    await t.client.sync();
    expect(t.calls.at(-1)).toBe("unsubscribe");
    expect(t.ui()).toMatchObject({ enabled: false, gate: "denied" });
  });

  test("is quiet when offline and does nothing on an unsupported page", async () => {
    const t = setup();
    t.fail.next = new RpcError("offline", "not connected");
    await t.client.sync();
    expect(t.notices).toEqual([]);

    const u = setup();
    u.browser.environment = { ...u.browser.environment, secure: false };
    await u.client.sync();
    expect(u.calls).toEqual([]);
    expect(u.ui()).toMatchObject({ checked: true, gate: "insecure" });
  });

  test("a server without push does nothing", async () => {
    const t = setup({ key: "" });
    await t.client.sync();
    expect(t.calls).toEqual([]);
    expect(t.ui().gate).toBe("server-off");
  });

  test("forgetting the phone clears local state and the browser subscription", async () => {
    const t = setup();
    await t.client.enable();
    const sub = t.browser.sub;
    saveStoredPush(t.storage, { wanted: true, endpoint: "x", approvals: true, done: true });
    await t.client.forget();
    expect(sub?.unsubscribed).toBe(true);
    expect(loadStoredPush(t.storage).wanted).toBe(false);
  });
});
