import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { findDeck, findOnPath, parseHandshake } from "../src/main/decks";
import { openLocal } from "../src/renderer/connection";
import type { DeckClient } from "../src/renderer/client";
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

  test("a wrong token is refused", async () => {
    process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake.replace(fake.token, "cd".repeat(32));
    const out = await inProcessBridge().openFolder(dir);
    expect(out).toMatchObject({ ok: false, message: expect.stringMatching(/token/) });
  });

  test("two agents stream in one thread", async () => {
    const { ctl } = await open();
    await ctl.openSession("ses_main");
    await until(() => (ctl.state.roster["ses_main"] ?? []).length === 2);
    expect(await ctl.send("ses_main", "$claude $codex+ compare notes", [])).toBe(true);
    await until(() => (ctl.state.roster["ses_main"] ?? []).some((l) => l.id === "codex-2"));
    await until(
      () =>
        (ctl.state.messages["ses_main"] ?? []).filter(
          (m) => m.finished && (m.agent === "claude-1" || m.agent === "codex-2") && m.parts.some((p) => (p.text ?? "").includes("compare notes")),
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

  test("a Deck that stops sends the window back to the connect screen", async () => {
    process.env["DECK_FAKE_EXIT_AFTER"] = "600";
    const { ctl } = await open();
    await until(() => ctl.state.auth === "unpaired", 5000);
    expect(ctl.state.authMessage).toMatch(/Deck stopped/);
  });
});
