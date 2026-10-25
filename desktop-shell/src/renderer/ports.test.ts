import type { Message } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { type DetectedPort, detect, findPorts, messagePorts, PENDING_MS, probed, splitLines, stripAnsi, toProbe } from "./ports";

const term = { kind: "terminal", term: "term_1", name: "Terminal 1" } as const;

describe("findPorts", () => {
  test("reads the lines dev servers print", () => {
    expect(findPorts("  ➜  Local:   http://localhost:5173/\n  ➜  Network: use --host to expose")).toEqual([
      { port: 5173, url: "http://localhost:5173/" },
    ]);
    expect(findPorts("Server listening on port 3000")).toEqual([{ port: 3000, url: "http://localhost:3000/" }]);
    expect(findPorts("ready on http://127.0.0.1:4000")).toEqual([{ port: 4000, url: "http://127.0.0.1:4000/" }]);
    expect(findPorts("Serving HTTP on 0.0.0.0 port 8000 (http://0.0.0.0:8000/) ...")).toEqual([
      { port: 8000, url: "http://localhost:8000/" },
    ]);
    expect(findPorts("listening on [::]:4001")).toEqual([{ port: 4001, url: "http://localhost:4001/" }]);
    expect(findPorts("Listening on :8081")).toEqual([{ port: 8081, url: "http://localhost:8081/" }]);
  });

  test("normalises 0.0.0.0 and [::1] to localhost and keeps the path", () => {
    expect(findPorts("open http://[::1]:9000/docs/index.html, then")).toEqual([{ port: 9000, url: "http://localhost:9000/docs/index.html" }]);
    expect(findPorts("at http://0.0.0.0:3001/app.")).toEqual([{ port: 3001, url: "http://localhost:3001/app" }]);
    expect(findPorts("HTTPS://LOCALHOST:8443/")).toEqual([{ port: 8443, url: "https://localhost:8443/" }]);
  });

  test("strips ANSI colours first", () => {
    const vite = "  \x1b[32m➜\x1b[39m  \x1b[1mLocal\x1b[22m:   \x1b[36mhttp://localhost:\x1b[1m5173\x1b[22m/\x1b[39m";
    expect(stripAnsi(vite)).toBe("  ➜  Local:   http://localhost:5173/");
    expect(findPorts(vite)).toEqual([{ port: 5173, url: "http://localhost:5173/" }]);
  });

  test("ignores other hosts, ports out of range and repeats", () => {
    expect(findPorts("see http://example.com:8080/ and http://10.0.0.2:3000/")).toEqual([]);
    expect(findPorts("http://localhost:70000/ and http://localhost:0/")).toEqual([]);
    expect(findPorts("http://localhost:3000/a\nhttp://localhost:3000/b")).toEqual([{ port: 3000, url: "http://localhost:3000/a" }]);
  });
});

test("splitLines scans only whole lines and keeps the rest", () => {
  let rest = "";
  const lines: string[] = [];
  for (const chunk of ["$ npm run dev\r\n  Local: http://local", "host:3000/\r\n$ "]) {
    const r = splitLines(rest, chunk);
    lines.push(...r.lines);
    rest = r.rest;
  }
  expect(lines).toEqual(["$ npm run dev", "  Local: http://localhost:3000/"]);
  expect(rest).toBe("$ ");
  expect(splitLines("", "x".repeat(10_000)).rest).toHaveLength(4096);
});

test("messagePorts reads tool output once and names the room and agent", () => {
  const msg = (id: string, output: string): Message => ({
    id,
    session_id: "ses_1",
    role: "assistant",
    agent: "claude-1",
    created_at: "",
    updated_at: "",
    finished: true,
    parts: [{ type: "text", text: "http://localhost:1111/ in prose is not output" }, { type: "tool_result", output }],
  });
  const seen = new Set<string>();
  const first = messagePorts([msg("m1", "VITE ready\n  Local: http://localhost:5173/")], seen);
  expect(first).toEqual([{ found: { port: 5173, url: "http://localhost:5173/" }, source: { kind: "agent", room: "ses_1", agent: "claude-1" } }]);
  expect(messagePorts([msg("m1", "VITE ready\n  Local: http://localhost:5173/")], seen)).toEqual([]);
  // The output grew: the part is read again.
  expect(messagePorts([msg("m1", "VITE ready\n  Local: http://localhost:5173/\nlistening on port 6000")], seen)).toHaveLength(2);
});

describe("the list of ports", () => {
  test("a port keeps its first URL and source, and waits for a probe", () => {
    let list = detect([], [{ port: 3000, url: "http://localhost:3000/a" }], term, 0);
    list = detect(list, [{ port: 3000, url: "http://localhost:3000/b" }], { kind: "agent", room: "r", agent: "a" }, 10);
    expect(list).toEqual([{ port: 3000, url: "http://localhost:3000/a", source: term, open: false, until: PENDING_MS }]);
  });

  test("a probe opens a port, drops it when it closes, and drops a waiting port after its time", () => {
    let list: DetectedPort[] = detect([], [{ port: 3000, url: "u3" }, { port: 4000, url: "u4" }], term, 0);
    list = probed(list, [3000, 4000], [3000], 5000);
    expect(list.map((d) => [d.port, d.open])).toEqual([
      [3000, true],
      [4000, false],
    ]);
    expect(toProbe(list)).toEqual([3000, 4000]);
    // Nothing changed: the same list.
    expect(probed(list, [3000, 4000], [3000], 6000)).toBe(list);
    list = probed(list, [3000, 4000], [], PENDING_MS);
    expect(list).toEqual([]);
  });

  test("a port the probe did not ask about stays", () => {
    const list = probed(detect([], [{ port: 3000, url: "u" }], term, 0), [], [3000], 1);
    expect(probed(list, [], [], 2)).toBe(list);
  });
});
