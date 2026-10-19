import type { DeckClient } from "../src/renderer/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { isRequest, sealHello } from "../src/main/link";
import { TOKEN_PLACEHOLDER } from "../src/shared/bridge";
import { connect } from "../src/renderer/connection";
import { parsePairText } from "../src/renderer/connect";
import { inProcessBridge } from "./bridge";
import { FakeDeck } from "./fake-server";

async function until(cond: () => boolean, ms = 5000): Promise<void> {
  const end = Date.now() + ms;
  while (!cond()) {
    if (Date.now() > end) throw new Error("timed out waiting");
    await new Promise((r) => setTimeout(r, 10));
  }
}

describe("hello and frame filter", () => {
  test("main writes the real token and keeps only a valid resume", () => {
    const sealed = sealHello(
      JSON.stringify({ t: "hello", v: 2, token: TOKEN_PLACEHOLDER, resume: { epoch: "e", seq: 4, focus: "s", x: 1 }, extra: true }),
      "secret",
    );
    expect(JSON.parse(sealed ?? "")).toEqual({ t: "hello", v: 2, token: "secret", resume: { epoch: "e", seq: 4, focus: "s" } });
    expect(sealHello(JSON.stringify({ t: "hello", v: 1, token: "x" }), "secret")).toBeNull();
    expect(sealHello(JSON.stringify({ t: "req", id: "1", m: "sessions.list" }), "secret")).toBeNull();
    expect(sealHello("not json", "secret")).toBeNull();
  });

  test("after the hello only requests pass", () => {
    expect(isRequest(JSON.stringify({ t: "req", id: "1", m: "sessions.list", p: {} }))).toBe(true);
    expect(isRequest(JSON.stringify({ t: "hello", v: 2, token: "x" }))).toBe(false);
    expect(isRequest("[]")).toBe(false);
  });
});

describe("the protocol client against a fake Deck", () => {
  let fake: FakeDeck;
  let c: DeckClient | null = null;

  beforeEach(async () => {
    fake = await FakeDeck.start({ tickMs: 20 });
  });
  afterEach(async () => {
    c?.forget();
    c = null;
    await fake.close();
  });

  async function pairAndConnect() {
    const bridge = inProcessBridge();
    const parsed = parsePairText(fake.pairLink, "");
    if (!parsed.ok) throw new Error(parsed.message);
    const out = await bridge.pair(parsed.input);
    if (!out.ok) throw new Error(out.message);
    c = connect(bridge, out.server.id);
    const ctl = c;
    await until(() => ctl.state.link === "online" && ctl.state.sessionsLoaded);
    return { bridge, ctl, server: out.server };
  }

  test("pairs, connects and lists sessions; the window never sees the token", async () => {
    const { bridge, ctl, server } = await pairAndConnect();
    expect(server).not.toHaveProperty("token");
    expect(server.persisted).toBe(false);
    expect(await bridge.listServers()).toEqual([server]);
    expect(ctl.state.server?.project).toBe("deck");
    expect(ctl.state.sessions.map((s) => s.id)).toContain("ses_main");
    expect(ctl.state.approvals.map((a) => a.id)).toEqual(["apr_docs"]);
  });

  test("a used code fails with a message and saves nothing", async () => {
    await pairAndConnect();
    const bridge = inProcessBridge();
    const parsed = parsePairText(fake.pairLink.replace(/#pair=.*/, "#pair=WRONG-CODE"), "");
    if (!parsed.ok) throw new Error(parsed.message);
    const out = await bridge.pair(parsed.input);
    expect(out.ok).toBe(false);
    expect(await bridge.listServers()).toEqual([]);
  });

  test("sends once with an idempotency key and streams two agents in one thread", async () => {
    const { ctl } = await pairAndConnect();
    await ctl.openSession("ses_main");
    await until(() => (ctl.state.roster["ses_main"] ?? []).length === 2);
    expect(await ctl.send("ses_main", "$claude $codex+ compare notes", [])).toBe(true);
    expect(fake.sent.map((s) => s.text)).toEqual(["$claude $codex+ compare notes"]);
    await until(() => (ctl.state.roster["ses_main"] ?? []).some((l) => l.id === "codex-2"));
    await until(() => {
      const replies = (ctl.state.messages["ses_main"] ?? []).filter(
        (m) => m.role === "assistant" && m.finished && (m.agent === "claude-1" || m.agent === "codex-2"),
      );
      return replies.filter((m) => m.parts.some((p) => (p.text ?? "").includes("compare notes"))).length === 2;
    });
  });

  test("pages older messages with the cursor", async () => {
    const { ctl } = await pairAndConnect();
    await ctl.openSession("ses_long");
    await until(() => (ctl.state.messages["ses_long"] ?? []).length === 100);
    expect(ctl.state.pages["ses_long"]?.more).toBe(true);
    expect(await ctl.loadOlder("ses_long")).toBe(false);
    expect(ctl.state.messages["ses_long"]).toHaveLength(130);
  });

  test("answers an approval in another session", async () => {
    const { ctl } = await pairAndConnect();
    await ctl.answerApproval("ses_docs", "apr_docs", "allow_session");
    expect(fake.answered).toEqual([{ id: "apr_docs", decision: "allow_session" }]);
    await until(() => ctl.state.approvals.length === 0);
  });

  test("reconnects after a restart, keeps the focus and refetches", async () => {
    const { ctl } = await pairAndConnect();
    await ctl.openSession("ses_main");
    await until(() => (ctl.state.messages["ses_main"] ?? []).length === 3);
    fake.restart();
    await until(() => ctl.state.link !== "online");
    await until(() => ctl.state.link === "online", 10_000);
    expect(await ctl.send("ses_main", "after the restart", [])).toBe(true);
    await until(() => (ctl.state.messages["ses_main"] ?? []).some((m) => m.parts.some((p) => p.text === "after the restart")));
  });

  test("interrupts one agent", async () => {
    const { ctl } = await pairAndConnect();
    await ctl.openSession("ses_main");
    await ctl.send("ses_main", "$codex take a long look", []);
    await until(() => (ctl.state.roster["ses_main"] ?? []).some((l) => l.id === "codex-1" && l.busy));
    await ctl.interrupt("ses_main", "codex-1");
    expect(fake.interrupted).toEqual(["codex-1"]);
    await until(() => (ctl.state.roster["ses_main"] ?? []).every((l) => !l.busy));
  });

  test("a token Deck no longer takes is forgotten in main and the window", async () => {
    const { bridge, ctl } = await pairAndConnect();
    fake.revokeAll();
    await until(() => ctl.state.auth === "unpaired");
    expect(await bridge.listServers()).toEqual([]);
  });
});
