import type { KV } from "./auth";
import type { PushStatus, PushSubscribeReq } from "./protocol";
import {
  base64UrlToBytes,
  clearStoredPush,
  type LocalSub,
  loadStoredPush,
  type PushEnv,
  type PushGate,
  type PushPermission,
  planSync,
  pushGate,
  sameBytes,
  saveStoredPush,
  toWireSubscription,
} from "./push";
import { isTransient, RpcError } from "./rpc";

// What the UI shows. `enabled` is the server's view of this phone.
export interface PushUi {
  // False until the first look at the browser, so the UI does not flash a verdict.
  checked: boolean;
  gate: PushGate;
  enabled: boolean;
  approvals: boolean;
  done: boolean;
  busy: boolean;
}

export const initialPushUi: PushUi = {
  checked: false,
  gate: "server-off",
  enabled: false,
  approvals: true,
  done: true,
  busy: false,
};

export interface BrowserSub {
  endpoint: string;
  // PushSubscription.toJSON()
  json: unknown;
  applicationServerKey: ArrayBuffer | null;
  unsubscribe(): Promise<boolean>;
}

// The browser surface push needs, so the flow can be driven by fakes in tests.
export interface PushBrowser {
  env(): PushEnv;
  permission(): PushPermission;
  requestPermission(): Promise<PushPermission>;
  current(): Promise<BrowserSub | null>;
  subscribe(key: Uint8Array<ArrayBuffer>): Promise<BrowserSub>;
}

export interface PushApi {
  status(): Promise<PushStatus>;
  subscribe(req: PushSubscribeReq): Promise<void>;
  unsubscribe(): Promise<void>;
  test(): Promise<void>;
}

export interface PushClientDeps {
  browser: PushBrowser;
  api: PushApi;
  storage: KV | null;
  pushKey: () => string;
  update: (patch: Partial<PushUi>) => void;
  notify: (text: string, kind: "info" | "error") => void;
  describe: (e: unknown) => string;
}

// A dropped connection during a background sync is routine; the next welcome retries.
function isOfflineError(e: unknown): boolean {
  return e instanceof RpcError && (e.code === "offline" || isTransient(e));
}

export class PushClient {
  private working = false;
  private readonly d: PushClientDeps;

  constructor(deps: PushClientDeps) {
    this.d = deps;
  }

  private gate(): PushGate {
    return pushGate(this.d.browser.env(), this.d.browser.permission(), this.d.pushKey());
  }

  // Local-only: re-reads support and permission, which can change while the page is open.
  refresh(): PushGate {
    const gate = this.gate();
    this.d.update({ checked: true, gate });
    return gate;
  }

  // Reconciles the browser subscription with the server's record. Runs on every
  // (re)connect, so a subscription the browser expired or rotated is repaired.
  async sync(): Promise<void> {
    const gate = this.refresh();
    if (gate !== "ok" && gate !== "denied") return;
    if (this.working) return;
    this.working = true;
    try {
      await this.reconcile(gate);
    } catch (e) {
      if (!isOfflineError(e)) this.d.notify(this.d.describe(e), "error");
    } finally {
      this.working = false;
    }
  }

  private async reconcile(gate: PushGate): Promise<void> {
    const status = await this.d.api.status();
    const stored = loadStoredPush(this.d.storage);
    this.d.update({ enabled: status.enabled, approvals: status.approvals, done: status.done });

    const key = base64UrlToBytes(this.d.pushKey());
    if (key === null) return;
    // A denied permission makes any browser subscription useless.
    const sub = gate === "denied" ? null : await this.d.browser.current();
    const local: LocalSub | null =
      sub === null
        ? null
        : {
            endpoint: sub.endpoint,
            keyMatches:
              sub.applicationServerKey === null || sameBytes(sub.applicationServerKey, key),
          };

    const action = planSync(local, status, stored, this.d.browser.permission());
    const prefs = status.enabled
      ? { approvals: status.approvals, done: status.done }
      : { approvals: stored.approvals, done: stored.done };

    switch (action) {
      case "none":
        if (status.enabled) saveStoredPush(this.d.storage, { ...stored, ...prefs });
        return;
      case "register": {
        if (sub === null) return;
        await this.register(sub, prefs);
        return;
      }
      case "resubscribe": {
        const fresh = await this.freshSub(sub, key);
        await this.register(fresh, prefs);
        return;
      }
      case "unregister":
        await this.d.api.unsubscribe();
        saveStoredPush(this.d.storage, { ...stored, wanted: false, endpoint: null });
        this.d.update({ enabled: false });
        return;
    }
  }

