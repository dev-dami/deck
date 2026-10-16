import { describe, expect, test } from "bun:test";
import type { InboxItem, Welcome3Frame } from "./protocol3";
import type { SessionEvent } from "./session3";
import {
  type ComputerView,
  FleetState,
  groupInbox,
  offlineAge,
  type SessionLike,
  type StoreHooks,
} from "./state3";
import type { ComputerRecord } from "./store/computers";

function welcome(): Welcome3Frame {
  return {
    t: "welcome",
    epoch: "ep1",
    replay: false,
    server: {
      version: "0.9.0",
      cid: "cid_a",
      name: "pc",
      projects: [{ id: "prj_a1", name: "deck", online: true }],
      kinds: ["claude"],
      allow_yolo: false,
      features: ["inbox"],
      protocol: 3,
      tunnel: "",
      theme: {},
    },
    device: { id: "dev1", name: "Pixel", expires_at: "2027-01-01T00:00:00Z" },
  };
}

function item(over: Partial<InboxItem> = {}): InboxItem {
  return {
    pj: "prj_a1",
    session_id: "ses_1",
    title: "Fix bug",
    state: "running",
    approvals: 0,
    last_activity: "2026-10-14T01:00:00Z",
    summary: "working",
    unread: false,
    ...over,
  };
}

function record(over: Partial<ComputerRecord> = {}): ComputerRecord {
  return {
    cid: "cid_a",
    name: "Office",
    relayUrl: "https://relay.example",
    lan: [],
    deviceId: "dev1",
    relayToken: "tok",
    createdAt: "2026-10-14T00:00:00Z",
    computerStatic: "c3RhdGlj",
    phoneKey: { pub: new Uint8Array(32), priv: {} as CryptoKey },
    ...over,
  };
}

class FakeSession implements SessionLike {
  readonly welcome = welcome();
  calls: Array<{ method: string; params: unknown; pj: string | undefined }> = [];
  inbox: InboxItem[] = [];
  failGet: Error | null = null;
  closed = false;
  private eventCbs: Array<(ev: SessionEvent) => void> = [];
  private closeCbs: Array<(info: { code: number; reason: string }) => void> = [];

  async call<T = unknown>(method: string, params?: unknown, pj?: string): Promise<T> {
    this.calls.push({ method, params, pj });
    if (method === "inbox.get") {
      if (this.failGet !== null) throw this.failGet;
      return [...this.inbox] as unknown as T;
    }
    return undefined as unknown as T;
  }

  onEvent(cb: (ev: SessionEvent) => void): void {
    this.eventCbs.push(cb);
  }

  onClose(cb: (info: { code: number; reason: string }) => void): void {
    this.closeCbs.push(cb);
  }

  close(): void {
    this.closed = true;
  }

  emit(name: string, payload: unknown): void {
    for (const cb of this.eventCbs) cb({ seq: 1, name, pj: "prj_a1", payload });
  }

  drop(code: number): void {
    for (const cb of this.closeCbs) cb({ code, reason: "" });
  }
}

function setup(recs: ComputerRecord[] = [record()]): {
  fleet: FleetState;
  sessions: FakeSession[];
  store: StoreHooks & { forgotten: string[] };
} {
  const sessions: FakeSession[] = [];
  const forgotten: string[] = [];
  // Kept by reference so tests can add or remove rows behind the store.
  let rows: ComputerRecord[] = recs;
  const store: StoreHooks & { forgotten: string[] } = {
    forgotten,
    list: async () => [...rows],
    forget: async (cid: string) => {
      forgotten.push(cid);
      rows = rows.map((r) => (r.cid === cid ? { ...r, deviceId: null, relayToken: null } : r));
      return true;
    },
  };
  const fleet = new FleetState(async () => {
    const s = new FakeSession();
    sessions.push(s);
    return s;
  }, store);
  return { fleet, sessions, store };
}

