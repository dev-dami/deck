import {
  clearToken,
  type FetchFn,
  type KV,
  loadToken,
  offerFromInput,
  type PairOffer,
  pair,
  saveToken,
} from "./auth";
import {
  type ChangedFile,
  type Decision,
  type Device,
  type Image,
  MAX_PAGE_LIMIT,
  type MethodName,
  type MethodTable,
  type Mode,
  type ModelChoice,
  parseEvent,
  type QuestionAnswer,
  type RaceEntrant,
  type ServerEvent,
  type Session,
} from "./protocol";
import {
  type Clock,
  type ConnStatus,
  RpcClient,
  RpcError,
  type SocketFactory,
  type StopReason,
} from "./rpc";
import { type Action, type AppState, initialState, reduce } from "./state";
import { applyTheme, type ThemeTarget } from "./theme";

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
  // Where CSS variables go; null in tests without a document.
  theme: ThemeTarget | null;
  clock?: Clock;
  warn?: (msg: string) => void;
}

export function describeError(e: unknown): string {
  if (e instanceof RpcError) {
    if (e.code === "offline") return "You are offline. Reconnecting.";
    if (e.code === "timeout") return "Deck did not answer in time.";
    if (e.code === "bad_response") return "Deck sent an answer this page could not read.";
    return e.message === "" ? `Deck refused the request (${e.code}).` : e.message;
  }
  return e instanceof Error ? e.message : "Something went wrong.";
}

const STOP_MESSAGES: Record<StopReason, string | null> = {
  token_invalid: "Your pairing is no longer valid. Pair this phone again.",
  revoked: "This phone was removed from Deck. Pair it again to continue.",
  protocol: null,
  no_token: null,
};

export class Controller {
  readonly store = new Store();
  private rpc: RpcClient | null = null;
  private openSessionId: string | null = null;
  // The token in use and the install that issued it; a fallback for browsers
  // that refuse localStorage, where pairing lasts this page load.
  private token: { serverId: string; token: string } | null = null;
  private themeVars: string[] = [];
  private readonly d: ControllerDeps;

  constructor(deps: ControllerDeps) {
    this.d = deps;
  }

  get state(): AppState {
    return this.store.state;
  }

  get focus(): string | null {
    return this.openSessionId;
  }

  private warn(msg: string): void {
    (this.d.warn ?? ((m) => console.warn(m)))(msg);
  }

  // Entry point. `offer` is what the QR fragment carried, if anything.
  start(offer: PairOffer | null): void {
    if (offer !== null) {
      this.store.dispatch({ type: "offer", offer, message: null });
      return;
    }
    const stored = loadToken(this.d.storage);
    if (stored !== null) {
      this.token = stored;
      this.connect();
    } else {
      this.store.dispatch({ type: "auth", auth: "unpaired", message: null });
    }
  }

  // A pasted invite line or bare code from the pairing screen.
  submitInput(text: string): void {
    const offer = offerFromInput(text);
    if (offer === null) {
      this.store.dispatch({
        type: "offer",
        offer: null,
        message: "That is not a Deck pairing code or invite.",
      });
      return;
    }
    this.store.dispatch({ type: "offer", offer, message: null });
  }

  cancelOffer(): void {
    this.store.dispatch({ type: "offer", offer: null, message: null });
  }

  // R9: called only after the person confirmed the fingerprint (or there was
  // none to confirm). This is the only path that sends the code.
  async confirmPair(): Promise<void> {
    const offer = this.state.offer;
    if (offer === null || this.state.auth === "pairing") return;
    this.store.dispatch({ type: "auth", auth: "pairing", message: null });
    const res = await pair(this.d.fetchFn, offer.code, this.d.deviceName);
    if (!res.ok) {
      this.store.dispatch({
        type: "offer",
        offer: res.retryable ? offer : null,
        message: res.message,
        retryAt: res.retryAfterMs === null ? null : Date.now() + res.retryAfterMs,
      });
      return;
    }
    const { server_id: serverId, token } = res.response;
    this.token = { serverId, token };
    if (!saveToken(this.d.storage, serverId, token)) {
      this.store.dispatch({
        type: "toast.add",
        kind: "error",
        text: "This browser blocked storage, so you will need to pair again after closing the page.",
      });
    }
    this.store.dispatch({ type: "offer", offer: null, message: null });
    this.connect();
  }

  private connect(): void {
    this.rpc?.stop();
    const rpc = new RpcClient({
      url: this.d.wsUrl,
      getToken: () => this.token?.token ?? null,
      getFocus: () => this.openSessionId,
      socketFactory: this.d.socketFactory,
      ...(this.d.clock === undefined ? {} : { clock: this.d.clock }),
      onStatus: (s) => this.onStatus(s),
      onWelcome: ({ welcome, resync }) => {
        this.store.dispatch({ type: "welcome", server: welcome.server, device: welcome.device });
        this.applyTheme();
        if (resync) void this.refreshAll();
        else void this.refreshOpen();
      },
      onResync: () => void this.refreshAll(),
      onEvent: (_seq, e, p) => this.onEvent(e, p),
      onStopped: (reason) => this.onStopped(reason),
      warn: (m) => this.warn(m),
    });
    this.rpc = rpc;
    this.store.dispatch({ type: "auth", auth: "paired", message: null });
    rpc.start();
  }

