import { describe, expect, test } from "bun:test";
import {
  clearToken,
  deviceName,
  type KV,
  loadToken,
  pair,
  pairCodeFromHash,
  saveToken,
  wsUrl,
} from "./auth";
import { activeAgent, applyAgentChip, canSaveEdit, canSend } from "./composer";
import { base64Length, checkImage } from "./images";
import { availableModes, modeLabel, needsConfirm } from "./modes";
import { MAX_IMAGE_BYTES } from "./protocol";
import { formatRoute, parseRoute } from "./route";
import {
  appIsInView,
  clickUrl,
  notificationOptions,
  parsePushPayload,
  planClick,
  shellPaths,
  strategyFor,
} from "./sw-logic";
import { relativeTime, toMillis } from "./time";
import { groupParts, isNearBottom } from "./transcript";

function memory(): KV & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => {
      data.set(k, v);
    },
    removeItem: (k) => {
      data.delete(k);
    },
  };
}

const throwing: KV = {
  getItem: () => {
    throw new Error("blocked");
  },
  setItem: () => {
    throw new Error("blocked");
  },
  removeItem: () => {
    throw new Error("blocked");
  },
};

describe("token storage", () => {
  test("round trip", () => {
    const m = memory();
    expect(loadToken(m)).toBeNull();
    expect(saveToken(m, "dev1.secret")).toBe(true);
    expect(loadToken(m)).toBe("dev1.secret");
    clearToken(m);
    expect(loadToken(m)).toBeNull();
  });

  test("blocked storage never throws", () => {
    expect(loadToken(throwing)).toBeNull();
    expect(saveToken(throwing, "t")).toBe(false);
    expect(() => clearToken(throwing)).not.toThrow();
    expect(loadToken(null)).toBeNull();
    expect(saveToken(null, "t")).toBe(false);
  });
});

describe("pairing", () => {
  test("reads the code from the fragment", () => {
    expect(pairCodeFromHash("#pair=abc123")).toBe("abc123");
    expect(pairCodeFromHash("#pair=a%20b")).toBe("a b");
    expect(pairCodeFromHash("#pair=")).toBeNull();
    expect(pairCodeFromHash("#/s/1")).toBeNull();
    expect(pairCodeFromHash("")).toBeNull();
    expect(pairCodeFromHash("#pair=%E0%A4%A")).toBeNull();
  });

  test("websocket scheme follows the page scheme", () => {
    expect(wsUrl({ protocol: "https:", host: "h.example" })).toBe("wss://h.example/api/v1/ws");
    expect(wsUrl({ protocol: "http:", host: "192.168.1.5:7777" })).toBe(
      "ws://192.168.1.5:7777/api/v1/ws",
    );
  });

  test("device names", () => {
    expect(deviceName("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)")).toBe("iPhone");
    expect(deviceName("Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/AP1A) Chrome/120")).toBe(
      "Pixel 8",
    );
    expect(deviceName("Mozilla/5.0 (Linux; Android 10; K) Chrome/120")).toBe("Android phone");
    expect(deviceName("")).toBe("Phone");
  });

  test("posts the code and device name and returns the pair response", async () => {
    let seen: { url: string; init: RequestInit } | null = null;
    const res = await pair(
      async (url, init) => {
        seen = { url, init };
        return Response.json({ token: "dev1.secret", device_id: "dev1" });
      },
      "abc",
      "Pixel",
    );
    expect(res).toEqual({ ok: true, response: { token: "dev1.secret", device_id: "dev1" } });
    expect(seen).not.toBeNull();
    const call = seen as unknown as { url: string; init: RequestInit };
    expect(call.url).toBe("/api/v1/pair");
    expect(call.init.method).toBe("POST");
    expect(JSON.parse(String(call.init.body))).toEqual({ code: "abc", device_name: "Pixel" });
  });

  test("failure modes are described, not thrown", async () => {
    const bad = await pair(async () => new Response("no", { status: 401 }), "x", "p");
    expect(bad.ok === false && bad.retryable).toBe(false);
    const limited = await pair(async () => new Response("slow", { status: 429 }), "x", "p");
    expect(limited.ok === false && limited.message).toContain("Too many");
    const down = await pair(
      async () => {
        throw new TypeError("network");
      },
      "x",
      "p",
    );
    expect(down.ok === false && down.retryable).toBe(true);
    const junk = await pair(async () => Response.json({ nope: 1 }), "x", "p");
    expect(junk.ok).toBe(false);
  });
});