describe("fleet", () => {
  test("automatic reconnect retries a dropped session and stops when disabled", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    fleet.startReconnect();
    sessions[0]?.drop(1012);
    await new Promise((resolve) => setTimeout(resolve, 1100));
    expect(sessions).toHaveLength(2);
    expect(fleet.get("cid_a")?.status).toBe("online");
    sessions[1]?.drop(1012);
    fleet.stopReconnect();
    await new Promise((resolve) => setTimeout(resolve, 1100));
    expect(sessions).toHaveLength(2);
  });
  test("re-pair replaces live credentials and clears a revoked state", async () => {
    const rows = [record()];
    const { fleet, sessions } = setup(rows);
    await fleet.reload();
    await fleet.connect("cid_a");
    const old = sessions[0];
    rows[0] = record({ deviceId: "new-device", relayToken: "new-token" });
    await fleet.reload();
    expect(old?.closed).toBe(true);
    expect(fleet.session("cid_a")).toBeNull();
    await fleet.connect("cid_a");
    expect(fleet.get("cid_a")?.status).toBe("online");
  });
  test("revocation during handshake stops reconnect and forgets credentials", async () => {
    let forgotten = false;
    const fleet = new FleetState(
      async () => {
        throw { code: 4403 };
      },
      {
        list: async () => [record()],
        forget: async () => {
          forgotten = true;
          return true;
        },
      },
    );
    await fleet.reload();
    await fleet.connect("cid_a");
    expect(forgotten).toBe(true);
    expect(fleet.get("cid_a")?.status).toBe("revoked");
  });
  test("a refresh notification refetches without erasing the inbox", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    const s = sessions[0];
    if (!s) throw new Error("missing session");
    s.inbox = [item({ session_id: "fresh" })];
    s.emit("inbox.changed", { unread: false });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fleet.get("cid_a")?.inbox[0]?.session_id).toBe("fresh");
  });

  test("concurrent connect calls share one attempt and disconnect rejects late results", async () => {
    let finish: ((s: SessionLike) => void) | undefined;
    let opens = 0;
    const fleet = new FleetState(
      () => {
        opens++;
        return new Promise((resolve) => {
          finish = resolve;
        });
      },
      { list: async () => [record()], forget: async () => true },
    );
    await fleet.reload();
    const connecting = fleet.connect("cid_a");
    await fleet.connect("cid_a");
    expect(opens).toBe(1);
    fleet.disconnect("cid_a");
    const late = new FakeSession();
    finish?.(late);
    await connecting;
    expect(late.closed).toBe(true);
    expect(fleet.session("cid_a")).toBeNull();
    expect(fleet.get("cid_a")?.status).toBe("offline");
  });

  test("an old session close cannot drop the replacement", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    const first = sessions[0];
    fleet.disconnect("cid_a");
    await fleet.connect("cid_a");
    first?.drop(4403);
    await Promise.resolve();
    expect(fleet.get("cid_a")?.status).toBe("online");
    expect(fleet.session("cid_a")).toBe(sessions[1] ?? null);
  });

  test("reconnect preserves the replay position", async () => {
    const received: unknown[] = [];
    const sessions: FakeSession[] = [];
    const point = { epoch: "ep1", seq: 12 };
    const fleet = new FleetState(
      async (_rec, resume) => {
        received.push(resume);
        const session = Object.assign(new FakeSession(), { resumePoint: () => point });
        sessions.push(session);
        return session;
      },
      { list: async () => [record()], forget: async () => true },
    );
    await fleet.reload();
    await fleet.connect("cid_a");
    sessions[0]?.drop(1006);
    await fleet.connect("cid_a");
    expect(received).toEqual([undefined, point]);
  });
  test("reload lists records as offline and notifies", async () => {
    const { fleet } = setup([record(), record({ cid: "cid_b", name: "Laptop" })]);
    let notes = 0;
    fleet.subscribe(() => {
      notes += 1;
    });
    await fleet.reload();
    const views = fleet.views();
    expect(views.map((v: ComputerView) => v.record.cid)).toEqual(["cid_a", "cid_b"]);
    expect(views.every((v: ComputerView) => v.status === "offline")).toBe(true);
    expect(notes).toBe(1);
  });

  test("connect goes online and loads the inbox", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    expect(sessions).toHaveLength(1);
    expect(fleet.get("cid_a")?.status).toBe("online");
    expect(sessions[0]?.calls.map((c) => c.method)).toEqual(["inbox.get"]);
  });

  test("connect without keys reports needs-pairing", async () => {
    const bare = record({ relayToken: null });
    delete bare.phoneKey;
    delete bare.computerStatic;
    const { fleet, sessions } = setup([bare]);
    await fleet.reload();
    await fleet.connect("cid_a");
    expect(sessions).toHaveLength(0);
    expect(fleet.get("cid_a")?.status).toBe("needs-pairing");
  });

  test("connect failure reports offline with the error", async () => {
    const { fleet } = setup();
    await fleet.reload();
    const failing = new FleetState(
      async () => {
        throw new Error("no route");
      },
      { list: async () => [record()], forget: async () => true },
    );
    await failing.reload();
    await failing.connect("cid_a");
    expect(failing.get("cid_a")?.status).toBe("offline");
    expect(failing.get("cid_a")?.error).toBe("no route");
  });

  test("inbox.changed replaces the list, resync refetches", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    const s = sessions[0];
    if (s === undefined) throw new Error("no session");
    s.inbox = [item({ session_id: "s9", state: "needs_you", approvals: 1, unread: true })];
    s.emit("inbox.changed", [item({ session_id: "s9", state: "needs_you", approvals: 1 })]);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fleet.get("cid_a")?.inbox.map((i) => i.session_id)).toEqual(["s9"]);
    expect(fleet.get("cid_a")?.inbox[0]?.unread).toBe(true);
    s.inbox = [item({ session_id: "s10" })];
    s.emit("resync", null);
    await Promise.resolve();
    await new Promise((r) => setTimeout(r, 10));
    expect(fleet.get("cid_a")?.inbox.map((i) => i.session_id)).toEqual(["s10"]);
  });

  test("close 4403 revokes and forgets, 4401 needs pairing, other drops offline", async () => {
    const { fleet, sessions, store } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    sessions[0]?.drop(4403);
    await new Promise((r) => setTimeout(r, 10));
    expect(store.forgotten).toEqual(["cid_a"]);
    expect(fleet.get("cid_a")?.status).toBe("revoked");
    expect(fleet.get("cid_a")?.inbox).toEqual([]);
  });

  test("close 4401 needs pairing, close 1006 goes offline with a stamp", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    sessions[0]?.drop(4401);
    await new Promise((r) => setTimeout(r, 10));
    expect(fleet.get("cid_a")?.status).toBe("needs-pairing");
  });

  test("ack calls inbox.ack then refreshes", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    const s = sessions[0];
    if (s === undefined) throw new Error("no session");
    s.inbox = [item({ session_id: "done-1" })];
    await fleet.ack("cid_a", "prj_a1", "ses_1");
    expect(s.calls.map((c) => c.method)).toEqual(["inbox.get", "inbox.ack", "inbox.get"]);
    expect(s.calls[1]?.params).toEqual({ pj: "prj_a1", session_id: "ses_1" });
    expect(fleet.get("cid_a")?.inbox.map((i) => i.session_id)).toEqual(["done-1"]);
  });

  test("ack offline throws", async () => {
    const { fleet } = setup();
    await fleet.reload();
    await expect(fleet.ack("cid_a", "prj_a1", "ses_1")).rejects.toThrow();
  });

  test("disconnect closes the session and stamps offline", async () => {
    const { fleet, sessions } = setup();
    await fleet.reload();
    await fleet.connect("cid_a");
    fleet.disconnect("cid_a");
    expect(sessions[0]?.closed).toBe(true);
    expect(fleet.get("cid_a")?.status).toBe("offline");
    expect(fleet.get("cid_a")?.lastSeen).not.toBeNull();
  });

  test("reload drops removed computers and closes their sessions", async () => {
    const rows = [record()];
    const { fleet, sessions } = setup(rows);
    await fleet.reload();
    await fleet.connect("cid_a");
    rows.length = 0;
    await fleet.reload();
    expect(fleet.get("cid_a")).toBeNull();
    expect(sessions[0]?.closed).toBe(true);
  });
});