  notifyVisible(): void {
    this.rpc?.notifyVisible();
  }

  // Drops the token of the current install and returns to pairing.
  forget(message: string | null = null): void {
    this.rpc?.stop();
    this.rpc = null;
    this.openSessionId = null;
    if (this.token !== null) clearToken(this.d.storage, this.token.serverId);
    this.token = null;
    this.store.dispatch({ type: "reset", auth: "unpaired", message });
  }

  private onStatus(s: ConnStatus): void {
    const link = s === "ready" ? "online" : s === "connecting" ? "connecting" : "offline";
    this.store.dispatch({ type: "link", link });
  }

  private onStopped(reason: StopReason): void {
    if (reason === "protocol") {
      this.rpc = null;
      this.store.dispatch({ type: "auth", auth: "protocol", message: null });
      return;
    }
    this.forget(STOP_MESSAGES[reason]);
  }

  private applyTheme(): void {
    const theme = this.state.theme;
    if (this.d.theme === null || theme === null) return;
    this.themeVars = applyTheme(this.d.theme, theme, this.themeVars);
  }

  private onEvent(e: string, p: unknown): void {
    const ev = parseEvent(e, p);
    if (ev === null) {
      this.warn(`dropped event ${e}: unknown or failed validation`);
      return;
    }
    this.applyEvent(ev);
  }

  // Scoped events (§3.3) belong to the focused session only; one for another
  // session is stale (focus moved while it was in flight) and is dropped.
  private isStaleScoped(ev: ServerEvent): boolean {
    switch (ev.e) {
      case "message.created":
      case "message.updated":
      case "message.deleted":
      case "queue.changed":
      case "notice":
        return ev.p.session_id !== this.openSessionId;
      default:
        return false;
    }
  }

  private applyEvent(ev: ServerEvent): void {
    if (this.isStaleScoped(ev)) {
      this.warn(`dropped scoped event ${ev.e} for a session that is not open`);
      return;
    }
    this.store.dispatch({ type: "event", event: ev });
    switch (ev.e) {
      case "theme.changed":
        this.applyTheme();
        break;
      case "approval.requested":
        this.d.vibrate([60, 40, 60]);
        break;
      case "roster.changed":
        if (ev.p.session_id === this.openSessionId) void this.loadRoster(ev.p.session_id);
        break;
      case "device.revoked":
        // The server follows with close 4403 for this device; nothing to do here.
        break;
      default:
        break;
    }
  }

