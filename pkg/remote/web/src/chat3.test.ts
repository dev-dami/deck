import { describe, expect, test } from "bun:test";
import { ChatSession, newIdempotencyKey } from "./chat3";
import type { Welcome3Frame } from "./protocol3";
import type { SessionEvent } from "./session3";
import type { SessionLike } from "./state3";

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

function msg(id: string, over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id,
    session_id: "ses_1",
    role: "assistant",
    created_at: `2026-10-14T01:00:${id.slice(-2)}Z`,
    updated_at: `2026-10-14T01:00:${id.slice(-2)}Z`,
    finished: true,
    parts: [{ type: "text", text: `hello ${id}` }],
    ...over,
  };
}

class FakeSession implements SessionLike {
  readonly welcome = welcome();
  calls: Array<{ method: string; params: unknown; pj: string | undefined }> = [];
  failNext: Error | null = null;
  failTimes = 0;
  page: unknown = { messages: [], more: false };
  roster: unknown[] = [];
  models: unknown[] = [];
  changes: unknown[] = [];
  pageWait: Promise<void> | null = null;
  rosterWait: Promise<void> | null = null;
  private eventCbs: Array<(ev: SessionEvent) => void> = [];

  async call<T = unknown>(method: string, params?: unknown, pj?: string): Promise<T> {
    this.calls.push({ method, params, pj });
    if (this.failNext !== null && this.failTimes > 0) {
      this.failTimes -= 1;
      throw this.failNext;
    }
    switch (method) {
      case "messages.list":
        await this.pageWait;
        return this.page as unknown as T;
      case "roster.get":
        await this.rosterWait;
        return [...this.roster] as unknown as T;
      case "models.list":
        return [...this.models] as unknown as T;
      case "changes.get":
        return [...this.changes] as unknown as T;
      case "sessions.create":
        return { id: "ses_new" } as unknown as T;
      default:
        return undefined as unknown as T;
    }
  }

  onEvent(cb: (ev: SessionEvent) => void): () => void {
    this.eventCbs.push(cb);
    return () => {
      const index = this.eventCbs.indexOf(cb);
      if (index !== -1) this.eventCbs.splice(index, 1);
    };
  }

  get eventListenerCount(): number {
    return this.eventCbs.length;
  }

  onClose(): void {}

  close(): void {}

  emit(name: string, payload: unknown, pj = "prj_a1"): void {
    for (const cb of this.eventCbs) cb({ seq: 1, name, pj, payload });
  }
}

function collection(methods: string[]): FakeSession {
  const s = new FakeSession();
  s.page = {
    messages: [msg("m01"), msg("m02")],
    more: false,
  };
  void methods;
  return s;
}

describe("chat open", () => {
  test("loads messages, roster and changes", async () => {
    const s = collection([]);
    s.roster = [{ id: "claude-1", name: "Claude", kind: "claude", busy: true }];
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    expect(chat.messages.map((m) => m.id)).toEqual(["m01", "m02"]);
    expect(chat.roster.map((a) => a.id)).toEqual(["claude-1"]);
    expect(chat.more).toBe(false);
    expect(chat.busy).toBe(true);
  });

  test("drops messages that fail validation", async () => {
    const s = collection([]);
    s.page = { messages: [msg("m01"), { bogus: true }], more: false };
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    expect(chat.messages.map((m) => m.id)).toEqual(["m01"]);
  });

  test("bad page throws", async () => {
    const s = collection([]);
    s.page = { nope: true };
    await expect(ChatSession.open(s, "prj_a1", "ses_1")).rejects.toThrow();
    expect(s.eventListenerCount).toBe(0);
  });

  test("repeated navigation releases chat event handlers on detach", async () => {
    const s = collection([]);
    for (let i = 0; i < 10; i++) {
      const chat = await ChatSession.open(s, "prj_a1", "ses_1");
      expect(s.eventListenerCount).toBe(1);
      chat.detach();
      chat.detach();
      expect(s.eventListenerCount).toBe(0);
    }
  });
});

describe("paging", () => {
  test("loadOlder merges and tracks the cursor", async () => {
    const s = collection([]);
    s.page = { messages: [msg("m03"), msg("m04")], more: true, cursor: "c1" };
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.page = { messages: [msg("m01"), msg("m02")], more: false };
    const more = await chat.loadOlder();
    expect(more).toBe(false);
    expect(chat.messages.map((m) => m.id)).toEqual(["m01", "m02", "m03", "m04"]);
    const second = await chat.loadOlder();
    expect(second).toBe(false);
  });
});

