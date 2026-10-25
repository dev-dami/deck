import type { Message } from "@web/protocol";

// Dev servers say where they listen: "Local: http://localhost:5173/",
// "listening on port 3000", "Serving HTTP on 0.0.0.0 port 8000". This finds
// those ports in terminal and tool output, and keeps the list of ports
// that a probe in main found open. Pure, so the tests drive it directly.

// Where a port was seen: a terminal of the drawer, or an agent's tool
// output in a room.
export type PortSource = { kind: "terminal"; term: string; name: string } | { kind: "agent"; room: string; agent: string };

export interface FoundPort {
  port: number;
  // Where the page is, with the host made loopback: http://localhost:3000/.
  url: string;
}

export interface DetectedPort extends FoundPort {
  source: PortSource;
  // A probe found the port open. Only open ports show.
  open: boolean;
  // A port no probe found open yet is dropped at this time (Unix ms).
  until: number;
}

// How long a port that a server announced may take to open.
export const PENDING_MS = 30_000;
// The most ports one probe asks about (main refuses more).
export const PROBE_MAX = 32;

// CSI and OSC sequences, and the other two-byte escapes.
const ANSI = /\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g;

export function stripAnsi(s: string): string {
  return s.replace(ANSI, "");
}

const HOST = String.raw`(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1?\])`;
const URL_RE = new RegExp(String.raw`\b(https?)://(${HOST}):(\d{1,5})(/[^\s'"<>\`]*)?`, "gi");
// "listening on port 3000", "Serving HTTP on 0.0.0.0 port 8000", "running at port 8080".
const PHRASE_RE = /\b(?:listening|serving|running|started|ready)\b[^\n]{0,60}?\bport\s*:?\s*(\d{2,5})\b/gi;
// "Listening on 127.0.0.1:8080", "listening on [::]:4000", "listening on :3000".
const BOUND_RE = new RegExp(String.raw`\b(?:listening|serving|running|started|ready)\b\s+(?:on|at)\s+(?:${HOST}|\*)?:(\d{2,5})\b`, "gi");

const valid = (port: number) => Number.isInteger(port) && port >= 1 && port <= 65535;

// The path as the server printed it, without the punctuation of the
// sentence around the URL.
function cleanPath(p: string | undefined): string {
  const path = (p ?? "/").replace(/[.,;:!?)\]}]+$/, "");
  return path === "" ? "/" : path;
}

// The ports one line of output names, first one first.
function lineFound(line: string): FoundPort[] {
  const out: FoundPort[] = [];
  const add = (port: number, url: string) => {
    if (valid(port) && !out.some((f) => f.port === port)) out.push({ port, url });
  };
  for (const m of line.matchAll(URL_RE)) {
    const host = m[2]!.toLowerCase() === "127.0.0.1" ? "127.0.0.1" : "localhost";
    const port = Number(m[3]);
    add(port, `${m[1]!.toLowerCase()}://${host}:${port}${cleanPath(m[4])}`);
  }
  for (const re of [PHRASE_RE, BOUND_RE]) {
    for (const m of line.matchAll(re)) {
      const port = Number(m[1]);
      add(port, `http://localhost:${port}/`);
    }
  }
  return out;
}

// The ports a block of output names, in the order they appear.
export function findPorts(text: string): FoundPort[] {
  const out: FoundPort[] = [];
  for (const line of stripAnsi(text).split(/\r\n|\r|\n/)) {
    for (const f of lineFound(line)) if (!out.some((o) => o.port === f.port)) out.push(f);
  }
  return out;
}

// A partial line longer than this is cut: a progress bar, not a URL.
const PARTIAL_MAX = 4096;

// Splits a terminal's stream into whole lines. A line is scanned only when
// it ends, so a URL that is still being typed or printed does not count.
export function splitLines(rest: string, chunk: string): { lines: string[]; rest: string } {
  const parts = (rest + chunk).split(/\r\n|\r|\n/);
  const last = parts.pop() ?? "";
  return { lines: parts, rest: last.slice(-PARTIAL_MAX) };
}

// The ports in the tool output of messages, each part once: seen holds the
// keys of the parts already read.
export function messagePorts(messages: readonly Message[], seen: Set<string>): { found: FoundPort; source: PortSource }[] {
  const out: { found: FoundPort; source: PortSource }[] = [];
  for (const m of messages) {
    m.parts.forEach((p, i) => {
      const output = p.output;
      if (output === undefined || output === "") return;
      const key = `${m.id}:${i}:${output.length}`;
      if (seen.has(key)) return;
      seen.add(key);
      const source: PortSource = { kind: "agent", room: m.session_id, agent: m.agent ?? "agent" };
      for (const found of findPorts(output)) out.push({ found, source });
    });
  }
  return out;
}

// Adds the ports found to the list. A port already there keeps its first
// URL and source; a new one waits for a probe until PENDING_MS have passed.
export function detect(list: readonly DetectedPort[], found: readonly FoundPort[], source: PortSource, now: number): DetectedPort[] {
  const fresh = found.filter((f, i) => !list.some((d) => d.port === f.port) && found.findIndex((g) => g.port === f.port) === i);
  if (fresh.length === 0) return list as DetectedPort[];
  return [...list, ...fresh.map((f) => ({ ...f, source, open: false, until: now + PENDING_MS }))];
}

// The ports to probe next: open ones first, then the newest waiting ones.
export function toProbe(list: readonly DetectedPort[]): number[] {
  return [...list.filter((d) => d.open), ...list.filter((d) => !d.open).reverse()].slice(0, PROBE_MAX).map((d) => d.port);
}

// The list after a probe of asked found open: an asked port that is open
// shows; an open port that closed goes; a waiting port goes when its time
// is up.
export function probed(list: readonly DetectedPort[], asked: readonly number[], open: readonly number[], now: number): DetectedPort[] {
  const out: DetectedPort[] = [];
  for (const d of list) {
    const isOpen = open.includes(d.port);
    if (isOpen) out.push(d.open ? d : { ...d, open: true });
    else if (d.open && asked.includes(d.port)) continue;
    else if (!d.open && d.until <= now) continue;
    else out.push(d);
  }
  return out.length === list.length && out.every((d, i) => d === list[i]) ? (list as DetectedPort[]) : out;
}

// The port's chip label, ":3000".
export const portLabel = (port: number) => `:${port}`;
