import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { findAgentClis, findDeck, findOnPath, noAgentCli, parseHandshake } from "../src/main/decks";
import { openLocal } from "../src/renderer/connection";
import type { DeckClient } from "../src/renderer/client";
import type { EngineStop } from "../src/shared/bridge";
import { inProcessBridge } from "./bridge";
import { FakeRpcDeck } from "./fake-grpc";

async function until(cond: () => boolean, ms = 5000): Promise<void> {
  const end = Date.now() + ms;
  while (!cond()) {
    if (Date.now() > end) throw new Error("timed out waiting");
    await new Promise((r) => setTimeout(r, 10));
  }
}

const TOKEN = "ab".repeat(32);

describe("the handshake of deck serve --rpc", () => {
  test("reads the line pkg/rpc prints", () => {
    expect(parseHandshake(`{"v":1,"sock":"/tmp/d/run/deck-1.sock","token":"${TOKEN}","pid":12,"version":"1.2.3"}`)).toEqual({
      v: 1,
      sock: "/tmp/d/run/deck-1.sock",
      token: TOKEN,
      pid: 12,
      version: "1.2.3",
    });
  });

  test("refuses another version, a relative socket, a bad token and other lines", () => {
    const line = (o: object) => JSON.stringify({ v: 1, sock: "/s.sock", token: TOKEN, pid: 1, version: "x", ...o });
    expect(parseHandshake(line({ v: 2 }))).toBeNull();
    expect(parseHandshake(line({ sock: "run/s.sock" }))).toBeNull();
    expect(parseHandshake(line({ token: "short" }))).toBeNull();
    expect(parseHandshake(line({ pid: "1" }))).toBeNull();
    expect(parseHandshake("Deck is starting")).toBeNull();
  });

  test("findDeck prefers DECK_BIN, then the bundled engine, then PATH", () => {
    const node = process.execPath;
    expect(findDeck("/no/such/dir", { PATH: "", DECK_BIN: node })).toBe(node);
    const bundled = mkdtempSync(join(tmpdir(), "deck-bundled-"));
    const exe = join(bundled, process.platform === "win32" ? "deck.exe" : "deck");
    writeFileSync(exe, "#!/bin/sh\n", { mode: 0o755 });
    expect(findDeck(bundled, { PATH: "" })).toBe(exe);
    expect(findDeck("/no/such/dir", { PATH: "" })).toBeNull();
  });

  test("findOnPath finds an executable", () => {
    expect(findOnPath("node")).not.toBeNull();
    expect(findOnPath("no-such-deck-binary")).toBeNull();
  });
});

describe("the agent CLIs", () => {
  test.skipIf(process.platform === "win32")("are looked for on the engine's PATH, in its order", () => {
    const root = mkdtempSync(join(tmpdir(), "deck-clis-"));
    const a = join(root, "a");
    const b = join(root, "b");
    mkdirSync(a);
    mkdirSync(b);
    writeFileSync(join(a, "claude"), "#!/bin/sh\n", { mode: 0o755 });
    writeFileSync(join(b, "claude"), "#!/bin/sh\n", { mode: 0o755 });
    // Antigravity's CLI is agy, Pi's ACP server pi-acp; a file that does not
    // run is no CLI.
    writeFileSync(join(b, "agy"), "#!/bin/sh\n", { mode: 0o755 });
    writeFileSync(join(a, "pi-acp"), "#!/bin/sh\n", { mode: 0o755 });
    writeFileSync(join(b, "codex"), "not a program", { mode: 0o644 });
    mkdirSync(join(b, "opencode"));
    expect(findAgentClis(["/no/such/dir", a, b].join(delimiter))).toEqual([
      { kind: "claude", path: join(a, "claude") },
      { kind: "codex", path: null },
      { kind: "opencode", path: null },
      { kind: "antigravity", path: join(b, "agy") },
      { kind: "pi", path: join(a, "pi-acp") },
    ]);
    expect(findAgentClis("").every((c) => c.path === null)).toBe(true);
  });

  test("the engine's own message says that it found none", () => {
    expect(noAgentCli("Error: none of claude, codex, opencode or antigravity was found on PATH — run `deck doctor`\n")).toBe(true);
    expect(noAgentCli("Error: none of claude, codex, opencode, antigravity or pi was found on PATH — run `deck doctor`\n")).toBe(true);
    expect(noAgentCli("Error: open store: the store is locked")).toBe(false);
    expect(noAgentCli("")).toBe(false);
  });
});