describe("send", () => {
  test("sends with an idempotency key and the project", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    await chat.send("  hello  ");
    const sent = s.calls.filter((c) => c.method === "chat.send");
    expect(sent).toHaveLength(1);
    expect(sent[0]?.pj).toBe("prj_a1");
    expect(sent[0]?.params).toMatchObject({ session_id: "ses_1", text: "hello" });
    const params = sent[0]?.params as { idempotency_key?: unknown } | undefined;
    expect(typeof params?.idempotency_key).toBe("string");
  });

  test("blank text never sends", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    await chat.send("   ");
    expect(s.calls.filter((c) => c.method === "chat.send")).toHaveLength(0);
  });

  test("retries timeouts with the same key, then gives up", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    const timeout = Object.assign(new Error("slow"), { code: "timeout" });
    s.failNext = timeout;
    s.failTimes = 5;
    await expect(chat.send("hi")).rejects.toThrow("slow");
    const keys = s.calls
      .filter((c) => c.method === "chat.send")
      .map((c) => (c.params as { idempotency_key?: string }).idempotency_key);
    expect(keys).toHaveLength(3);
    expect(new Set(keys).size).toBe(1);
  });

  test("non-retryable errors throw at once", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.failNext = new Error("forbidden");
    s.failTimes = 5;
    await expect(chat.send("hi")).rejects.toThrow("forbidden");
    expect(s.calls.filter((c) => c.method === "chat.send")).toHaveLength(1);
  });

  test("idempotency keys look unique", () => {
    expect(newIdempotencyKey()).not.toBe(newIdempotencyKey());
  });
});

describe("live events", () => {
  test("live resync replaces stale rows and refreshes roster and changes", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.page = { messages: [msg("m03")], more: true, cursor: "new-cursor" };
    s.roster = [{ id: "codex", name: "Codex", kind: "codex", busy: true }];
    s.changes = [{ path: "new.ts", added: 1, removed: 0 }];
    s.emit("resync", undefined);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(chat.messages.map((message) => message.id)).toEqual(["m03"]);
    expect(chat.roster[0]?.id).toBe("codex");
    expect(chat.busy).toBe(true);
    expect(chat.changes[0]?.path).toBe("new.ts");
    expect(chat.more).toBe(true);
    expect(chat.resyncing).toBe(false);
  });

  test("live updates and deletions win over an in-flight resync snapshot", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    let release = () => {};
    s.pageWait = new Promise<void>((resolve) => {
      release = resolve;
    });
    s.emit("resync", undefined);
    expect(chat.resyncing).toBe(true);
    s.emit("message.deleted", { id: "m01", session_id: "ses_1" });
    s.emit("message.created", msg("m03"));
    release();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(chat.messages.map((message) => message.id)).toEqual(["m02", "m03"]);
  });

  test("failed resync preserves the transcript and exposes an error that a refresh clears", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.page = { invalid: true };
    s.emit("resync", undefined);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(chat.error).toContain("unreadable transcript");
    expect(chat.messages.map((message) => message.id)).toEqual(["m01", "m02"]);
    expect(chat.resyncing).toBe(false);
    s.page = { messages: [msg("m03")], more: false };
    await chat.refresh();
    expect(chat.error).toBeNull();
    expect(chat.messages.map((message) => message.id)).toEqual(["m03"]);
  });

  test("initial snapshot preserves live messages and deletions received during open", async () => {
    const s = collection([]);
    let release = () => {};
    s.pageWait = new Promise<void>((resolve) => {
      release = resolve;
    });
    const opening = ChatSession.open(s, "prj_a1", "ses_1");
    s.emit("message.deleted", { id: "m01", session_id: "ses_1" });
    s.emit(
      "message.updated",
      msg("m02", { updated_at: "2026-10-14T02:00:00Z", parts: [{ type: "text", text: "newer" }] }),
    );
    s.emit("message.created", msg("m03"));
    release();
    const chat = await opening;
    expect(chat.messages.map((message) => message.id)).toEqual(["m02", "m03"]);
    expect(chat.messages[0]?.parts[0]?.text).toBe("newer");
  });

  test("busy events during open take precedence over the roster snapshot", async () => {
    const s = collection([]);
    s.roster = [{ id: "claude-1", name: "Claude", kind: "claude", busy: true }];
    let release = () => {};
    s.pageWait = new Promise<void>((resolve) => {
      release = resolve;
    });
    const opening = ChatSession.open(s, "prj_a1", "ses_1");
    s.emit("busy", { session_id: "ses_1", busy: false });
    release();
    const chat = await opening;
    expect(chat.busy).toBe(false);
  });

  test("a stale roster refresh cannot overwrite a newer busy event", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.roster = [{ id: "claude-1", name: "Claude", kind: "claude", busy: true }];
    let release = () => {};
    s.rosterWait = new Promise<void>((resolve) => {
      release = resolve;
    });
    s.emit("roster.changed", { session_id: "ses_1" });
    s.emit("busy", { session_id: "ses_1", busy: false });
    release();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(chat.busy).toBe(false);
  });

  test("detach stops updates from a closed screen", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    let renders = 0;
    chat.subscribe(() => {
      renders++;
    });
    chat.detach();
    s.emit("message.created", msg("m03"));
    expect(chat.messages.map((message) => message.id)).toEqual(["m01", "m02"]);
    expect(renders).toBe(0);
  });

  test("created, updated and deleted track the open session only", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.emit("message.created", msg("m09", { text: undefined }));
    expect(chat.messages.map((m) => m.id)).toContain("m09");
    s.emit("message.created", { ...msg("m10"), session_id: "other" });
    expect(chat.messages.map((m) => m.id)).not.toContain("m10");
    s.emit("message.deleted", { id: "m09", session_id: "ses_1" });
    expect(chat.messages.map((m) => m.id)).not.toContain("m09");
  });

  test("busy flag follows matching events", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.emit("busy", { session_id: "ses_1", busy: true });
    expect(chat.busy).toBe(true);
    s.emit("busy", { session_id: "other", busy: false });
    expect(chat.busy).toBe(true);
    s.emit("busy", { session_id: "ses_1", busy: false });
    expect(chat.busy).toBe(false);
  });

  test("roster.changed refetches the roster", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    s.roster = [{ id: "codex-1", name: "Codex", kind: "codex", busy: false }];
    s.emit("roster.changed", { session_id: "ses_1" });
    await new Promise((r) => setTimeout(r, 10));
    expect(chat.roster.map((a) => a.id)).toEqual(["codex-1"]);
  });
});

