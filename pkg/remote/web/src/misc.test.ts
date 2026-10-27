import { describe, expect, test } from "bun:test";
import {
  activeAgent,
  activeAgents,
  applyAgentChip,
  canSaveEdit,
  canSend,
  toggleAgent,
} from "./composer";
import { diffStats, parseDiff } from "./diff";
import { base64Length, checkImage, displayName } from "./images";
import { availableModes, modeLabel, needsConfirm } from "./modes";
import { formatRoute, parseRoute } from "./route";
import { shellPaths, strategyFor } from "./sw-logic";
import { compareNewest, daysUntil, relativeTime, toMillis } from "./time";
import { groupParts, isNearBottom, messageText } from "./transcript";

describe("time", () => {
  test("RFC 3339 strings become millis; garbage is 0", () => {
    expect(toMillis("2026-10-12T09:30:00Z")).toBe(Date.UTC(2026, 9, 12, 9, 30));
    expect(toMillis("nope")).toBe(0);
    expect(toMillis(undefined)).toBe(0);
  });

  test("relative time and expiry", () => {
    const now = Date.UTC(2026, 9, 12, 10, 0);
    expect(relativeTime("2026-10-12T09:59:50Z", now)).toBe("now");
    expect(relativeTime("2026-10-12T09:30:00Z", now)).toBe("30m");
    expect(relativeTime("2026-10-12T07:00:00Z", now)).toBe("3h");
    expect(relativeTime("2026-10-09T10:00:00Z", now)).toBe("3d");
    expect(relativeTime(undefined, now)).toBe("");
    expect(daysUntil("2026-10-15T10:00:00Z", now)).toBe(3);
    expect(daysUntil(undefined, now)).toBeNull();
    expect(compareNewest("2026-10-12T10:00:00Z", "a", "2026-10-12T09:00:00Z", "b")).toBeLessThan(0);
  });
});

describe("diff", () => {
  test("classifies a unified diff", () => {
    const lines = parseDiff("--- a\n+++ b\n@@ -1,2 +1,2 @@\n ctx\n-old\n+new\n");
    expect(lines.map((l) => l.kind)).toEqual(["meta", "meta", "hunk", "ctx", "del", "add"]);
    expect(diffStats(lines)).toEqual({ added: 1, removed: 1 });
  });

  test("a removed line that starts with -- is not a header", () => {
    const lines = parseDiff("@@ -1,1 +1,1 @@\n--- x\n+++ y\n--- z");
    expect(lines.map((l) => l.kind)).toEqual(["hunk", "del", "add", "meta"]);
  });
});

describe("images and composer", () => {
  test("image checks", () => {
    expect(checkImage({ size: 10, type: "image/png", name: "a.png" }, 0)).toEqual({ ok: true });
    expect(checkImage({ size: 10, type: "text/plain", name: "a.txt" }, 0).ok).toBe(false);
    expect(checkImage({ size: 9 << 20, type: "image/png", name: "big.png" }, 0).ok).toBe(false);
    expect(checkImage({ size: 1 << 20, type: "image/png", name: "a.png" }, 12 << 20).ok).toBe(
      false,
    );
    expect(base64Length(3)).toBe(4);
    expect(base64Length(4)).toBe(8);
    expect(displayName("C:\\Users\\me\\shot.png")).toBe("shot.png");
    expect(displayName("/tmp/x/IMG_2041.jpg")).toBe("IMG_2041.jpg");
    expect(displayName("")).toBe("image");
  });

  test("agent chips and send rules", () => {
    expect(applyAgentChip("hello", "claude")).toBe("$claude hello");
    expect(applyAgentChip("$codex hello", "claude")).toBe("$claude hello");
    expect(activeAgent("$codex hi")).toBe("codex");
    expect(activeAgent("hi")).toBeNull();
    expect(canSend("$codex", 0)).toBe(false);
    expect(canSend("$codex", 1)).toBe(true);
    expect(canSend("  x", 0)).toBe(true);
    expect(canSaveEdit("a", "a ", true, false)).toBe(false);
    expect(canSaveEdit("a", "b", true, false)).toBe(true);
    expect(canSaveEdit("a", "b", false, false)).toBe(false);
    expect(canSaveEdit("a", "b", true, true)).toBe(false);
  });
});

describe("@ mentions in the composer", () => {
  test("@ addresses as $ does, and a path after @ is not a mention", () => {
    expect(activeAgent("@charles hi")).toBe("charles");
    expect(activeAgent("@src/a.ts hi")).toBeNull();
    expect(activeAgent("@main.go hi")).toBeNull();
    expect(activeAgents("@charles $codex fix it")).toEqual(["charles", "codex"]);
    expect(activeAgents("@charles $codex+ fix it")).toEqual(["charles"]);
    expect(activeAgents("@README.md fix it")).toEqual([]);
    expect(applyAgentChip("@codex hello", "claude")).toBe("$claude hello");
    expect(canSend("@charles ", 0)).toBe(false);
    expect(canSend("@src/a.ts", 0)).toBe(true);
    expect(toggleAgent("@charles fix", "charles")).toBe("fix");
  });
});