describe("routes", () => {
  test("parse and format are inverses", () => {
    for (const r of [
      { name: "sessions" },
      { name: "session", id: "a b/c" },
      { name: "changes", id: "s1" },
      { name: "devices" },
    ] as const) {
      expect(parseRoute(formatRoute(r))).toEqual(r);
    }
  });

  test("unknown hashes land on the session list", () => {
    expect(parseRoute("")).toEqual({ name: "sessions" });
    expect(parseRoute("#/wat")).toEqual({ name: "sessions" });
    expect(parseRoute("#/s/%E0%A4%A")).toEqual({ name: "sessions" });
  });
});

describe("images", () => {
  test("accepts small supported images", () => {
    expect(checkImage({ name: "a.png", type: "image/png", size: 1000 }, 0)).toEqual({ ok: true });
  });

  test("rejects over 8MB, unsupported types and oversize totals", () => {
    const big = checkImage({ name: "a.png", type: "image/png", size: MAX_IMAGE_BYTES + 1 }, 0);
    expect(big.ok).toBe(false);
    const edge = checkImage({ name: "a.png", type: "image/png", size: MAX_IMAGE_BYTES }, 0);
    expect(edge.ok).toBe(true);
    expect(checkImage({ name: "a.svg", type: "image/svg+xml", size: 10 }, 0).ok).toBe(false);
    const second = checkImage(
      { name: "b.png", type: "image/png", size: MAX_IMAGE_BYTES },
      base64Length(MAX_IMAGE_BYTES),
    );
    expect(second.ok).toBe(false);
  });

  test("base64 length matches the encoder", () => {
    for (const n of [0, 1, 2, 3, 4, 100]) {
      expect(base64Length(n)).toBe(Buffer.alloc(n).toString("base64").length);
    }
  });
});

describe("composer", () => {
  test("chips prefix and retarget", () => {
    expect(applyAgentChip("fix it", "claude")).toBe("$claude fix it");
    expect(applyAgentChip("$claude fix it", "codex")).toBe("$codex fix it");
    expect(applyAgentChip("", "claude")).toBe("$claude ");
  });

  test("active agent and send gating", () => {
    expect(activeAgent("$codex hi")).toBe("codex");
    expect(activeAgent("hi $codex")).toBeNull();
    expect(canSend("$claude ", 0)).toBe(false);
    expect(canSend("$claude hi", 0)).toBe(true);
    expect(canSend("", 1)).toBe(true);
    expect(canSend("   ", 0)).toBe(false);
  });
});

describe("modes", () => {
  test("yolo is offered only when the server allows it", () => {
    expect(availableModes(false).map((m) => m.mode)).toEqual(["plan", "ask"]);
    expect(availableModes(true).map((m) => m.mode)).toEqual(["plan", "ask", "yolo"]);
  });

  test("only yolo needs a second confirm", () => {
    expect(needsConfirm("yolo")).toBe(true);
    expect(needsConfirm("ask")).toBe(false);
    expect(needsConfirm("plan")).toBe(false);
    expect(modeLabel("ask")).toBe("Ask");
    expect(modeLabel(null)).toBe("Mode");
  });
});

