import { describeError, Store } from "@web/controller";
import type { ChangedFile, Decision, Image, Mode, ModelChoice, ServerEvent, ServerInfo, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import { applyTheme, type ThemeTarget } from "@web/theme";
import { ProjectApi } from "./project";
import type { DeckBridge, LocalDeck, RpcMethod, RpcStreamEvent } from "../shared/bridge";
import type { DeckClient } from "./client";
import {
  toApprovals,
  toChanges,
  toEvent,
  toMessages,
  toModels,
  toQueue,
  toRoster,
  toSession,
  toSessions,
  toTheme,
} from "./rpcMap";

const PAGE = 100;
const BACKOFF_MAX_MS = 10_000;
// gRPC status codes the client names.
const UNAVAILABLE = 14;

// What a local Deck can do: everything but device pairing. It allows yolo,
// because the caller is the person at this machine (pkg/rpc SetMode).
const FEATURES = ["images", "edit", "changes", "queue", "models"];

// A Deck on this machine over deck serve --rpc, through main's gRPC client.
// It keeps the same AppState as the v2 Controller with the shared reducer.
// Unlike v2, the event stream carries every session's events, so a thread
// in the background updates live, and it resumes by (epoch, seq).
export class RpcDeckClient implements DeckClient {
  readonly store = new Store();
  private openId: string | null = null;
  private epoch = "";
  private seq = 0;
  private stopped = false;
  private attempt = 0;
  private retry: ReturnType<typeof setTimeout> | null = null;
  private themeVars: string[] = [];
  private readonly off: () => void;

  readonly project: ProjectApi;

  constructor(
    private readonly bridge: DeckBridge,
    readonly deck: LocalDeck,
    private readonly theme: ThemeTarget | null,
  ) {
    this.project = new ProjectApi(bridge, deck.id);
    this.off = bridge.onRpc((id, ev) => {
      if (id === deck.id && !this.stopped) this.onStream(ev);
    });
  }

  get state(): AppState {
    return this.store.state;
  }

  start(): void {
    this.store.dispatch({ type: "auth", auth: "paired", message: null });
    void this.boot();
  }

  private toast(text: string): void {
    this.store.dispatch({ type: "toast.add", kind: "error", text });
  }

  // One unary call; a failure becomes a toast and null.
  private async call(method: RpcMethod, request: Record<string, unknown>): Promise<unknown> {
    let r: Awaited<ReturnType<DeckBridge["rpcCall"]>>;
    try {
      r = await this.bridge.rpcCall(this.deck.id, method, request);
    } catch (e) {
      this.toast(describeError(e));
      return null;
    }
    if (r.ok) return r.value;
    this.toast(r.code === UNAVAILABLE ? "Deck is not reachable." : r.message || `Deck refused ${method}.`);
    return null;
  }

  private async boot(): Promise<void> {
    const info = await this.call("Info", {});
    if (info === null || this.stopped) return;
    const i = info as Record<string, unknown>;
    const server: ServerInfo = {
      version: typeof i["version"] === "string" ? i["version"] : this.deck.version,
      project: typeof i["project"] === "string" ? i["project"] : this.deck.project,
      kinds: Array.isArray(i["kinds"]) ? i["kinds"].filter((k): k is string => typeof k === "string") : [],
      allow_yolo: true,
      features: FEATURES,
      server_id: this.deck.id,
      // Not a protocol v2 server.
      protocol: 0,
      headless: true,
      tunnel: "",
      theme: toTheme(i["theme"]) ?? { palette: {}, kinds: {} },
    };
    this.store.dispatch({
      type: "welcome",
      server,
      device: { id: "local", name: "This computer", expires_at: "9999-12-31T00:00:00Z" },
    });
    this.applyTheme();
    this.subscribe();
  }

  private subscribe(): void {
    this.retry = null;
    void this.bridge.rpcSubscribe(this.deck.id, this.epoch, this.seq);
  }

  private onStream(ev: RpcStreamEvent): void {
    switch (ev.type) {
      case "event": {
        const e = toEvent(ev.event);
        if (e === null) return;
        if (e.k === "ready") {
          const fresh = this.epoch === "" || e.ready.resync;
          this.epoch = e.ready.epoch;
          this.seq = e.ready.seq;
          this.attempt = 0;
          this.store.dispatch({ type: "link", link: "online" });
          void (fresh ? this.refreshAll() : this.refreshOpen());
          return;
        }
        if (e.seq <= this.seq) return;
        this.seq = e.seq;
        this.apply(e.event);
        return;
      }
      case "end": {
        // Resume from the last seq after a pause; the Deck still runs.
        this.store.dispatch({ type: "link", link: "offline" });
        const wait = Math.min(BACKOFF_MAX_MS, 500 * 2 ** this.attempt++);
        this.retry = setTimeout(() => this.subscribe(), wait);
        return;
      }
      case "exit":
        this.stop();
        this.store.dispatch({ type: "reset", auth: "unpaired", message: ev.message });
        return;
    }
  }

  private apply(ev: ServerEvent): void {
    this.store.dispatch({ type: "event", event: ev });
    if (ev.e === "theme.changed") this.applyTheme();
    if (ev.e === "roster.changed" && ev.p.session_id === this.openId) void this.loadRoster(ev.p.session_id);
  }

  private applyTheme(): void {
    const theme = this.state.theme;
    if (this.theme !== null && theme !== null) this.themeVars = applyTheme(this.theme, theme, this.themeVars);
  }

  private async refreshAll(): Promise<void> {
    const open = this.openId;
    await Promise.all([this.loadSessions(), this.loadApprovals(), open === null ? null : this.loadSession(open)]);
  }

  private async refreshOpen(): Promise<void> {
    if (this.openId !== null) await this.loadSession(this.openId);
  }

  private async loadSessions(): Promise<void> {
    const r = await this.call("ListSessions", {});
    if (r !== null) this.store.dispatch({ type: "sessions.loaded", sessions: toSessions(r) });
  }

  private async loadApprovals(): Promise<void> {
    const r = await this.call("Approvals", { session_id: "" });
    if (r !== null) this.store.dispatch({ type: "approvals.loaded", approvals: toApprovals(r) });
  }

  private async loadRoster(sessionId: string): Promise<void> {
    const r = await this.call("Roster", { session_id: sessionId });
    if (r !== null) this.store.dispatch({ type: "roster.loaded", sessionId, roster: toRoster(r) });
  }

  private async loadSession(sessionId: string): Promise<void> {
    const [msgs, queue] = await Promise.all([
      this.call("ListMessages", { session_id: sessionId, limit: PAGE }),
      this.call("Queue", { session_id: sessionId }),
      this.loadRoster(sessionId),
    ]);
    if (msgs !== null) this.loadedPage(sessionId, msgs, true);
    if (queue !== null) this.store.dispatch({ type: "queue.loaded", sessionId, items: toQueue(queue) });
  }

  private loadedPage(sessionId: string, r: unknown, latest: boolean): boolean {
    const page = r as Record<string, unknown>;
    const more = page["more"] === true;
    const cursor = typeof page["cursor"] === "string" && page["cursor"] !== "" ? page["cursor"] : null;
    this.store.dispatch({ type: "messages.loaded", sessionId, messages: toMessages(r), more, cursor, latest });
    return more;
  }

  // ---- DeckClient -----------------------------------------------------------

  async openSession(id: string): Promise<void> {
    if (this.openId === id) return;
    this.openId = id;
    // No focus call: the stream already carries every session's events.
    await this.loadSession(id);
  }

  closeSession(): void {
    this.openId = null;
  }

  async loadOlder(sessionId: string): Promise<boolean> {
    const page = this.state.pages[sessionId];
    if (page === undefined || !page.more || page.cursor === null) return false;
    const r = await this.call("ListMessages", { session_id: sessionId, before: page.cursor, limit: PAGE });
    return r === null ? false : this.loadedPage(sessionId, r, false);
  }

  async createSession(title?: string): Promise<Session | null> {
    const s = toSession(await this.call("CreateSession", { title: title ?? "" }));
    if (s !== null) this.store.dispatch({ type: "event", event: { e: "session.created", p: s } });
    return s;
  }

  async renameSession(id: string, title: string): Promise<void> {
    await this.call("RenameSession", { session_id: id, title });
  }

  async deleteSession(id: string): Promise<boolean> {
    if ((await this.call("DeleteSession", { session_id: id })) === null) return false;
    const s = this.state.sessions.find((x) => x.id === id);
    if (s !== undefined) this.store.dispatch({ type: "event", event: { e: "session.deleted", p: s } });
    return true;
  }

  // Sent once: deck.v1 Send has no idempotency key, so a retry could post
  // the prompt twice. The draft stays in the composer on failure.
  async send(sessionId: string, text: string, images: Image[]): Promise<boolean> {
    const r = await this.call("Send", {
      session_id: sessionId,
      text,
      // Base64 in a bytes field; the proto encoder in main decodes it.
      images: images.map((im) => ({ name: im.name, mime: im.mime, data: im.data_b64 ?? "" })),
    });
    return r !== null;
  }

  async cancel(sessionId: string): Promise<void> {
    await this.call("Cancel", { session_id: sessionId });
  }

  async interrupt(sessionId: string, agentId: string): Promise<void> {
    await this.call("Interrupt", { session_id: sessionId, agent: agentId });
  }

  async steer(sessionId: string, itemId: string): Promise<void> {
    await this.call("Steer", { session_id: sessionId, item_id: itemId });
  }

  async cancelQueued(sessionId: string, itemId: string): Promise<void> {
    await this.call("CancelQueued", { session_id: sessionId, item_id: itemId });
  }

  async answerApproval(sessionId: string, id: string, decision: Decision): Promise<void> {
    const r = await this.call("Answer", { session_id: sessionId, id, decision });
    if (r !== null) this.store.dispatch({ type: "approval.dropped", id });
  }

  async setMode(sessionId: string, mode: Mode): Promise<boolean> {
    if ((await this.call("SetMode", { session_id: sessionId, mode })) === null) return false;
    this.store.dispatch({ type: "mode.loaded", sessionId, mode });
    return true;
  }

  async listModels(sessionId: string, kind: string): Promise<ModelChoice[] | null> {
    const r = await this.call("Models", { session_id: sessionId, kind });
    return r === null ? null : toModels(r);
  }

  async setModel(sessionId: string, kind: string, model: string, effort?: string): Promise<void> {
    await this.call("SetModel", { session_id: sessionId, kind, model, effort: effort ?? "" });
    await this.loadRoster(sessionId);
  }

  async changes(sessionId: string): Promise<ChangedFile[] | null> {
    const r = await this.call("Changes", { session_id: sessionId });
    return r === null ? null : toChanges(r);
  }

  // Closes the folder: main closes the Deck's stdin and it stops.
  forget(message: string | null = null): void {
    this.project.dispose();
    if (!this.stopped) void this.bridge.closeFolder(this.deck.id);
    this.stop();
    this.store.dispatch({ type: "reset", auth: "unpaired", message });
  }

  private stop(): void {
    this.stopped = true;
    if (this.retry !== null) clearTimeout(this.retry);
    this.off();
  }
}