describe("actions", () => {
  test("interrupt targets busy agents", async () => {
    const s = collection([]);
    s.roster = [
      { id: "claude-1", name: "Claude", kind: "claude", busy: true },
      { id: "codex-1", name: "Codex", kind: "codex", busy: false },
    ];
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    await chat.interrupt();
    const calls = s.calls.filter((c) => c.method === "agent.interrupt");
    expect(calls).toHaveLength(1);
    expect(calls[0]?.params).toEqual({ session_id: "ses_1", agent: "claude-1" });
    expect(calls[0]?.pj).toBe("prj_a1");
  });

  test("continue and cancel", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    await chat.cont();
    await chat.cancel();
    expect(s.calls.filter((c) => c.method === "chat.continue")).toHaveLength(1);
    expect(s.calls.filter((c) => c.method === "chat.cancel")).toHaveLength(1);
  });

  test("models list and set", async () => {
    const s = collection([]);
    s.models = [{ id: "opus", label: "Opus" }];
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    expect(await chat.models("claude")).toEqual([{ id: "opus", label: "Opus" }]);
    await chat.setModel("claude", "opus", "high");
    const set = s.calls.filter((c) => c.method === "models.set");
    expect(set[0]?.params).toEqual({
      session_id: "ses_1",
      kind: "claude",
      model: "opus",
      effort: "high",
    });
  });

  test("changes decode, bad rows dropped", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    expect(chat.changes).toEqual([]);
    await chat.refreshChanges();
    expect(chat.changes).toEqual([]);
  });

  test("createSession returns the new id", async () => {
    const s = collection([]);
    const chat = await ChatSession.open(s, "prj_a1", "ses_1");
    await expect(chat.createSession("hello")).resolves.toBe("ses_new");
    expect(s.calls.filter((c) => c.method === "sessions.create")[0]?.pj).toBe("prj_a1");
  });
});
