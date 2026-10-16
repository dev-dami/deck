// One session drill-in over a computer's session (plan §8 B7): the message
// transcript with paging, send, interrupt/continue, models and read-only
// change summaries. UI-free so bun test covers it; the screen renders it.
import {
  type AgentLine,
  bool,
  type ChangedFile,
  type Decoder,
  decodeAgentLine,
  decodeChangedFile,
  decodeMessage,
  decodeMessageDeleted,
  decodeModelChoice,
  type Message,
  type ModelChoice,
  object,
  str,
} from "./protocol";
import type { SessionLike } from "./state3";

const PAGE_LIMIT = 100;

function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  const byId = new Map<string, Message>();
  for (const m of existing) byId.set(m.id, m);
  for (const m of incoming) {
    const cur = byId.get(m.id);
    if (cur === undefined || m.updated_at >= cur.updated_at) byId.set(m.id, m);
  }
  return [...byId.values()].sort((a, b) => (a.created_at < b.created_at ? -1 : 1));
}

function decodeMessageList(v: unknown): Message[] {
  if (!Array.isArray(v)) return [];
  const out: Message[] = [];
  for (const item of v) {
    const decoded = decodeMessage(item);
    if (decoded !== null) out.push(decoded);
    else console.warn("dropping a message that fails validation");
  }
  return out;
}

function decodeAgentList(v: unknown): AgentLine[] {
  if (!Array.isArray(v)) return [];
  const out: AgentLine[] = [];
  for (const item of v) {
    const decoded = decodeAgentLine(item);
    if (decoded !== null) out.push(decoded);
  }
  return out;
}

const decodeSessionId: Decoder<{ id: string }> = object({ id: str }, {});

interface MessagePage {
  messages: Message[];
  more: boolean;
  cursor: string | null;
}

// The envelope decodes strictly but rows decode tolerantly: one corrupt
// message must not hide the whole transcript.
function decodePage(v: unknown): MessagePage | null {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return null;
  const rec = v as Record<string, unknown>;
  const envelope = object({ more: bool }, { cursor: str })(v);
  if (envelope === null) return null;
  return {
    messages: decodeMessageList(rec["messages"]),
    more: envelope.more,
    cursor: envelope.cursor ?? null,
  };
}

export function newIdempotencyKey(): string {
  try {
    if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  } catch {
    // Fall through to the manual key.
  }
  const rand = new Uint8Array(16);
  crypto.getRandomValues(rand);
  return Array.from(rand, (b) => b.toString(16).padStart(2, "0")).join("");
}

export class ChatSession {
  messages: Message[] = [];
  busy = false;
  roster: AgentLine[] = [];
  changes: ChangedFile[] = [];
  more = false;
  error: string | null = null;
  resyncing = false;
  private cursor: string | null = null;
  private listeners = new Set<() => void>();
  private detached = false;
  private unsubscribe: (() => void) | null = null;
  private busyRevision = 0;
  private deletedIds = new Set<string>();
  private refreshRevision = 0;
  private refreshUpdates: Message[] = [];

  private constructor(
    private readonly session: SessionLike,
    readonly pj: string,
    readonly sessionId: string,
  ) {}

  static async open(session: SessionLike, pj: string, sessionId: string): Promise<ChatSession> {
    const chat = new ChatSession(session, pj, sessionId);
    const unsubscribe = session.onEvent((ev) => {
      if (ev.pj === undefined || ev.pj === pj) chat.onEvent(ev.name, ev.payload);
    });
    if (typeof unsubscribe === "function") chat.unsubscribe = unsubscribe;
    try {
      await chat.refresh();
      return chat;
    } catch (error) {
      chat.detach();
      throw error;
    }
  }

  // Resync replaces the old page so missed deletions disappear, while events
  // received during the request still win over the returned snapshot.
  async refresh(): Promise<void> {
    const revision = ++this.refreshRevision;
    const busyRevision = this.busyRevision;
    this.resyncing = true;
    this.refreshUpdates = [];
    this.deletedIds.clear();
    this.error = null;
    this.emit();
    try {
      const [listed, roster] = await Promise.all([
        this.session.call(
          "messages.list",
          { session_id: this.sessionId, limit: PAGE_LIMIT },
          this.pj,
        ),
        this.session
          .call("roster.get", { session_id: this.sessionId }, this.pj)
          .catch(() => [] as unknown),
      ]);
      if (this.detached || revision !== this.refreshRevision) return;
      const page = decodePage(listed);
      if (page === null) throw new Error("the computer sent an unreadable transcript");
      this.messages = mergeMessages(page.messages, this.refreshUpdates).filter(
        (message) => !this.deletedIds.has(message.id),
      );
      this.more = page.more;
      this.cursor = page.cursor;
      this.roster = decodeAgentList(roster);
      if (busyRevision === this.busyRevision) this.busy = this.roster.some((agent) => agent.busy);
      await this.refreshChanges();
    } catch (error) {
      if (revision === this.refreshRevision && !this.detached)
        this.error = error instanceof Error ? error.message : "Could not refresh the conversation.";
      throw error;
    } finally {
      if (revision === this.refreshRevision) {
        this.resyncing = false;
        this.refreshUpdates = [];
        this.emit();
      }
    }
  }

  subscribe(cb: () => void): () => void {
    this.listeners.add(cb);
    return () => {
      this.listeners.delete(cb);
    };
  }

  private emit(): void {
    if (!this.detached) for (const cb of this.listeners) cb();
  }

  detach(): void {
    this.detached = true;
    this.unsubscribe?.();
    this.unsubscribe = null;
    this.listeners.clear();
  }

