import type { Mode } from "@web/protocol";
import { RACE_MAX, RACE_MIN, raceText } from "./crew";

// The composer's slash commands. A message that starts with one of these
// names runs the command instead of going to the agents. Any other text
// after a "/" is an ordinary message.

export interface SlashCommand {
  name: string;
  // What follows the name, as the picker shows it; "" for none.
  args: string;
  detail: string;
}

export const SLASH_COMMANDS: readonly SlashCommand[] = [
  { name: "plan", args: "", detail: "Switch to Plan mode" },
  { name: "ask", args: "", detail: "Switch to Ask mode" },
  { name: "yolo", args: "", detail: "Switch to Yolo mode" },
  { name: "new", args: "[title]", detail: "Start a new thread" },
  { name: "race", args: "<n> [kind] <brief>", detail: `Race ${RACE_MIN} to ${RACE_MAX} agents on one brief` },
  { name: "share", args: "", detail: "Share this room" },
  { name: "stop", args: "", detail: "Stop all agents" },
  { name: "clear", args: "", detail: "Clear the draft" },
];

export type Slash =
  | { cmd: "mode"; mode: Mode }
  | { cmd: "new"; title: string }
  | { cmd: "race"; text: string }
  | { cmd: "share" }
  | { cmd: "stop" }
  | { cmd: "clear" };

export type SlashParse = Slash | { error: string } | null;

const RACE_USAGE = "Use /race <n> [kind] <brief>, for example /race 3 codex fix the flaky test.";

// Reads a whole message as a slash command: null when it is not one. kinds
// are the agent kinds a race may name; fallback is the kind of a race that
// names none (null when there is no kind to fall back on).
export function parseSlash(text: string, kinds: readonly string[], fallback: string | null): SlashParse {
  const m = /^\/([a-z]+)(?:\s+([\s\S]*))?$/.exec(text.trim());
  if (m === null) return null;
  const name = m[1] ?? "";
  const rest = (m[2] ?? "").trim();
  const alone = (s: Slash): SlashParse => (rest === "" ? s : { error: `/${name} takes nothing after it.` });
  switch (name) {
    case "plan":
    case "ask":
    case "yolo":
      return alone({ cmd: "mode", mode: name });
    case "share":
    case "stop":
    case "clear":
      return alone({ cmd: name });
    case "new":
      return { cmd: "new", title: rest.replace(/\s+/g, " ") };
    case "race":
      return race(rest, kinds, fallback);
    default:
      return null;
  }
}

function race(rest: string, kinds: readonly string[], fallback: string | null): SlashParse {
  const m = /^(\d+)\s+([\s\S]+)$/.exec(rest);
  if (m === null) return { error: RACE_USAGE };
  const n = Number(m[1]);
  if (n < RACE_MIN || n > RACE_MAX) return { error: `A race takes ${RACE_MIN} to ${RACE_MAX} agents.` };
  let brief = (m[2] ?? "").trim();
  let kind = fallback;
  // A kind alone is a race without a brief.
  if (kinds.includes(brief.toLowerCase())) return { error: RACE_USAGE };
  const first = /^(\S+)\s+([\s\S]+)$/.exec(brief);
  if (first !== null && kinds.includes((first[1] ?? "").toLowerCase())) {
    kind = (first[1] ?? "").toLowerCase();
    brief = (first[2] ?? "").trim();
  }
  if (kind === null) return { error: "Name the kind of agent to race: /race 3 codex <brief>." };
  const text = raceText(kind, n, brief);
  return text === null ? { error: RACE_USAGE } : { cmd: "race", text };
}

export function slashCommand(name: string): SlashCommand | undefined {
  return SLASH_COMMANDS.find((c) => c.name === name);
}