  // Must be called straight from a tap: requestPermission is the first await so
  // the browser still sees the user gesture.
  async enable(): Promise<void> {
    if (this.working) return;
    if (this.gate() !== "ok") {
      this.refresh();
      return;
    }
    this.working = true;
    this.d.update({ busy: true });
    try {
      const permission = await this.d.browser.requestPermission();
      if (permission !== "granted") {
        this.refresh();
        this.d.notify(
          permission === "denied"
            ? "Notifications are blocked for this page."
            : "Notifications were not allowed.",
          "error",
        );
        return;
      }
      const key = base64UrlToBytes(this.d.pushKey());
      if (key === null) {
        this.d.notify("Deck sent a notification key this browser cannot read.", "error");
        return;
      }
      const stored = loadStoredPush(this.d.storage);
      const sub = await this.freshSub(await this.d.browser.current(), key, true);
      await this.register(sub, { approvals: stored.approvals, done: stored.done });
    } catch (e) {
      this.d.notify(this.d.describe(e), "error");
    } finally {
      this.working = false;
      this.d.update({ busy: false });
    }
  }

  async setPrefs(prefs: { approvals: boolean; done: boolean }): Promise<void> {
    if (this.working) return;
    this.working = true;
    this.d.update({ busy: true });
    try {
      const key = base64UrlToBytes(this.d.pushKey());
      if (key === null) return;
      const sub = await this.freshSub(await this.d.browser.current(), key, true);
      await this.register(sub, prefs);
    } catch (e) {
      this.d.notify(this.d.describe(e), "error");
    } finally {
      this.working = false;
      this.d.update({ busy: false });
    }
  }

  async test(): Promise<void> {
    if (this.working) return;
    this.working = true;
    this.d.update({ busy: true });
    try {
      await this.d.api.test();
      this.d.notify("Test notification sent.", "info");
    } catch (e) {
      this.d.notify(this.d.describe(e), "error");
    } finally {
      this.working = false;
      this.d.update({ busy: false });
    }
  }

  async disable(): Promise<void> {
    if (this.working) return;
    this.working = true;
    this.d.update({ busy: true });
    try {
      const stored = loadStoredPush(this.d.storage);
      // Recorded first: if Deck cannot be reached below, the next sync must retry
      // telling it rather than quietly re-subscribing this phone.
      saveStoredPush(this.d.storage, { ...stored, wanted: false, endpoint: null });
      const sub = await this.d.browser.current();
      if (sub !== null) await sub.unsubscribe();
      await this.d.api.unsubscribe();
      this.d.update({ enabled: false });
    } catch (e) {
      this.d.notify(this.d.describe(e), "error");
    } finally {
      this.working = false;
      this.d.update({ busy: false });
    }
  }

  // Called when this phone is unpaired: stop the browser pushing for a Deck it
  // no longer belongs to, and forget local preferences.
  async forget(): Promise<void> {
    clearStoredPush(this.d.storage);
    try {
      const sub = await this.d.browser.current();
      if (sub !== null) await sub.unsubscribe();
    } catch (e) {
      console.warn("could not drop the push subscription", e);
    }
  }

  // Reuses a subscription made with the current key; otherwise replaces it.
  // `reuse` lets callers that already have a valid one skip the replacement.
  private async freshSub(
    existing: BrowserSub | null,
    key: Uint8Array<ArrayBuffer>,
    reuse = false,
  ): Promise<BrowserSub> {
    if (existing !== null) {
      const keyOk =
        existing.applicationServerKey === null || sameBytes(existing.applicationServerKey, key);
      if (reuse && keyOk) return existing;
      await existing.unsubscribe();
    }
    return this.d.browser.subscribe(key);
  }

  private async register(
    sub: BrowserSub,
    prefs: { approvals: boolean; done: boolean },
  ): Promise<void> {
    const wire = toWireSubscription(sub.json);
    if (wire === null) throw new Error("This browser returned an incomplete push subscription.");
    await this.d.api.subscribe({ subscription: wire, ...prefs });
    saveStoredPush(this.d.storage, { wanted: true, endpoint: wire.endpoint, ...prefs });
    this.d.update({ enabled: true, approvals: prefs.approvals, done: prefs.done });
  }
}
