import { describeError, Store } from "@web/controller";
import type { ChangedFile, Decision, Image, Mode, ModelChoice, QuestionAnswer, RaceEntrant, ServerEvent, ServerInfo, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import { applyTheme, type ThemeTarget } from "@web/theme";
import { ProjectApi } from "./project";
import type { DeckBridge, EngineStop, LocalDeck, RpcMethod, RpcStreamEvent } from "../shared/bridge";
import type { DeckClient } from "./client";
import { getPrefs } from "./prefs";
import type { Profile, ProfileScope } from "./profiles";
import { RoomMetaStore } from "./roomMeta";
import {
  type FileList,
  type ImageBytes,
  type SearchHit,
  toApprovals,
  toCapabilities,
  toChanges,
  toEvent,
  toFileList,
  toFiles,
  toImage,
  toMessages,
  toModels,
  toProfiles,
  toQueue,
  toRoom,
  toRooms,
  toRoster,
  toSearchHits,
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
  // The engine stopped by itself; restart() starts it again.
  private halted = false;
  private restarting = false;
  private attempt = 0;
  private retry: ReturnType<typeof setTimeout> | null = null;
  private themeVars: string[] = [];
  private readonly off: () => void;
  // The optional methods the Deck serves, from Info.
  private caps: readonly string[] = [];
  // Each image part asked for, by session, message and index; a failure is
  // forgotten so the next look asks again.
  private readonly images = new Map<string, Promise<ImageBytes | null>>();

  readonly project: ProjectApi;
  readonly rooms = new RoomMetaStore();

  constructor(
    private readonly bridge: DeckBridge,
    readonly deck: LocalDeck,
    private readonly theme: ThemeTarget | null,
    private readonly onEngine: (stop: EngineStop | null) => void = () => {},
  ) {
    this.project = new ProjectApi(bridge, deck.id, (id) => this.ensureLoaded(id));
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

  // One unary call; a failure becomes a toast and null. A quiet call fails
  // with null alone.
  private async call(method: RpcMethod, request: Record<string, unknown>, quiet = false): Promise<unknown> {
    let r: Awaited<ReturnType<DeckBridge["rpcCall"]>>;
    try {
      r = await this.bridge.rpcCall(this.deck.id, method, request);
    } catch (e) {
      if (!quiet) this.toast(describeError(e));
      return null;
    }
    if (r.ok) return r.value;
    if (!quiet) this.toast(r.code === UNAVAILABLE ? "Deck is not reachable." : r.message || `Deck refused ${method}.`);
    return null;
  }

  private async boot(): Promise<void> {
    const info = await this.call("Info", {});
    if (info === null || this.stopped) return;
    this.caps = toCapabilities(info);
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
        if (e.room !== undefined) this.rooms.apply(e.room);
        if (e.k === "event") this.apply(e.event);
        return;
      }
      case "end": {
        // Resume from the last seq after a pause; the Deck still runs.
        this.store.dispatch({ type: "link", link: "offline" });
        const wait = Math.min(BACKOFF_MAX_MS, 500 * 2 ** this.attempt++);
        this.retry = setTimeout(() => this.subscribe(), wait);
        return;
      }
      case "stopped":
        // The project stays open with what it showed; the window offers a restart.
        this.halted = true;
        if (this.retry !== null) clearTimeout(this.retry);
        this.retry = null;
        this.store.dispatch({ type: "link", link: "offline" });
        this.onEngine({ message: ev.message, stderr: ev.stderr });
        return;
      case "exit":
        this.stop();
        this.store.dispatch({ type: "reset", auth: "unpaired", message: ev.message });
        return;
    }
  }

  // Starts the stopped engine in the same folder, then resyncs from scratch:
  // the new process has a new epoch.
  async restart(): Promise<void> {
    if (!this.halted || this.stopped || this.restarting) return;
    this.restarting = true;
    let out: Awaited<ReturnType<DeckBridge["restartFolder"]>>;
    try {
      out = await this.bridge.restartFolder(this.deck.id);
    } catch (e) {
      out = { ok: false, message: describeError(e) };
    }
    this.restarting = false;
    if (this.stopped) return;
    if (!out.ok) {
      this.onEngine({ message: out.message, stderr: [] });
      return;
    }
    this.halted = false;
    this.epoch = "";
    this.seq = 0;
    this.attempt = 0;
    this.onEngine(null);
    await this.boot();
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
    if (r === null) return;
    this.rooms.load(toRooms(r));
    this.store.dispatch({ type: "sessions.loaded", sessions: toSessions(r) });
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

  // Loads a session's latest messages once, without opening it (the side
  // chat reads its own thread beside the open one).
  async ensureLoaded(id: string): Promise<void> {
    if (this.state.pages[id] === undefined) await this.loadSession(id);
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

  async createSession(title?: string, isolate = false): Promise<Session | null> {
    if (isolate && !this.serves("IsolateSession")) return null;
    // The orchestration level of Settings › Agents; a Deck from before
    // levels ignores it.
    const r = await this.call("CreateSession", { title: title ?? "", isolate, level: getPrefs().orchestration });
    const s = toSession(r);
    if (s === null) return null;
    const room = toRoom(r);
    if (room !== null) this.rooms.apply(room);
    this.store.dispatch({ type: "event", event: { e: "session.created", p: s } });
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

  async editMessage(sessionId: string, messageId: string, text: string): Promise<boolean> {
    return (await this.call("EditMessage", { session_id: sessionId, message_id: messageId, text })) !== null;
  }

  async continueRun(sessionId: string): Promise<void> {
    await this.call("Continue", { session_id: sessionId });
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

  async answerApproval(sessionId: string, id: string, decision: Decision, answers?: QuestionAnswer[]): Promise<void> {
    const r = await this.call("Answer", { session_id: sessionId, id, decision, answers: answers ?? [] });
    if (r !== null) this.store.dispatch({ type: "approval.dropped", id });
  }

  async setMode(sessionId: string, mode: Mode): Promise<boolean> {
    if ((await this.call("SetMode", { session_id: sessionId, mode })) === null) return false;
    this.store.dispatch({ type: "mode.loaded", sessionId, mode });
    return true;
  }

  // Quiet: the pickers say themselves when a kind could not be listed.
  async listModels(sessionId: string, kind: string, refresh = false): Promise<ModelChoice[] | null> {
    const r = await this.call("Models", { session_id: sessionId, kind, refresh }, true);
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

  // ---- the room calls: each a no-op when the Deck lacks its method -------

  capabilities(): readonly string[] {
    return this.caps;
  }

  private serves(method: RpcMethod): boolean {
    return this.caps.includes(method);
  }

  // One optional call: null when the Deck does not serve it, as when it fails.
  private optional(method: RpcMethod, request: Record<string, unknown>): Promise<unknown> {
    return this.serves(method) ? this.call(method, request) : Promise.resolve(null);
  }

  async isolateSession(sessionId: string): Promise<boolean> {
    return (await this.optional("IsolateSession", { session_id: sessionId })) !== null;
  }

  async applyIsolated(sessionId: string): Promise<string[] | null> {
    const r = await this.optional("ApplyIsolated", { session_id: sessionId });
    return r === null ? null : toFiles(r);
  }

  async undoApply(sessionId: string): Promise<string[] | null> {
    const r = await this.optional("UndoApply", { session_id: sessionId });
    return r === null ? null : toFiles(r);
  }

  async unisolate(sessionId: string): Promise<boolean> {
    return (await this.optional("UnisolateSession", { session_id: sessionId })) !== null;
  }

  async revertFiles(sessionId: string, paths: string[], since = 0): Promise<string[] | null> {
    const r = await this.optional("RevertFiles", { session_id: sessionId, since, paths });
    return r === null ? null : toFiles(r);
  }

  async undoRevert(sessionId: string): Promise<boolean> {
    return (await this.optional("UndoRevert", { session_id: sessionId })) !== null;
  }

  async archiveSession(sessionId: string): Promise<boolean> {
    return this.markArchived(sessionId, true, await this.optional("ArchiveSession", { session_id: sessionId }));
  }

  async unarchiveSession(sessionId: string): Promise<boolean> {
    return this.markArchived(sessionId, false, await this.optional("UnarchiveSession", { session_id: sessionId }));
  }

  // The room shows its new state at once; session_updated may come later.
  private markArchived(id: string, archived: boolean, answer: unknown): boolean {
    if (answer === null) return false;
    const m = this.rooms.get(id);
    this.rooms.apply({ k: "session", id, attention: m.attention, branch: m.branch, archived });
    return true;
  }

  async startRace(sessionId: string, brief: string, entrants: RaceEntrant[], judge = false): Promise<string | null> {
    // An engine from before entrants reads kind and count: they say the
    // same race when every entrant is one plain kind.
    const first = entrants[0]?.agent ?? "";
    const plain = entrants.every((e) => e.agent === first && e.model === undefined && e.effort === undefined);
    const old = plain ? { kind: first, count: entrants.length } : {};
    const r = await this.optional("StartRace", { session_id: sessionId, brief, entrants, judge, ...old });
    if (r === null) return null;
    const id = (r as Record<string, unknown>)["task_id"];
    return typeof id === "string" ? id : null;
  }

  async listFiles(sessionId: string, query: string, limit = 0): Promise<FileList | null> {
    const r = await this.optional("ListFiles", { session_id: sessionId, query, limit });
    return r === null ? null : toFileList(r);
  }

  readImage(sessionId: string, messageId: string, index: number): Promise<ImageBytes | null> {
    if (!this.serves("ReadImage")) return Promise.resolve(null);
    const key = `${sessionId}/${messageId}/${index}`;
    let p = this.images.get(key);
    if (p === undefined) {
      p = this.call("ReadImage", { session_id: sessionId, message_id: messageId, index }, true).then((r) => {
        const img = r === null ? null : toImage(r);
        if (img === null) this.images.delete(key);
        return img;
      });
      this.images.set(key, p);
    }
    return p;
  }

  async searchMessages(query: string, limit = 0): Promise<SearchHit[] | null> {
    if (!this.serves("SearchMessages")) return null;
    const r = await this.call("SearchMessages", { query, limit }, true);
    return r === null ? null : toSearchHits(r);
  }

  async listProfiles(): Promise<Profile[] | null> {
    if (!this.serves("ListProfiles")) return null;
    const r = await this.call("ListProfiles", {}, true);
    return r === null ? null : toProfiles(r);
  }

  // The Deck checks the profile (SaveProfile); its reason comes back for
  // the editor to show by the fields, not as a toast.
  async saveProfile(p: Profile): Promise<{ ok: true } | { ok: false; message: string }> {
    if (!this.serves("SaveProfile")) return { ok: false, message: "This Deck does not keep profiles." };
    try {
      const r = await this.bridge.rpcCall(this.deck.id, "SaveProfile", { profile: { ...p } });
      if (r.ok) return { ok: true };
      return { ok: false, message: r.code === UNAVAILABLE ? "Deck is not reachable." : r.message || "Deck refused the profile." };
    } catch (e) {
      return { ok: false, message: describeError(e) };
    }
  }

  async deleteProfile(name: string, scope: ProfileScope): Promise<boolean> {
    return (await this.optional("DeleteProfile", { name, scope })) !== null;
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