describe("a local Deck over rpc", () => {
  let fake: FakeRpcDeck;
  let dir: string;
  let c: DeckClient | null = null;

  beforeEach(async () => {
    fake = await FakeRpcDeck.start({ tickMs: 20 });
    dir = mkdtempSync(join(tmpdir(), "deck-project-"));
    process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
    delete process.env["DECK_FAKE_STOPPED"];
    delete process.env["DECK_FAKE_EXIT_AFTER"];
  });
  afterEach(async () => {
    c?.forget();
    c = null;
    await fake.close();
  });

  async function open() {
    const bridge = inProcessBridge();
    const out = await bridge.openFolder(dir);
    if (!out.ok) throw new Error(out.message);
    c = openLocal(bridge, out.deck);
    const ctl = c;
    await until(() => ctl.state.link === "online" && ctl.state.sessionsLoaded);
    return { bridge, ctl, deck: out.deck };
  }

  test("starts deck serve --rpc in the folder, lists sessions and remembers the folder", async () => {
    const { bridge, ctl, deck } = await open();
    expect(deck.dir).toBe(dir);
    expect(deck.project).toBe("deck");
    expect(ctl.state.server?.allow_yolo).toBe(true);
    expect(ctl.state.theme?.kinds["claude"]?.tint).toBe("#d97757");
    expect(ctl.state.sessions.map((s) => s.id)).toEqual(expect.arrayContaining(["ses_main", "ses_docs", "ses_long"]));
    expect(ctl.state.approvals.map((a) => a.id)).toEqual(["apr_docs"]);
    expect(await bridge.recentFolders()).toEqual([dir]);
    // A second open of the folder reuses the running Deck.
    const again = await bridge.openFolder(dir);
    expect(again).toEqual({ ok: true, deck });
  });

  test("the token never reaches the window", async () => {
    const { bridge, ctl } = await open();
    await ctl.openSession("ses_main");
    await ctl.send("ses_main", "$claude $codex hello", []);
    await until(() => (ctl.state.messages["ses_main"] ?? []).filter((m) => m.role === "assistant" && m.finished).length >= 4);
    expect(bridge.seen.length).toBeGreaterThan(10);
    expect(JSON.stringify(bridge.seen)).not.toContain(fake.token);
    expect(JSON.stringify(ctl.state)).not.toContain(fake.token);
  });

  test("an engine that finds no agent CLI says so, apart from other start errors", async () => {
    process.env["DECK_FAKE_NO_AGENTS"] = "1";
    try {
      const out = await inProcessBridge().openFolder(dir);
      expect(out).toMatchObject({ ok: false, start: { noAgents: true, lines: [expect.stringContaining("none of claude")] } });
    } finally {
      delete process.env["DECK_FAKE_NO_AGENTS"];
    }
    delete process.env["DECK_FAKE_HANDSHAKE"];
    const out = await inProcessBridge().openFolder(dir);
    expect(out).toMatchObject({ ok: false, start: { noAgents: false, lines: ["fake deck: DECK_FAKE_HANDSHAKE is not set"] } });
  });

  test("a wrong token is refused", async () => {
    process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake.replace(fake.token, "cd".repeat(32));
    const out = await inProcessBridge().openFolder(dir);
    expect(out).toMatchObject({ ok: false, message: expect.stringMatching(/token/) });
  });

  test("two agents stream in one thread", async () => {
    const { ctl } = await open();
    await ctl.openSession("ses_main");
    await until(() => (ctl.state.roster["ses_main"] ?? []).length === 2);
    expect(await ctl.send("ses_main", "$claude $codex compare notes", [])).toBe(true);
    await until(
      () =>
        (ctl.state.messages["ses_main"] ?? []).filter(
          (m) => m.finished && (m.agent === "claude-1" || m.agent === "codex-1") && m.parts.some((p) => (p.text ?? "").includes("compare notes")),
        ).length === 2,
    );
  });

  test("a session in the background updates live", async () => {
    const { ctl } = await open();
    await ctl.openSession("ses_docs");
    await ctl.send("ses_main", "$codex work in the background", []);
    await until(() =>
      (ctl.state.messages["ses_main"] ?? []).some((m) => m.agent === "codex-1" && m.finished && m.parts.some((p) => (p.text ?? "").includes("background"))),
    );
    // No ListMessages for ses_main: the events alone filled it.
    expect(fake.calls.filter((m) => m === "ListMessages")).toHaveLength(1);
  });

  test("answers approvals, sets yolo, pages and interrupts", async () => {
    const { ctl } = await open();
    await ctl.answerApproval("ses_docs", "apr_docs", "deny");
    expect(fake.deck.answered).toEqual([{ id: "apr_docs", decision: "deny" }]);
    expect(await ctl.setMode("ses_main", "yolo")).toBe(true);
    await until(() => ctl.state.sessions.find((s) => s.id === "ses_main")?.mode === "yolo");
    await ctl.openSession("ses_long");
    await until(() => (ctl.state.messages["ses_long"] ?? []).length === 100);
    expect(await ctl.loadOlder("ses_long")).toBe(false);
    expect(ctl.state.messages["ses_long"]).toHaveLength(130);
    await ctl.send("ses_main", "$codex look slowly", []);
    await until(() => (ctl.state.roster["ses_long"] ?? []).length > 0);
    await ctl.interrupt("ses_main", "codex-1");
    expect(fake.deck.interrupted).toEqual(["codex-1"]);
    expect(await ctl.changes("ses_main")).toHaveLength(2);
  });

  test("edits a message and continues a run", async () => {
    const { ctl } = await open();
    await ctl.openSession("ses_main");
    await until(() => (ctl.state.messages["ses_main"] ?? []).length > 0);
    const first = (ctl.state.messages["ses_main"] ?? []).find((m) => m.role === "user");
    if (first === undefined) throw new Error("no user message");
    expect(await ctl.editMessage("ses_main", first.id, "$claude Why only some phones?")).toBe(true);
    expect(fake.deck.edited).toEqual([{ message_id: first.id, text: "$claude Why only some phones?" }]);
    await until(() => (ctl.state.messages["ses_main"] ?? []).some((m) => m.id === first.id && m.edited === true));
    // An unknown message is refused with a toast, not thrown.
    expect(await ctl.editMessage("ses_main", "msg_none", "x")).toBe(false);
    expect(ctl.state.toasts.at(-1)?.text).toBe("no such message");
    await ctl.continueRun("ses_main");
    expect(fake.deck.continued).toEqual(["ses_main"]);
    expect(fake.calls.filter((m) => m === "EditMessage" || m === "Continue")).toEqual(["EditMessage", "EditMessage", "Continue"]);
  });

  test("the room calls reach Deck and the rooms follow its answers and events", async () => {
    fake.room.meta["ses_docs"] = { attention: "approval" };
    const { ctl } = await open();
    const rooms = ctl.rooms;
    if (rooms === undefined) throw new Error("a local Deck keeps rooms");
    expect(ctl.capabilities?.()).toContain("StartRace");
    expect(rooms.get("ses_docs").attention).toBe("approval");

    expect(await ctl.isolateSession?.("ses_main")).toBe(true);
    await until(() => rooms.get("ses_main").branch === "deck/ses_ma");
    expect(await ctl.applyIsolated?.("ses_main")).toEqual(["pkg/remote/auth/limit.go"]);
    expect(await ctl.unisolate?.("ses_main")).toBe(true);
    await until(() => rooms.get("ses_main").branch === "");
    // A refusal carries the engine's words.
    expect(await ctl.applyIsolated?.("ses_main")).toBeNull();
    expect(ctl.state.toasts.at(-1)?.text).toBe("session: this thread is not isolated");

    expect(await ctl.revertFiles?.("ses_main", ["a.go"], 1760000000000)).toEqual(["a.go"]);
    expect(await ctl.undoRevert?.("ses_main")).toBe(true);
    // Entrants mix kinds and repeat one on the same model.
    const mixed = [{ agent: "codex" }, { agent: "claude", model: "opus" }, { agent: "claude", model: "opus", effort: "high" }];
    expect(await ctl.startRace?.("ses_main", "fix the limiter", mixed, true)).toBe("t6");
    expect(await ctl.listFiles?.("ses_main", "limit", 1)).toEqual({ paths: ["pkg/remote/auth/limit.go"], truncated: true });
    expect(await ctl.archiveSession?.("ses_docs")).toBe(true);
    await until(() => rooms.get("ses_docs").archived);

    const s = await ctl.createSession("Isolated", true);
    expect(s).not.toBeNull();
    expect(rooms.get(s?.id ?? "").branch).toMatch(/^deck\//);
    expect(fake.room.asked).toEqual([
      "IsolateSession ses_main",
      "ApplyIsolated ses_main",
      "UnisolateSession ses_main",
      "ApplyIsolated ses_main",
      "RevertFiles ses_main",
      "since 1760000000000",
      "UndoRevert ses_main",
      "StartRace ses_main",
      "race codex,claude/opus,claude/opus/high judged: fix the limiter",
      "ArchiveSession ses_docs",
    ]);
  });

  test("unarchive, undo an apply, an image's bytes and a search across rooms", async () => {
    const { ctl } = await open();
    const rooms = ctl.rooms;
    if (rooms === undefined) throw new Error("a local Deck keeps rooms");
    expect(await ctl.archiveSession?.("ses_docs")).toBe(true);
    // The room shows archived at once, before session_updated.
    expect(rooms.get("ses_docs").archived).toBe(true);
    expect(await ctl.unarchiveSession?.("ses_docs")).toBe(true);
    expect(rooms.get("ses_docs").archived).toBe(false);
    expect(await ctl.unarchiveSession?.("ses_docs")).toBe(false);
    expect(ctl.state.toasts.at(-1)?.text).toBe("this session is not archived");

    expect(await ctl.isolateSession?.("ses_main")).toBe(true);
    expect(await ctl.undoApply?.("ses_main")).toEqual(["pkg/remote/auth/limit.go"]);

    const png = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
    const m = fake.deck.sendImage("ses_docs", "Here is the screen.", "shot.png", "image/png", png);
    const img = await ctl.readImage?.("ses_docs", m.id, 0);
    expect(img?.mime).toBe("image/png");
    expect([...(img?.data ?? [])]).toEqual([...png]);
    // Asked once, then kept.
    await ctl.readImage?.("ses_docs", m.id, 0);
    expect(fake.calls.filter((c) => c === "ReadImage")).toHaveLength(1);
    // Another session's message, or a part that is not there: null, quietly.
    const toasts = ctl.state.toasts.length;
    expect(await ctl.readImage?.("ses_main", m.id, 0)).toBeNull();
    expect(await ctl.readImage?.("ses_docs", m.id, 1)).toBeNull();
    expect(ctl.state.toasts).toHaveLength(toasts);

    const hits = await ctl.searchMessages?.("note 17");
    expect(hits?.map((h) => [h.session_id, h.snippet])).toEqual([["ses_long", "Note 17"]]);
    expect(await ctl.searchMessages?.("no such words")).toEqual([]);
  });

  test("leases and overlap notes land in the room", async () => {
    const { ctl } = await open();
    const rooms = ctl.rooms;
    if (rooms === undefined) throw new Error("a local Deck keeps rooms");
    const lease = { session_id: "ses_main", agent: "claude-1", paths: ["pkg/remote/**"], exclusive: true };
    fake.emit("lease_changed", { ...lease, released: false });
    await until(() => rooms.get("ses_main").leases.length === 1);
    expect(rooms.get("ses_main").leases[0]).toEqual({ agent: "claude-1", paths: ["pkg/remote/**"], exclusive: true });
    fake.emit("notice", { session_id: "ses_main", text: "claude-1 and codex-2 both changed a.go", via: "overlap" });
    await until(() => rooms.get("ses_main").notes.length === 1);
    expect(rooms.get("ses_main").notes[0]?.via).toBe("overlap");
    fake.emit("lease_changed", { ...lease, released: true });
    await until(() => rooms.get("ses_main").leases.length === 0);
  });

  test("against an older Deck the room calls do nothing", async () => {
    fake.room.caps = [];
    const { ctl } = await open();
    const calls = fake.calls.length;
    expect(ctl.capabilities?.()).toEqual([]);
    expect(await ctl.isolateSession?.("ses_main")).toBe(false);
    expect(await ctl.startRace?.("ses_main", "fix it", [{ agent: "claude" }, { agent: "claude" }])).toBeNull();
    expect(await ctl.listFiles?.("ses_main", "")).toBeNull();
    expect(await ctl.createSession("Isolated", true)).toBeNull();
    expect(await ctl.unarchiveSession?.("ses_main")).toBe(false);
    expect(await ctl.undoApply?.("ses_main")).toBeNull();
    expect(await ctl.readImage?.("ses_main", "msg_1", 0)).toBeNull();
    expect(await ctl.searchMessages?.("limiter")).toBeNull();
    expect(fake.calls.length).toBe(calls);
    expect(ctl.state.toasts).toEqual([]);
  });

  test("a dropped event stream resumes and refetches", async () => {
    const { ctl } = await open();
    fake.dropStreams();
    await until(() => ctl.state.link === "offline");
    await until(() => ctl.state.link === "online", 5000);
    expect(fake.calls.filter((m) => m === "Events")).toHaveLength(2);
    expect(fake.calls.filter((m) => m === "ListSessions").length).toBeGreaterThanOrEqual(1);
  });

  test("closing the folder closes stdin and Deck stops", async () => {
    const stopped = join(dir, "stopped");
    process.env["DECK_FAKE_STOPPED"] = stopped;
    const { ctl } = await open();
    ctl.forget();
    c = null;
    await until(() => existsSync(stopped));
    expect(readFileSync(stopped, "utf8")).toBe("stopped");
  });

  test("a Deck that stops keeps the project, shows why and restarts", async () => {
    process.env["DECK_FAKE_EXIT_AFTER"] = "600";
    const bridge = inProcessBridge();
    const out = await bridge.openFolder(dir);
    if (!out.ok) throw new Error(out.message);
    let stop: EngineStop | null = null;
    const ctl = openLocal(bridge, out.deck, (s) => {
      stop = s;
    });
    c = ctl;
    await until(() => ctl.state.sessionsLoaded);
    await until(() => stop !== null, 5000);
    expect(stop).toEqual({ message: "Deck stopped (exit 3).", stderr: ["fake deck: the store is locked", "panic: fake crash"] });
    expect(ctl.state.auth).toBe("paired");
    expect(ctl.state.link).toBe("offline");
    expect(ctl.state.sessions.length).toBeGreaterThan(0);

    delete process.env["DECK_FAKE_EXIT_AFTER"];
    const events = fake.calls.filter((m) => m === "Events").length;
    await ctl.restart?.();
    expect(stop).toBeNull();
    await until(() => ctl.state.link === "online");
    // The same Deck id in the same folder, subscribed again and resynced.
    expect(await bridge.openFolder(dir)).toEqual({ ok: true, deck: out.deck });
    expect(fake.calls.filter((m) => m === "Events").length).toBe(events + 1);
    await until(() => fake.calls.filter((m) => m === "ListSessions").length >= 2);
  });

  test("a restart of a closed Deck is refused", async () => {
    const { bridge, deck } = await open();
    await bridge.closeFolder(deck.id);
    expect(await bridge.restartFolder(deck.id)).toEqual({ ok: false, message: "That Deck is closed." });
  });
});
