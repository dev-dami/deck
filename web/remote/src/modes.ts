import type { Mode } from "./protocol";

export interface ModeInfo {
  mode: Mode;
  label: string;
  blurb: string;
}

const ALL: ModeInfo[] = [
  { mode: "plan", label: "Plan", blurb: "Agents propose changes and do not run anything." },
  { mode: "ask", label: "Ask", blurb: "Agents ask before commands and edits." },
  { mode: "yolo", label: "Yolo", blurb: "Agents run everything without asking." },
];

// Yolo is only offered when the server was started with --allow-yolo.
export function availableModes(allowYolo: boolean): ModeInfo[] {
  return ALL.filter((m) => m.mode !== "yolo" || allowYolo);
}

export function needsConfirm(mode: Mode): boolean {
  return mode === "yolo";
}

export function modeLabel(mode: Mode | null): string {
  return ALL.find((m) => m.mode === mode)?.label ?? "Mode";
}
