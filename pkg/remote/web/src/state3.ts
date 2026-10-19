// Fleet state for the hosted v3 app (plan §8 B6): one entry per paired
// computer, each holding a live Session3 when its keys are present. The UI
// subscribes and re-renders; this module stays UI-free so bun test covers it.
import { b64urlDecode } from "./b64";
import { decodeInboxItem, type InboxItem, type Resume3, type Welcome3Frame } from "./protocol3";
import { Session3, type SessionEvent } from "./session3";
import { type ComputerRecord, forgetOnRevoke, listComputers } from "./store/computers";
import { openRelayDevice } from "./transport/relay";
import type { Transport } from "./transport/transport";

export type ComputerStatus = "online" | "connecting" | "offline" | "needs-pairing" | "revoked";

export interface ComputerView {
  record: ComputerRecord;
  status: ComputerStatus;
  inbox: InboxItem[];
  projects: Array<{ id: string; name: string; online: boolean }>;
  lastSeen: string | null;
  error: string | null;
}

// The slice of Session3 the fleet uses; real sessions satisfy it, tests fake it.
export interface SessionLike {
  readonly welcome: Welcome3Frame;
  call<T = unknown>(method: string, params?: unknown, pj?: string): Promise<T>;
  // biome-ignore lint/suspicious/noConfusingVoidType: legacy adapters return void, real sessions return a cleanup.
  onEvent(cb: (ev: SessionEvent) => void): void | (() => void);
  // biome-ignore lint/suspicious/noConfusingVoidType: preserve compatibility with existing void-returning adapters.
  onClose(cb: (info: { code: number; reason: string }) => void): void | (() => void);
  close(): void;
  resumePoint?(): Resume3;
}

export type SessionOpener = (rec: ComputerRecord, resume?: Resume3) => Promise<SessionLike>;

export interface StoreHooks {
  list: () => Promise<ComputerRecord[]>;
  forget: (cid: string) => Promise<boolean>;
}

async function defaultOpen(rec: ComputerRecord, resume?: Resume3): Promise<SessionLike> {
  if (rec.relayToken === null || rec.phoneKey === undefined || rec.computerStatic === undefined) {
    throw new Error("this computer needs pairing first");
  }
  const computerStatic = b64urlDecode(rec.computerStatic);
  if (computerStatic === null) throw new Error("this computer needs pairing first");
  const transport: Transport = await openRelayDevice(rec.relayUrl, rec.cid, rec.relayToken);
  try {
    return await Session3.open({
      transport,
      phoneKey: rec.phoneKey,
      computerStatic,
      cid: rec.cid,
      ...(resume !== undefined ? { resume } : {}),
    });
  } catch (err) {
    transport.close();
    throw err;
  }
}

function decodeInboxList(v: unknown): InboxItem[] {
  if (!Array.isArray(v)) return [];
  const out: InboxItem[] = [];
  for (const item of v) {
    const decoded = decodeInboxItem(item);
    if (decoded !== null) out.push(decoded);
    else console.warn("dropping an inbox item that fails validation");
  }
  return out;
}

export interface GroupedInbox {
  needsYou: InboxItem[];
  running: InboxItem[];
  done: InboxItem[];
}

export function groupInbox(items: InboxItem[]): GroupedInbox {
  const needsYou: InboxItem[] = [];
  const running: InboxItem[] = [];
  const done: InboxItem[] = [];
  for (const item of items) {
    if (item.state === "needs_you") needsYou.push(item);
    else if (item.state === "running") running.push(item);
    else done.push(item);
  }
  const byActivity = (a: InboxItem, b: InboxItem): number =>
    a.last_activity < b.last_activity ? 1 : -1;
  needsYou.sort(byActivity);
  running.sort(byActivity);
  done.sort(byActivity);
  return { needsYou, running, done };
}

