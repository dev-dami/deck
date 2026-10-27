import type { Mode } from "@web/protocol";

// The composer's slash commands. A message that starts with one of these
// names runs the command instead of going to the agents. Any other text
// after a "/" is an ordinary message.

export interface SlashCommand {
  name: string;
  // What follows the name, as the picker shows it; "" for none.
  args: string;
  detail: string;
  // Picking it from the menu runs it at once, though text may follow it
  // when typed (/race opens its builder, with the text as the task).
  instant?: boolean;
}

export const SLASH_COMMANDS: readonly SlashCommand[] = [
  { name: "plan", args: "", detail: "Switch to Plan mode" },
  { name: "ask", args: "", detail: "Switch to Ask mode" },
  { name: "yolo", args: "", detail: "Switch to Yolo mode" },
  { name: "new", args: "[title]", detail: "Start a new thread" },
  { name: "race", args: "[task]", detail: "Race agents of any kind on one task", instant: true },
  { name: "share", args: "", detail: "Share this room" },
  { name: "stop", args: "", detail: "Stop all agents" },
  { name: "clear", args: "", detail: "Clear the draft" },
];

export type Slash =
  | { cmd: "mode"; mode: Mode }
  | { cmd: "new"; title: string }
  // Opens the race builder with brief as its task; the builder picks the
  // agents and starts the race.
  | { cmd: "race"; brief: string }
  | { cmd: "share" }
  | { cmd: "stop" }
  | { cmd: "clear" };

export type SlashParse = Slash | { error: string } | null;

// Reads a whole message as a slash command: null when it is not one.
export function parseSlash(text: string): SlashParse {
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
      return { cmd: "race", brief: rest };
    default:
      return null;
  }
}

export function slashCommand(name: string): SlashCommand | undefined {
  return SLASH_COMMANDS.find((c) => c.name === name);
}