  async loadOlder(): Promise<boolean> {
    if (!this.more || this.cursor === null) return false;
    const revision = this.refreshRevision;
    const page = decodePage(
      await this.session.call(
        "messages.list",
        { session_id: this.sessionId, before: this.cursor, limit: PAGE_LIMIT },
        this.pj,
      ),
    );
    if (this.detached || revision !== this.refreshRevision) return this.more;
    if (page === null) throw new Error("the computer sent an unreadable transcript");
    this.messages = mergeMessages(this.messages, page.messages).filter(
      (message) => !this.deletedIds.has(message.id),
    );
    this.more = page.more;
    this.cursor = typeof page.cursor === "string" ? page.cursor : this.cursor;
    this.emit();
    return this.more;
  }

  // Retryable sends keep one idempotency key: a timeout or a drop may have
  // delivered the message already (client contract §6).
  async send(text: string, key: string = newIdempotencyKey()): Promise<void> {
    const trimmed = text.trim();
    if (trimmed === "") return;
    const params = { session_id: this.sessionId, text: trimmed, idempotency_key: key };
    let last: unknown = null;
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        await this.session.call("chat.send", params, this.pj);
        return;
      } catch (err) {
        last = err;
        if (!isRetryable(err)) throw err;
      }
    }
    throw last instanceof Error ? last : new Error("could not send");
  }

  async interrupt(): Promise<void> {
    const busy = this.roster.filter((a) => a.busy);
    const targets = busy.length > 0 ? busy : this.roster.slice(0, 1);
    for (const agent of targets) {
      if (agent === undefined) continue;
      await this.session.call(
        "agent.interrupt",
        { session_id: this.sessionId, agent: agent.id },
        this.pj,
      );
    }
  }

  async cont(): Promise<void> {
    await this.session.call("chat.continue", { session_id: this.sessionId }, this.pj);
  }

  async cancel(): Promise<void> {
    await this.session.call("chat.cancel", { session_id: this.sessionId }, this.pj);
  }

  async models(kind: string): Promise<ModelChoice[]> {
    const v = await this.session.call("models.list", { session_id: this.sessionId, kind }, this.pj);
    if (!Array.isArray(v)) return [];
    const out: ModelChoice[] = [];
    for (const item of v) {
      const decoded = decodeModelChoice(item);
      if (decoded !== null) out.push(decoded);
    }
    return out;
  }

  async setModel(kind: string, model: string, effort?: string): Promise<void> {
    const params: Record<string, unknown> = { session_id: this.sessionId, kind, model };
    if (effort !== undefined && effort !== "") params["effort"] = effort;
    await this.session.call("models.set", params, this.pj);
    await this.refreshRoster();
  }

  async refreshChanges(): Promise<void> {
    const revision = this.refreshRevision;
    try {
      const v = await this.session.call(
        "changes.get",
        { session_id: this.sessionId, summary: true },
        this.pj,
      );
      if (!Array.isArray(v) || this.detached || revision !== this.refreshRevision) return;
      const out: ChangedFile[] = [];
      for (const item of v) {
        const decoded = decodeChangedFile(item);
        if (decoded !== null) out.push(decoded);
      }
      this.changes = out;
      this.emit();
    } catch {
      // Changes are decoration; the transcript matters.
    }
  }

  async createSession(title?: string): Promise<string> {
    const params: Record<string, unknown> = {};
    if (title !== undefined && title !== "") params["title"] = title;
    const created = decodeSessionId(await this.session.call("sessions.create", params, this.pj));
    if (created === null) throw new Error("the computer sent an unreadable session");
    return created.id;
  }

  private async refreshRoster(): Promise<void> {
    const revision = this.busyRevision;
    const refreshRevision = this.refreshRevision;
    try {
      const v = await this.session.call("roster.get", { session_id: this.sessionId }, this.pj);
      if (this.detached || refreshRevision !== this.refreshRevision) return;
      this.roster = decodeAgentList(v);
      if (revision === this.busyRevision) this.busy = this.roster.some((agent) => agent.busy);
      this.emit();
    } catch {
      // Stale roster beats no transcript.
    }
  }

  private onEvent(name: string, payload: unknown): void {
    if (this.detached) return;
    if (name === "message.created" || name === "message.updated") {
      const msg = decodeMessage(payload);
      if (msg === null || msg.session_id !== this.sessionId) return;
      this.deletedIds.delete(msg.id);
      if (this.resyncing) this.refreshUpdates = mergeMessages(this.refreshUpdates, [msg]);
      this.messages = mergeMessages(this.messages, [msg]);
      this.emit();
    } else if (name === "message.deleted") {
      const gone = decodeMessageDeleted(payload);
      if (gone === null || gone.session_id !== this.sessionId) return;
      this.deletedIds.add(gone.id);
      this.messages = this.messages.filter((m) => m.id !== gone.id);
      this.emit();
    } else if (name === "busy") {
      if (!isRecord(payload)) return;
      if (payload["session_id"] !== this.sessionId) return;
      if (typeof payload["busy"] === "boolean") {
        this.busyRevision++;
        this.busy = payload["busy"];
        this.emit();
        if (!this.busy) void this.refreshChanges();
      }
    } else if (name === "roster.changed") {
      if (!isRecord(payload) || payload["session_id"] !== this.sessionId) return;
      void this.refreshRoster();
    } else if (name === "resync") {
      // refresh exposes its error to subscribers; consume the rejection here.
      void this.refresh().catch(() => {});
    }
  }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function isRetryable(err: unknown): boolean {
  if (typeof err !== "object" || err === null) return false;
  const code = (err as { code?: unknown }).code;
  return code === "timeout" || code === 1006 || code === 1012;
}