describe("groupInbox", () => {
  test("groups by state and sorts newest first", () => {
    const g = groupInbox([
      item({ session_id: "old-run", state: "running", last_activity: "2026-10-14T01:00:00Z" }),
      item({ session_id: "new-run", state: "running", last_activity: "2026-10-14T02:00:00Z" }),
      item({ session_id: "need", state: "needs_you", approvals: 2 }),
      item({ session_id: "done", state: "done" }),
    ]);
    expect(g.needsYou.map((i) => i.session_id)).toEqual(["need"]);
    expect(g.running.map((i) => i.session_id)).toEqual(["new-run", "old-run"]);
    expect(g.done.map((i) => i.session_id)).toEqual(["done"]);
  });
});

describe("offlineAge", () => {
  test("buckets minutes, hours, days", () => {
    const now = Date.parse("2026-10-14T12:00:00Z");
    expect(offlineAge(null, now)).toBeNull();
    expect(offlineAge("bogus", now)).toBeNull();
    expect(offlineAge("2026-10-14T11:59:30Z", now)).toBe("offline just now");
    expect(offlineAge("2026-10-14T11:48:00Z", now)).toBe("offline 12m");
    expect(offlineAge("2026-10-14T09:00:00Z", now)).toBe("offline 3h");
    expect(offlineAge("2026-10-11T12:00:00Z", now)).toBe("offline 3d");
  });
});