describe("transcript", () => {
  test("pairs tool results with their calls", () => {
    const chunks = groupParts([
      { type: "text", text: "hello" },
      { type: "tool_call", id: "t1", name: "bash", summary: "go test" },
      { type: "tool_call", id: "t2", name: "edit", summary: "a.go" },
      { type: "tool_result", tool_call_id: "t1", name: "bash", output: "ok" },
      { type: "image", name: "a.png", mime: "image/png" },
    ]);
    expect(chunks.map((c) => c.k)).toEqual(["text", "tool", "tool", "image"]);
    const first = chunks[1];
    expect(first?.k === "tool" && first.result?.output).toBe("ok");
    const second = chunks[2];
    expect(second?.k === "tool" && second.result).toBeNull();
  });

  test("orphan results stand alone and unknown parts are skipped", () => {
    const chunks = groupParts([
      { type: "tool_result", tool_call_id: "gone", output: "x" },
      { type: "hologram" },
      { type: "text", text: "" },
    ]);
    expect(chunks).toHaveLength(1);
    expect(chunks[0]).toMatchObject({ k: "tool", call: null });
  });

  test("near-bottom detection drives auto-scroll", () => {
    expect(isNearBottom({ scrollTop: 900, scrollHeight: 1000, clientHeight: 100 })).toBe(true);
    expect(isNearBottom({ scrollTop: 500, scrollHeight: 1000, clientHeight: 100 })).toBe(false);
    expect(isNearBottom({ scrollTop: 0, scrollHeight: 50, clientHeight: 100 })).toBe(true);
  });
});

describe("service worker routing", () => {
  const shell = shellPaths(["./", "./index.html", "./main-abc.js"], "https://deck.example/");
  const origin = "https://deck.example";

  test("shell paths resolve against the scope", () => {
    expect([...shell].sort()).toEqual(["/", "/index.html", "/main-abc.js"]);
  });

  test("shell assets are cache-first, navigations network-first", () => {
    const get = (url: string, mode = "no-cors") =>
      strategyFor({ method: "GET", url, mode }, origin, shell);
    expect(get(`${origin}/main-abc.js`)).toBe("cache-first");
    expect(get(`${origin}/`, "navigate")).toBe("navigate");
  });

  test("the API, other origins, writes and unknown files are never intercepted", () => {
    const s = (method: string, url: string) =>
      strategyFor({ method, url, mode: "cors" }, origin, shell);
    expect(s("GET", `${origin}/api/v1/health`)).toBe("bypass");
    expect(s("GET", `${origin}/api/v1/ws`)).toBe("bypass");
    expect(s("POST", `${origin}/api/v1/pair`)).toBe("bypass");
    expect(s("GET", "https://cdn.example/main-abc.js")).toBe("bypass");
    expect(s("GET", `${origin}/other.js`)).toBe("bypass");
    expect(
      strategyFor({ method: "GET", url: `${origin}/api/v1/x`, mode: "navigate" }, origin, shell),
    ).toBe("bypass");
  });
});

describe("edit saving", () => {
  test("needs a real change, non-empty text, a connection and no save in flight", () => {
    expect(canSaveEdit("fix it", "fix it now", true, false)).toBe(true);
    expect(canSaveEdit("fix it", "fix it", true, false)).toBe(false);
    expect(canSaveEdit("fix it", "  fix it  ", true, false)).toBe(false);
    expect(canSaveEdit("fix it", "   ", true, false)).toBe(false);
    expect(canSaveEdit("fix it", "fix it now", false, false)).toBe(false);
    expect(canSaveEdit("fix it", "fix it now", true, true)).toBe(false);
  });
});