  private async guard<T>(work: Promise<T>): Promise<T | null> {
    try {
      return await work;
    } catch (e) {
      this.store.dispatch({ type: "toast.add", kind: "error", text: describeError(e) });
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
      // The hello could not carry focus on a fresh epoch; say it again.
      open === null ? Promise.resolve() : this.guard(this.call("sessions.focus", { id: open })),
      open === null ? Promise.resolve() : this.loadSession(open),
    ]);
  }

  // R11: after any reconnect the focused session's messages are refetched,
  // since scoped events are never replayed.
  private async refreshOpen(): Promise<void> {
    const open = this.openSessionId;
    if (open !== null) await this.loadSession(open);
  }

  async loadSessions(): Promise<void> {
    const sessions = await this.guard(this.call("sessions.list", {}));
    if (sessions !== null) this.store.dispatch({ type: "sessions.loaded", sessions });
  }

  async loadApprovals(): Promise<void> {
    const approvals = await this.guard(this.call("approvals.list", {}));
    if (approvals !== null) this.store.dispatch({ type: "approvals.loaded", approvals });
  }

  async loadRoster(sessionId: string): Promise<void> {
    const roster = await this.guard(this.call("roster.get", { session_id: sessionId }));
    if (roster !== null) this.store.dispatch({ type: "roster.loaded", sessionId, roster });
  }

  async loadQueue(sessionId: string): Promise<void> {
    const items = await this.guard(this.call("queue.list", { session_id: sessionId }));
    if (items !== null) this.store.dispatch({ type: "queue.loaded", sessionId, items });
  }

  async loadMode(sessionId: string): Promise<void> {
    const r = await this.guard(this.call("mode.get", { session_id: sessionId }));
    if (r !== null) this.store.dispatch({ type: "mode.loaded", sessionId, mode: r.mode });
  }

  async loadSession(sessionId: string): Promise<void> {
    await Promise.all([
      this.loadMessages(sessionId),
      this.loadQueue(sessionId),
      this.loadRoster(sessionId),
    ]);
  }

  private async loadMessages(sessionId: string): Promise<void> {
    const r = await this.guard(
      this.call("messages.list", { session_id: sessionId, limit: MAX_PAGE_LIMIT }),
    );
    if (r === null) return;
    this.store.dispatch({
      type: "messages.loaded",
      sessionId,
      messages: r.messages,
      more: r.more,
      cursor: r.cursor ?? null,
      latest: true,
    });
  }

  // Fetches the page before the oldest one held; false when there is none.
  async loadOlder(sessionId: string): Promise<boolean> {
    const page = this.state.pages[sessionId];
    if (page === undefined || !page.more || page.cursor === null) return false;
    const r = await this.guard(
      this.call("messages.list", {
        session_id: sessionId,
        before: page.cursor,
        limit: MAX_PAGE_LIMIT,
      }),
    );
    if (r === null) return false;
    this.store.dispatch({
      type: "messages.loaded",
      sessionId,
      messages: r.messages,
      more: r.more,
      cursor: r.cursor ?? null,
      latest: false,
    });
    return r.more;
  }

  // ---- sessions -----------------------------------------------------------

  // The screen opens a session once per visit, whatever the link does in
  // between: a disconnect keeps the focus, so the resume hello carries it and
  // the welcome refetches (R11). Offline, the welcome's refetch sends focus.
  async openSession(sessionId: string): Promise<void> {
    if (this.openSessionId === sessionId) return;
    this.openSessionId = sessionId;
    if (this.rpc?.status !== "ready") return;
    // Focus tells the hub which scoped events this phone wants.
    const focused = await this.guard(this.call("sessions.focus", { id: sessionId }));
    if (focused === null || this.openSessionId !== sessionId) return;
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
    const s = this.state.sessions.find((x) => x.id === id);
    if (s !== undefined) {
      this.store.dispatch({ type: "event", event: { e: "session.deleted", p: s } });
    }
    return true;
  }

  // ---- chat ---------------------------------------------------------------

  async send(sessionId: string, text: string, images: Image[]): Promise<boolean> {
    const req =
      images.length > 0 ? { session_id: sessionId, text, images } : { session_id: sessionId, text };
    try {
      if (this.rpc === null) throw new RpcError("offline", "not connected");
      await this.rpc.sendChat(req);
      return true;
    } catch (e) {
      // Offline fails fast and says so: the draft stays in the composer.
      const text =
        e instanceof RpcError && e.code === "offline"
          ? "You are offline, so the message was not sent. Try again once connected."
          : describeError(e);
      this.store.dispatch({ type: "toast.add", kind: "error", text });
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

  async interrupt(sessionId: string, agentId: string): Promise<void> {
    await this.guard(this.call("agent.interrupt", { session_id: sessionId, agent: agentId }));
  }

  async steer(sessionId: string, itemId: string): Promise<void> {
    await this.guard(this.call("queue.steer", { session_id: sessionId, item_id: itemId }));
  }

  async cancelQueued(sessionId: string, itemId: string): Promise<void> {
    await this.guard(this.call("queue.cancel", { session_id: sessionId, item_id: itemId }));
  }

  // ---- approvals, mode, models -------------------------------------------

  // A question approval takes "answer" with answers, or "deny" to decline it.
  async answerApproval(
    sessionId: string,
    id: string,
    decision: Decision,
    answers?: QuestionAnswer[],
  ): Promise<void> {
    this.d.vibrate(25);
    const r = await this.guard(
      this.call("approvals.answer", {
        session_id: sessionId,
        id,
        decision,
        ...(answers !== undefined ? { answers } : {}),
      }),
    );
    // resolved:false means someone else (the desktop) answered first; either
    // way the approval is no longer waiting on this phone.
    if (r !== null) this.store.dispatch({ type: "approval.dropped", id });
  }

  async setMode(sessionId: string, mode: Mode): Promise<boolean> {
    const r = await this.guard(this.call("mode.set", { session_id: sessionId, mode }));
    if (r === null) return false;
    this.store.dispatch({ type: "mode.loaded", sessionId, mode: r.mode });
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

  // ---- races --------------------------------------------------------------

  // Entrants do brief, each on a worktree of its own; judge has one more
  // agent rank them. The race's task id, or null (toasted) when refused.
  async startRace(
    sessionId: string,
    brief: string,
    entrants: RaceEntrant[],
    judge = false,
  ): Promise<string | null> {
    const params = { session_id: sessionId, brief, entrants };
    const r = await this.guard(this.call("race.start", judge ? { ...params, judge } : params));
    return r?.task_id ?? null;
  }

  // ---- changes and devices ------------------------------------------------

  async changes(sessionId: string): Promise<ChangedFile[] | null> {
    return this.guard(this.call("changes.get", { session_id: sessionId }));
  }

  async loadDevices(): Promise<Device[] | null> {
    const devices = await this.guard(this.call("devices.list", {}));
    if (devices !== null) this.store.dispatch({ type: "devices.loaded", devices });
    return devices;
  }

  // R10: a phone may revoke itself only. The server answers with close 4403.
  async revokeSelf(): Promise<boolean> {
    const id = this.state.device?.id;
    if (id === undefined) return false;
    const r = await this.guard(this.call("devices.revoke", { id }));
    return r !== null;
  }
}