// "offline 12m" for the greyed snapshot header; null when never seen.
export function offlineAge(lastSeen: string | null, nowMs: number): string | null {
  if (lastSeen === null) return null;
  const at = Date.parse(lastSeen);
  if (Number.isNaN(at)) return null;
  const mins = Math.max(0, Math.floor((nowMs - at) / 60_000));
  if (mins < 1) return "offline just now";
  if (mins < 60) return `offline ${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `offline ${hours}h`;
  return `offline ${Math.floor(hours / 24)}d`;
}

interface LiveComputer {
  view: ComputerView;
  session: SessionLike | null;
  opener: SessionOpener;
  generation: number;
  retry?: ReturnType<typeof setTimeout>;
  attempts: number;
  resume?: Resume3;
}

export class FleetState {
  private computers = new Map<string, LiveComputer>();
  private listeners = new Set<() => void>();
  private readonly open: SessionOpener;
  private readonly store: StoreHooks;
  private reconnect = false;

  startReconnect(): void {
    this.reconnect = true;
    for (const cid of this.computers.keys()) void this.connect(cid);
  }

  stopReconnect(): void {
    this.reconnect = false;
    for (const live of this.computers.values()) clearTimeout(live.retry);
  }

  session(cid: string): SessionLike | null {
    return this.computers.get(cid)?.session ?? null;
  }

  private schedule(cid: string, live: LiveComputer): void {
    if (!this.reconnect) return;
    clearTimeout(live.retry);
    const delay = Math.min(30_000, 1000 * 2 ** Math.min(live.attempts++, 5));
    live.retry = setTimeout(() => void this.connect(cid), delay);
  }

  constructor(
    open: SessionOpener = defaultOpen,
    store: StoreHooks = { list: listComputers, forget: forgetOnRevoke },
  ) {
    this.open = open;
    this.store = store;
  }

  subscribe(cb: () => void): () => void {
    this.listeners.add(cb);
    return () => {
      this.listeners.delete(cb);
    };
  }

  private emit(): void {
    for (const cb of this.listeners) cb();
  }

  views(): ComputerView[] {
    return [...this.computers.values()].map((c) => ({ ...c.view, inbox: [...c.view.inbox] }));
  }

  get(cid: string): ComputerView | null {
    const found = this.computers.get(cid);
    return found === undefined ? null : { ...found.view, inbox: [...found.view.inbox] };
  }

  async reload(): Promise<void> {
    const records = await this.store.list();
    const seen = new Set<string>();
    for (const rec of records) {
      seen.add(rec.cid);
      const live = this.computers.get(rec.cid);
      if (live === undefined) {
        this.computers.set(rec.cid, {
          view: {
            record: rec,
            status: "offline",
            inbox: [],
            projects: [],
            lastSeen: rec.lastSeen ?? null,
            error: null,
          },
          session: null,
          opener: this.open,
          generation: 0,
          attempts: 0,
        });
      } else {
        if (
          live.view.record.deviceId !== rec.deviceId ||
          live.view.record.relayToken !== rec.relayToken ||
          live.view.record.computerStatic !== rec.computerStatic
        ) {
          this.disconnect(rec.cid);
          delete live.resume;
          live.attempts = 0;
          live.view.status = "offline";
          live.view.inbox = [];
          live.view.projects = [];
        }
        live.view.record = rec;
      }
    }
    for (const cid of [...this.computers.keys()]) {
      if (!seen.has(cid)) {
        const live = this.computers.get(cid);
        clearTimeout(live?.retry);
        if (live !== undefined) live.generation++;
        live?.session?.close();
        this.computers.delete(cid);
      }
    }
    this.emit();
  }

  async connect(cid: string): Promise<void> {
    const live = this.computers.get(cid);
    if (live === undefined || live.session !== null || live.view.status === "connecting") return;
    if (live.view.status === "revoked") return;
    clearTimeout(live.retry);
    const generation = ++live.generation;
    const rec = live.view.record;
    if (rec.relayToken === null || rec.phoneKey === undefined || rec.computerStatic === undefined) {
      live.view.status = "needs-pairing";
      live.view.error = null;
      this.emit();
      return;
    }
    live.view.status = "connecting";
    live.view.error = null;
    this.emit();
    let session: SessionLike;
    try {
      session = await live.opener(rec, live.resume);
    } catch (err) {
      if (live.generation !== generation || this.computers.get(cid) !== live) return;
      const code = typeof err === "object" && err !== null && "code" in err ? err.code : undefined;
      if (code === 4401 || code === 4403) {
        await this.onClose(cid, code);
        return;
      }
      live.view.status = "offline";
      live.view.error = err instanceof Error ? err.message : "could not connect";
      this.emit();
      this.schedule(cid, live);
      return;
    }
    if (this.computers.get(cid) !== live || live.generation !== generation) {
      session.close();
      return;
    }
    live.session = session;
    live.attempts = 0;
    live.view.status = "online";
    live.view.lastSeen = new Date().toISOString();
    live.view.projects = session.welcome.server.projects.map((p) => ({
      id: p.id,
      name: p.name,
      online: p.online,
    }));
    session.onEvent((ev) => {
      if (live.session === session) this.onEvent(cid, ev);
    });
    session.onClose((info) => {
      if (live.session === session) void this.onClose(cid, info.code);
    });
    try {
      const items = decodeInboxList(await session.call("inbox.get"));
      if (live.session === session) live.view.inbox = items;
    } catch (err) {
      if (live.session === session)
        live.view.error = err instanceof Error ? err.message : "could not load the inbox";
    }
    this.emit();
  }

  disconnect(cid: string): void {
    const live = this.computers.get(cid);
    if (live === undefined) return;
    clearTimeout(live.retry);
    live.generation++;
    const session = live.session;
    live.session = null;
    session?.close();
    if (live.view.status === "online" || live.view.status === "connecting") {
      live.view.status = "offline";
      live.view.lastSeen = new Date().toISOString();
    }
    this.emit();
  }

  async ack(cids: string, pj: string, sessionId: string): Promise<void> {
    const live = this.computers.get(cids);
    if (live?.session === undefined || live.session === null)
      throw new Error("that computer is offline");
    await live.session.call("inbox.ack", { pj, session_id: sessionId });
    try {
      live.view.inbox = decodeInboxList(await live.session.call("inbox.get"));
    } catch {
      // The ack landed; the refresh is best-effort.
    }
    this.emit();
  }

  private onEvent(cid: string, ev: SessionEvent): void {
    const live = this.computers.get(cid);
    if (live === undefined) return;
    if (ev.name === "inbox.changed") {
      this.onEvent(cid, { ...ev, name: "resync" });
    } else if (ev.name === "resync") {
      const session = live.session;
      if (session === null) return;
      session
        .call("inbox.get")
        .then((v) => {
          const again = this.computers.get(cid);
          if (again !== undefined && again.session === session) {
            again.view.inbox = decodeInboxList(v);
            this.emit();
          }
        })
        .catch(() => undefined);
    }
  }

  private async onClose(cid: string, code: number): Promise<void> {
    const live = this.computers.get(cid);
    if (live === undefined) return;
    const resume = live.session?.resumePoint?.();
    if (resume !== undefined) live.resume = resume;
    live.session = null;
    if (code === 4403) {
      await this.store.forget(cid);
      const records = await this.store.list();
      const rec = records.find((r) => r.cid === cid);
      live.view.record = rec ?? live.view.record;
      live.view.status = "revoked";
      live.view.inbox = [];
    } else if (code === 4401) {
      live.view.status = "needs-pairing";
    } else {
      live.view.status = "offline";
    }
    live.view.lastSeen = new Date().toISOString();
    live.view.error = null;
    this.emit();
    if (code !== 4401 && code !== 4403) this.schedule(cid, live);
  }
}