describe("service worker push", () => {
  const scope = "https://deck.example/";
  const win = (url: string, focused = false, visibilityState = "hidden") => ({
    url,
    focused,
    visibilityState,
  });

  test("a payload is parsed field by field", () => {
    const text = JSON.stringify({
      title: "Approve?",
      body: "claude wants to run go test",
      tag: "approval-a1",
      session_id: "s 1",
      approval_id: "a1",
    });
    expect(parsePushPayload(text)).toEqual({
      title: "Approve?",
      body: "claude wants to run go test",
      tag: "approval-a1",
      session_id: "s 1",
      approval_id: "a1",
    });
  });

  test("an unreadable payload still yields a notification", () => {
    const generic = { title: "Deck", body: "", tag: "", session_id: "", approval_id: "" };
    expect(parsePushPayload(null)).toEqual(generic);
    expect(parsePushPayload("")).toEqual(generic);
    expect(parsePushPayload("not json")).toEqual(generic);
    expect(parsePushPayload("[1]")).toEqual(generic);
    expect(parsePushPayload('{"title":7,"body":null}')).toEqual(generic);
  });

  test("options carry the route data and never re-alert on a repeated tag", () => {
    const p = parsePushPayload(
      '{"title":"t","body":"b","tag":"x","session_id":"s1","approval_id":"a"}',
    );
    expect(notificationOptions(p)).toEqual({
      body: "b",
      renotify: false,
      tag: "x",
      data: { session_id: "s1", approval_id: "a" },
    });
    const untagged = notificationOptions(parsePushPayload('{"title":"t"}'));
    expect("tag" in untagged).toBe(false);
  });

  test("a notification is skipped only when the app is focused or visible", () => {
    expect(appIsInView([])).toBe(false);
    expect(appIsInView([win(scope)])).toBe(false);
    expect(appIsInView([win(scope, true)])).toBe(true);
    expect(appIsInView([win(scope), win(scope, false, "visible")])).toBe(true);
  });

  test("clicks route to the session using the app's own hash format", () => {
    expect(clickUrl(scope, { session_id: "s1" })).toBe("https://deck.example/#/s/s1");
    expect(clickUrl(scope, { session_id: "a b/c" })).toBe("https://deck.example/#/s/a%20b%2Fc");
    expect(clickUrl(scope, { session_id: "" })).toBe("https://deck.example/#/");
    expect(clickUrl(scope, null)).toBe("https://deck.example/#/");
    expect(clickUrl("https://deck.example/remote/", { session_id: "s1" })).toBe(
      "https://deck.example/remote/#/s/s1",
    );
  });

  test("a click focuses the window the user was in, else a visible one, else any", () => {
    const data = { session_id: "s1" };
    const clients = [
      win(`${scope}other`),
      win(scope, false, "visible"),
      win(`${scope}#/devices`, true, "visible"),
    ];
    expect(planClick(clients, scope, data)).toEqual({
      action: "focus",
      index: 2,
      url: "https://deck.example/#/s/s1",
    });
    expect(planClick(clients.slice(0, 2), scope, data)).toMatchObject({
      action: "focus",
      index: 1,
    });
    expect(planClick(clients.slice(0, 1), scope, data)).toMatchObject({
      action: "focus",
      index: 0,
    });
  });

  test("a click opens a new window when none belongs to the app", () => {
    expect(planClick([], scope, { session_id: "s1" })).toEqual({
      action: "open",
      url: "https://deck.example/#/s/s1",
    });
    expect(planClick([win("https://elsewhere.example/")], scope, null)).toEqual({
      action: "open",
      url: "https://deck.example/#/",
    });
  });
});

describe("time", () => {
  test("seconds and milliseconds both work", () => {
    expect(toMillis(1_760_000_000)).toBe(1_760_000_000_000);
    expect(toMillis(1_760_000_000_000)).toBe(1_760_000_000_000);
  });

  test("relative labels", () => {
    const now = 1_760_000_000_000;
    expect(relativeTime(1_760_000_000, now)).toBe("now");
    expect(relativeTime(1_760_000_000 - 300, now)).toBe("5m");
    expect(relativeTime(1_760_000_000 - 3 * 3600, now)).toBe("3h");
    expect(relativeTime(1_760_000_000 - 2 * 86400, now)).toBe("2d");
    expect(relativeTime(0, now)).toBe("");
  });
});
