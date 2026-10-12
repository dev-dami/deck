import { clearToken, type FetchFn, type KV, loadToken, pair, saveToken } from "./auth";
import {
  type ChangedFile,
  type Decision,
  type Device,
  type ImageUpload,
  type MethodName,
  type MethodTable,
  type Mode,
  type ModelChoice,
  parseEvent,
  type ServerEvent,
  type Session,
} from "./protocol";
import { type PushBrowser, PushClient } from "./push-client";
import {
  type Clock,
  type ConnStatus,
  RpcClient,
  RpcError,
  type SocketFactory,
  type StopReason,
} from "./rpc";
import { type Action, type AppState, initialState, reduce } from "./state";

const PAGE_SIZE = 100;

export class Store {
  private current: AppState = initialState;
  private listeners = new Set<() => void>();

  get state(): AppState {
    return this.current;
  }

  dispatch = (a: Action): void => {
    const next = reduce(this.current, a);
    if (next === this.current) return;
    this.current = next;
    for (const l of this.listeners) l();
  };

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
}

export interface ControllerDeps {
  storage: KV | null;
  fetchFn: FetchFn;
  socketFactory: SocketFactory;
  wsUrl: string;
  deviceName: string;
  vibrate: (pattern: number | number[]) => void;
  clock?: Clock;
  // Absent where the page has no push surface (tests, very old browsers).
  pushBrowser?: PushBrowser;
}

function describe(e: unknown): string {
  if (e instanceof RpcError) {
    if (e.code === "offline") return "You are offline. Reconnecting.";
    if (e.code === "timeout") return "Deck did not answer in time.";
    return e.message;
  }
  return e instanceof Error ? e.message : "Something went wrong.";
}

export class Controller {
  readonly store = new Store();
  private rpc: RpcClient | null = null;
  private openSessionId: string | null = null;
  // Fallback for browsers that refuse localStorage: pairing lasts this page load.
  private memToken: string | null = null;
  private readonly d: ControllerDeps;
  readonly push: PushClient | null;

  constructor(deps: ControllerDeps) {
    this.d = deps;
    this.push =
      deps.pushBrowser === undefined
        ? null
        : new PushClient({
            browser: deps.pushBrowser,
            storage: deps.storage,
            api: {
              status: () => this.call("push.status", {}),
              subscribe: async (req) => {
                await this.call("push.subscribe", req);
              },
              unsubscribe: async () => {
                await this.call("push.unsubscribe", {});
              },
              test: async () => {
                await this.call("push.test", {});
              },
            },
            pushKey: () => this.state.server?.push_key ?? "",
            update: (patch) => this.store.dispatch({ type: "push.update", patch }),
            notify: (text, kind) => this.store.dispatch({ type: "notice.add", kind, text }),
            describe,
          });
  }

  get state(): AppState {
    return this.store.state;
  }

  // Entry point. `code` is a pairing code lifted from the URL fragment.
  async start(code: string | null): Promise<void> {
    if (code !== null) {
      await this.pairWith(code);
      return;
    }
    if (loadToken(this.d.storage) !== null) this.connect();
    else this.store.dispatch({ type: "auth", auth: "unpaired", message: null });
  }

  async pairWith(code: string): Promise<void> {
    this.store.dispatch({ type: "auth", auth: "pairing", message: null });
    const res = await pair(this.d.fetchFn, code, this.d.deviceName);
    if (!res.ok) {
      this.store.dispatch({ type: "auth", auth: "unpaired", message: res.message });
      return;
    }
    if (!saveToken(this.d.storage, res.response.token)) {
      this.store.dispatch({
        type: "notice.add",
        kind: "error",
        text: "This browser blocked storage, so you will need to pair again after closing the page.",
      });
    }
    this.connect(res.response.token);
  }

  private connect(tokenOverride?: string): void {
    this.rpc?.stop();
    if (tokenOverride !== undefined) this.memToken = tokenOverride;
    const rpc = new RpcClient({
      url: this.d.wsUrl,
      getToken: () => loadToken(this.d.storage) ?? this.memToken,
      socketFactory: this.d.socketFactory,
      ...(this.d.clock === undefined ? {} : { clock: this.d.clock }),
      onStatus: (s) => this.onStatus(s),
      onWelcome: ({ welcome, resync }) => {
        this.store.dispatch({
          type: "welcome",
          server: welcome.server ?? null,
          device: welcome.device ?? null,
        });
        if (resync) void this.refreshAll();
        void this.push?.sync();
      },
      onResync: () => void this.refreshAll(),
      onEvent: (_seq, e, p) => this.onEvent(e, p),
      onStopped: (reason) => this.onStopped(reason),
    });
    this.rpc = rpc;
    this.store.dispatch({ type: "auth", auth: "paired", message: null });
    rpc.start();
  }

