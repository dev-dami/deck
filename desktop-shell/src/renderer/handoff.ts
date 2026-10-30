// A message one agent sent another (the engine's relay): Deck's workspace
// writes it as the sender's message "→ $codex $claude\n\n<text>". A task's
// report to whoever handed it over reads `task t3 "Title" done: result`
// (session/handoff.go, notifyCreator). The timeline shows either as a
// hand-off card, not as prose.

export type HandoffState = "done" | "failed" | "blocked";

export interface Handoff {
  // The recipients' names, without their sigil.
  to: string[];
  // What was sent, without the arrow line.
  body: string;
  // The task a report is about, or null for a plain message.
  task: { id: string; title: string; state: HandoffState; result: string } | null;
  // The sub-thread a report comes from (its session and title), or null.
  sub: { id: string; title: string } | null;
}

const HEAD = /^→((?:\s+[@$][^\s@$]+)+)(?:\s+·\s+sub-thread\s+(\S+))?\s*(?:\n|$)/;
// A sub-thread's report opens with its title (session/subthread.go).
const SUB = /^\[sub-thread "((?:[^"\\]|\\.)*)"\]\s*/;
const REPORT = /^task (\S+) "((?:[^"\\]|\\.)*)" (done|failed|blocked)(?::\s*([\s\S]*))?$/;

export function handoffOf(text: string): Handoff | null {
  const head = HEAD.exec(text);
  if (head?.[1] === undefined) return null;
  const to = head[1]
    .trim()
    .split(/\s+/)
    .map((n) => n.slice(1))
    .filter((n) => n !== "");
  if (to.length === 0) return null;
  let body = text.slice(head[0].length).trim();
  let sub: Handoff["sub"] = null;
  const st = SUB.exec(body);
  if (head[2] !== undefined || st !== null) {
    sub = { id: head[2] ?? "", title: st?.[1] ?? "" };
    if (st !== null) body = body.slice(st[0].length).trim();
  }
  const r = REPORT.exec(body);
  const task =
    r?.[1] !== undefined && r[2] !== undefined && r[3] !== undefined
      ? { id: r[1], title: r[2], state: r[3] as HandoffState, result: (r[4] ?? "").trim() }
      : null;
  return { to, body, task, sub };
}

// Whether a body is long enough to fold behind "Show all".
export function foldsBody(body: string, lines = 6, chars = 480): boolean {
  return body.length > chars || body.split("\n").length > lines;
}
