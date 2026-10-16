import { expect, test } from "bun:test";
import { b64urlEncode } from "../b64";
import type { ComputerRecord } from "../store/computers";
import { disablePush, subscribePush } from "./subscribe";

const computer: ComputerRecord = {
  cid: "cid",
  name: "PC",
  relayUrl: "https://relay.example",
  lan: [],
  deviceId: "device",
  relayToken: "secret",
  createdAt: "2026-10-15T00:00:00Z",
};

test("subscribe keeps encryption keys inside the E2E RPC and only sends endpoint to relay", async () => {
  const notification = Object.getOwnPropertyDescriptor(globalThis, "Notification");
  const manager = Object.getOwnPropertyDescriptor(globalThis, "PushManager");
  Object.defineProperty(globalThis, "Notification", {
    configurable: true,
    value: { requestPermission: async () => "granted" },
  });
  Object.defineProperty(globalThis, "PushManager", { configurable: true, value: class {} });
  try {
    const key = new Uint8Array(65).fill(4);
    const calls: { url: string; init?: RequestInit }[] = [];
    const rpc: unknown[] = [];
    const subscription = {
      endpoint: "https://fcm.googleapis.com/push",
      options: { applicationServerKey: key.buffer },
      getKey: (name: string) => new Uint8Array(name === "auth" ? 16 : 65).fill(9).buffer,
    };
    const registration = {
      pushManager: { getSubscription: async () => subscription },
    } as unknown as ServiceWorkerRegistration;
    const doFetch = (async (url: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(url), ...(init ? { init } : {}) });
      return new Response(JSON.stringify({ public_key: b64urlEncode(key) }));
    }) as typeof fetch;
    const session = {
      call: async <T>(...args: unknown[]): Promise<T> => {
        rpc.push(args);
        return undefined as T;
      },
    };
    await subscribePush(computer, session, registration, { approvals: true, done: false }, doFetch);
    expect(calls[0]?.url).toBe("https://relay.example/r/v1/vapid");
    expect(calls[1]?.init?.body).toBe(JSON.stringify({ endpoint: subscription.endpoint }));
    expect(calls[1]?.init?.headers).toEqual({
      "content-type": "application/json",
      authorization: "Bearer secret",
    });
    expect(rpc).toEqual([
      [
        "push.register",
        {
          p256dh: b64urlEncode(new Uint8Array(65).fill(9)),
          auth: b64urlEncode(new Uint8Array(16).fill(9)),
          approvals: true,
          done: false,
        },
      ],
    ]);
    await disablePush(session);
    expect(rpc[1]).toEqual([
      "push.register",
      { p256dh: "", auth: "", approvals: false, done: false },
    ]);
    const otherRegistration = {
      pushManager: {
        getSubscription: async () => ({
          ...subscription,
          options: { applicationServerKey: new Uint8Array(65).fill(8).buffer },
        }),
      },
    } as unknown as ServiceWorkerRegistration;
    await expect(
      subscribePush(computer, session, otherRegistration, undefined, doFetch),
    ).rejects.toThrow("another relay");
  } finally {
    if (notification) Object.defineProperty(globalThis, "Notification", notification);
    else Reflect.deleteProperty(globalThis, "Notification");
    if (manager) Object.defineProperty(globalThis, "PushManager", manager);
    else Reflect.deleteProperty(globalThis, "PushManager");
  }
});