describe("transcript", () => {
  test("pairs tool results with their calls and reads image parts", () => {
    const chunks = groupParts([
      { type: "reasoning", text: "think", text_truncated: true },
      { type: "tool_call", id: "tc1", name: "bash", summary: "ls" },
      { type: "tool_result", tool_call_id: "tc1", name: "bash", output: "ok" },
      { type: "tool_result", tool_call_id: "tc9", name: "edit", is_error: true },
      { type: "text", text: "done" },
      { type: "image", image: { name: "shot.png", mime: "image/png" } },
      { type: "future" },
      { type: "text", text: "" },
    ]);
    expect(chunks.map((c) => c.k)).toEqual(["reasoning", "tool", "tool", "text", "image"]);
    expect(chunks[0]).toMatchObject({ truncated: true });
    expect(chunks[1]).toMatchObject({ call: { id: "tc1" }, result: { output: "ok" } });
    expect(chunks[2]).toMatchObject({ call: null, result: { is_error: true } });
    expect(chunks[4]).toEqual({ k: "image", name: "shot.png", mime: "image/png" });
    expect(
      messageText({
        id: "m",
        session_id: "s",
        role: "user",
        created_at: "2026-10-12T09:00:00Z",
        updated_at: "2026-10-12T09:00:00Z",
        finished: true,
        parts: [
          { type: "text", text: "a" },
          { type: "image", image: { name: "x", mime: "image/png" } },
          { type: "text", text: "b" },
        ],
      }),
    ).toBe("a\nb");
    expect(isNearBottom({ scrollTop: 900, scrollHeight: 1000, clientHeight: 50 })).toBe(true);
    expect(isNearBottom({ scrollTop: 0, scrollHeight: 1000, clientHeight: 50 })).toBe(false);
  });
});

describe("modes and routes", () => {
  test("yolo is offered only when this device may use it (R15)", () => {
    expect(availableModes(false).map((m) => m.mode)).toEqual(["plan", "ask"]);
    expect(availableModes(true).map((m) => m.mode)).toEqual(["plan", "ask", "yolo"]);
    expect(needsConfirm("yolo")).toBe(true);
    expect(needsConfirm("ask")).toBe(false);
    expect(modeLabel("plan")).toBe("Plan");
    expect(modeLabel(null)).toBe("Mode");
  });

  test("routes round-trip", () => {
    for (const r of [
      { name: "sessions" as const },
      { name: "session" as const, id: "ses 8f/2a" },
      { name: "changes" as const, id: "ses_8f2a" },
      { name: "devices" as const },
    ]) {
      expect(parseRoute(formatRoute(r))).toEqual(r);
    }
    expect(parseRoute("")).toEqual({ name: "sessions" });
    expect(parseRoute("#pair=abc")).toEqual({ name: "sessions" });
    expect(parseRoute("#/s/%E0%A4%A")).toEqual({ name: "sessions" });
  });
});

describe("service worker strategy", () => {
  const origin = "https://deck.local:7733";
  const shell = shellPaths(["./", "./index.html", "./main-abc.js"], `${origin}/`);

  test("shell files are cache-first, navigations network-first, the rest bypass", () => {
    expect(shell.has("/main-abc.js")).toBe(true);
    const get = (url: string, mode = "no-cors") => ({ method: "GET", url, mode });
    expect(strategyFor(get(`${origin}/main-abc.js`), origin, shell)).toBe("cache-first");
    expect(strategyFor(get(`${origin}/`, "navigate"), origin, shell)).toBe("navigate");
    expect(strategyFor(get(`${origin}/api/v1/health`), origin, shell)).toBe("bypass");
    expect(strategyFor(get(`${origin}/api/v1/ws`, "navigate"), origin, shell)).toBe("bypass");
    expect(strategyFor(get("https://other/main-abc.js"), origin, shell)).toBe("bypass");
    expect(strategyFor(get(`${origin}/other.js`), origin, shell)).toBe("bypass");
    expect(
      strategyFor({ method: "POST", url: `${origin}/`, mode: "navigate" }, origin, shell),
    ).toBe("bypass");
    expect(strategyFor(get("not a url"), origin, shell)).toBe("bypass");
  });
});

describe("addressing several agents", () => {
  test("reads every leading mention", () => {
    expect(activeAgents("$claude $codex compare")).toEqual(["claude", "codex"]);
    // The retired forms are text.
    expect(activeAgents("$codex+ start")).toEqual([]);
    expect(activeAgents("$codex×3 start")).toEqual([]);
    expect(activeAgents("ask $claude later")).toEqual([]);
  });

  test("toggles an agent in and out of the addressees", () => {
    expect(toggleAgent("fix it", "codex")).toBe("$codex fix it");
    expect(toggleAgent("$codex fix it", "claude")).toBe("$codex $claude fix it");
    expect(toggleAgent("$codex $claude fix it", "codex")).toBe("$claude fix it");
    expect(toggleAgent("$claude fix it", "claude")).toBe("fix it");
  });

  test("a message of mentions only has nothing to send", () => {
    expect(canSend("$claude $codex ", 0)).toBe(false);
    expect(canSend("$claude $codex hi", 0)).toBe(true);
  });
});
