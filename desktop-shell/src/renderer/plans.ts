import type { Mode } from "@web/protocol";

// A plan an agent posts: an ordered list of three or more steps, perhaps
// under a heading, with the prose around it kept as markdown.
export interface Plan {
  before: string;
  heading: string | null;
  steps: string[];
  after: string;
}

export const APPROVE_TEXT = "Go ahead with the plan.";

const ITEM = /^ {0,3}(\d{1,3})[.)][ \t]+(\S.*)$/;
const HEADING = /^ {0,3}#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$/;
const FENCE = /^ {0,3}(```|~~~)/;
const INDENTED = /^[ \t]{2,}\S/;
const MIN_STEPS = 3;

// The first ordered list of three or more steps in a markdown text, or null.
// Lists inside code fences do not count.
export function findPlan(text: string): Plan | null {
  const lines = text.split("\n");
  let fenced = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    if (FENCE.test(line)) {
      fenced = !fenced;
      continue;
    }
    if (fenced) continue;
    const m = ITEM.exec(line);
    if (m === null || m[1] !== "1") continue;
    const { steps, end } = readList(lines, i);
    if (steps.length < MIN_STEPS) {
      i = end - 1;
      continue;
    }
    return withHeading(lines, i, end, steps);
  }
  return null;
}

// Reads the list that starts at line start: each item with its indented
// continuation lines. It ends at the first line that is neither.
function readList(lines: string[], start: number): { steps: string[]; end: number } {
  const steps: string[] = [];
  let i = start;
  while (i < lines.length) {
    const line = lines[i] ?? "";
    const m = ITEM.exec(line);
    if (m !== null) {
      steps.push(m[2] ?? "");
      i++;
      continue;
    }
    if (line.trim() === "") {
      // A blank line goes on only when more of the list follows.
      let j = i;
      while (j < lines.length && (lines[j] ?? "").trim() === "") j++;
      const next = lines[j] ?? "";
      if (j < lines.length && (ITEM.test(next) || INDENTED.test(next))) {
        i = j;
        continue;
      }
      break;
    }
    if (INDENTED.test(line) && steps.length > 0) {
      steps[steps.length - 1] += `\n${line.trim()}`;
      i++;
      continue;
    }
    break;
  }
  return { steps, end: i };
}

// A heading right above the list: a markdown heading, a bold line, or a
// short line that ends in a colon.
function headingOf(line: string): string | null {
  const h = HEADING.exec(line);
  if (h !== null) return clean(h[1] ?? "");
  if (line.length > 80) return null;
  if (/^(\*\*|__)[^*_]+(\*\*|__):?$/.test(line) || /^[^*_].*:$/.test(line)) return clean(line);
  return null;
}

function clean(s: string): string {
  return s
    .replace(/^(\*\*|__)/, "")
    .replace(/:$/, "")
    .replace(/(\*\*|__)$/, "")
    .replace(/:$/, "")
    .trim();
}

function withHeading(lines: string[], start: number, end: number, steps: string[]): Plan {
  let h = start - 1;
  while (h >= 0 && (lines[h] ?? "").trim() === "") h--;
  const heading = h >= 0 ? headingOf((lines[h] ?? "").trim()) : null;
  const cut = heading === null ? start : h;
  return {
    before: lines.slice(0, cut).join("\n").trim(),
    heading,
    steps,
    after: lines.slice(end).join("\n").trim(),
  };
}

// A reply is a plan when the room is in plan mode, or when the engine marks
// it as one.
export function detectPlan(text: string, ctx: { mode: Mode | null; marked?: boolean }): Plan | null {
  if (ctx.mode !== "plan" && ctx.marked !== true) return null;
  return findPlan(text);
}

// The steps as a numbered list, as the composer gets them back to edit.
export function planText(steps: readonly string[]): string {
  return steps.map((s, i) => `${i + 1}. ${s.replace(/\n/g, "\n   ")}`).join("\n");
}

// What a room goes back to when its plan is approved: yolo when it was yolo
// before plan, else ask.
export function approveMode(before: Mode | null): Mode {
  return before === "yolo" ? "yolo" : "ask";
}

// The mode a room had before it went into plan, kept as the mode changes.
export interface ModeMemory {
  mode: Mode | null;
  before: Mode | null;
}

export function nextModeMemory(prev: ModeMemory | undefined, mode: Mode | null): ModeMemory {
  if (prev === undefined) return { mode, before: null };
  if (prev.mode === mode) return prev;
  if (mode === "plan") return { mode, before: prev.mode ?? prev.before };
  return { mode, before: null };
}