  notifyVisible(): void {
    this.rpc?.notifyVisible();
  }

  forget(message: string | null = null): void {
    void this.push?.forget();
    this.rpc?.stop();
    this.rpc = null;
    this.openSessionId = null;
    this.memToken = null;
    clearToken(this.d.storage);
    this.store.dispatch({ type: "reset", auth: "unpaired", message });
  }

  private onStatus(s: ConnStatus): void {
    const link = s === "ready" ? "online" : s === "connecting" ? "connecting" : "offline";
    this.store.dispatch({ type: "link", link });
  }

  private onStopped(reason: StopReason): void {
    this.forget(
      reason === "revoked"
        ? "This phone was removed from Deck. Pair it again to continue."
        : "Your pairing is no longer valid. Scan the QR code again.",
    );
  }

  private onEvent(e: string, p: unknown): void {
    const ev = parseEvent(e, p);
    if (ev === null) return;
    this.applyEvent(ev);
  }

  private applyEvent(ev: ServerEvent): void {
    if (ev.e === "device.revoked") {
      this.onStopped("revoked");
      return;
    }
    this.store.dispatch({ type: "event", event: ev });
    if (ev.e === "approval.requested") this.d.vibrate([60, 40, 60]);
    if (ev.e === "roster.changed" && ev.p.session_id === this.openSessionId) {
      void this.loadRoster(ev.p.session_id);
    }
  }

  private async guard<T>(work: Promise<T>): Promise<T | null> {
    try {
      return await work;
    } catch (e) {
      this.store.dispatch({ type: "notice.add", kind: "error", text: describe(e) });
      return null;
    }
  }

  private call<M extends MethodName>(
    m: M,
    params: MethodTable[M]["req"],
  ): Promise<MethodTable[M]["res"]> {
    if (this.rpc === null) return Promise.reject(new RpcError("offline", "not connected"));
    return this.rpc.call(m, params);
  }

  // ---- snapshots ----------------------------------------------------------

  async refreshAll(): Promise<void> {
    const open = this.openSessionId;
    await Promise.all([
      this.loadSessions(),
      this.loadApprovals(),
      this.loadMode(),
      open === null ? Promise.resolve() : this.loadSession(open),
    ]);
  }

  async loadSessions(): Promise<void> {
    const sessions = await this.guard(this.call("sessions.list", {}));
    if (sessions !== null) this.store.dispatch({ type: "sessions.loaded", sessions });
  }

  async loadApprovals(): Promise<void> {
    const approvals = await this.guard(this.call("approvals.list", {}));
    if (approvals !== null) this.store.dispatch({ type: "approvals.loaded", approvals });
  }

  async loadMode(): Promise<void> {
    const mode = await this.guard(this.call("mode.get", {}));
    if (mode !== null) this.store.dispatch({ type: "mode.loaded", mode });
  }

  async loadRoster(sessionId: string): Promise<void> {
    const roster = await this.guard(this.call("roster.get", { session_id: sessionId }));
    if (roster !== null) this.store.dispatch({ type: "roster.loaded", sessionId, roster });
  }

  async loadQueue(sessionId: string): Promise<void> {
    const items = await this.guard(this.call("queue.list", { session_id: sessionId }));
    if (items !== null) this.store.dispatch({ type: "queue.loaded", sessionId, items });
  }

  async loadSession(sessionId: string): Promise<void> {
    await Promise.all([
      this.loadMessages(sessionId),
      this.loadQueue(sessionId),
      this.loadRoster(sessionId),
    ]);
  }

  private async loadMessages(sessionId: string): Promise<void> {
    const messages = await this.guard(
      this.call("messages.list", { session_id: sessionId, limit: PAGE_SIZE }),
    );
    if (messages !== null) this.store.dispatch({ type: "messages.loaded", sessionId, messages });
  }

  // Returns false when there is nothing older to fetch.
  async loadOlder(sessionId: string): Promise<boolean> {
    const oldest = this.state.messages[sessionId]?.[0];
    if (oldest === undefined) return false;
    const messages = await this.guard(
      this.call("messages.list", {
        session_id: sessionId,
        before: oldest.created_at,
        limit: PAGE_SIZE,
      }),
    );
    if (messages === null) return false;
    const known = new Set((this.state.messages[sessionId] ?? []).map((m) => m.id));
    const fresh = messages.filter((m) => !known.has(m.id));
    this.store.dispatch({ type: "messages.loaded", sessionId, messages });
    return fresh.length > 0;
  }

  // ---- sessions -----------------------------------------------------------

  async openSession(sessionId: string): Promise<void> {
    if (this.openSessionId === sessionId) return;
    this.openSessionId = sessionId;
    // Focus is advisory (it tells the desktop which session the phone watches).
    void this.guard(this.call("sessions.focus", { id: sessionId }));
    await this.loadSession(sessionId);
  }

  closeSession(): void {
    if (this.openSessionId === null) return;
    this.openSessionId = null;
    if (this.rpc?.status === "ready") {
      void this.guard(this.rpc.call("sessions.focus", { id: "" }));
    }
  }

  async createSession(title?: string): Promise<Session | null> {
    const s = await this.guard(this.call("sessions.create", title === undefined ? {} : { title }));
    if (s !== null) {
      this.store.dispatch({ type: "event", event: { e: "session.created", p: s } });
    }
    return s;
  }

  async renameSession(id: string, title: string): Promise<void> {
    await this.guard(this.call("sessions.rename", { id, title }));
  }

  async deleteSession(id: string): Promise<boolean> {
    const r = await this.guard(this.call("sessions.delete", { id }));
    if (r === null) return false;
    this.store.dispatch({ type: "event", event: { e: "session.deleted", p: { id } } });
    return true;
  }

  // ---- chat ---------------------------------------------------------------

  async send(sessionId: string, text: string, images: ImageUpload[]): Promise<boolean> {
    const req =
      images.length > 0 ? { session_id: sessionId, text, images } : { session_id: sessionId, text };
    try {
      if (this.rpc === null) throw new RpcError("offline", "not connected");
      await this.rpc.sendChat(req);
      return true;
    } catch (e) {
      this.store.dispatch({ type: "notice.add", kind: "error", text: describe(e) });
      return false;
    }
  }

  // The server answers by publishing message.updated, which the reducer applies.
  async editMessage(sessionId: string, messageId: string, text: string): Promise<boolean> {
    const r = await this.guard(
      this.call("message.edit", { session_id: sessionId, message_id: messageId, text }),
    );
    return r !== null;
  }

  async cancel(sessionId: string): Promise<void> {
    await this.guard(this.call("chat.cancel", { session_id: sessionId }));
  }

  async continueRun(sessionId: string): Promise<void> {
    await this.guard(this.call("chat.continue", { session_id: sessionId }));
  }

  async interrupt(sessionId: string, agent: string): Promise<void> {
    await this.guard(this.call("agent.interrupt", { session_id: sessionId, agent }));
  }

  async steer(itemId: string): Promise<void> {
    await this.guard(this.call("queue.steer", { item_id: itemId }));
  }

  async cancelQueued(itemId: string): Promise<void> {
    await this.guard(this.call("queue.cancel", { item_id: itemId }));
  }

  // ---- approvals, mode, models -------------------------------------------

  async answerApproval(id: string, decision: Decision): Promise<void> {
    this.d.vibrate(25);
    const r = await this.guard(this.call("approvals.answer", { id, decision }));
    // resolved:false means someone else (the desktop) answered first.
    if (r !== null) this.store.dispatch({ type: "approval.dropped", id });
  }

  async setMode(mode: Mode): Promise<boolean> {
    const r = await this.guard(this.call("mode.set", { mode }));
    if (r === null) return false;
    // Applied only after the server accepted; mode.changed confirms it.
    this.store.dispatch({ type: "mode.loaded", mode });
    return true;
  }

  async listModels(sessionId: string, kind: string): Promise<ModelChoice[] | null> {
    return this.guard(this.call("models.list", { session_id: sessionId, kind }));
  }

  async setModel(sessionId: string, kind: string, model: string, effort?: string): Promise<void> {
    await this.guard(
      this.call(
        "models.set",
        effort === undefined
          ? { session_id: sessionId, kind, model }
          : { session_id: sessionId, kind, model, effort },
      ),
    );
    await this.loadRoster(sessionId);
  }

  // ---- changes and devices ------------------------------------------------

  async changes(sessionId: string): Promise<ChangedFile[] | null> {
    return this.guard(this.call("changes.get", { session_id: sessionId }));
  }

  async devices(): Promise<Device[] | null> {
    return this.guard(this.call("devices.list", {}));
  }

  async revokeDevice(id: string): Promise<boolean> {
    const r = await this.guard(this.call("devices.revoke", { id }));
    return r !== null;
  }
}
