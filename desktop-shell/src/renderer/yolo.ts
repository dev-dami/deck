import type { Mode } from "@web/protocol";

// What Ctrl+Y does to a thread's mode. Yolo is the one mode a person asks
// for and leaves again, so a second press goes back to where the thread was
// (Ask when that is not known), not to a fixed mode.
export type YoloMove = { to: Mode } | { refused: string };

export function yoloMove(current: Mode | null, before: Mode | null, allowYolo: boolean): YoloMove {
  if (current === "yolo") return { to: before !== null && before !== "yolo" ? before : "ask" };
  if (!allowYolo) return { refused: "Yolo mode is not allowed for this window: the paired Deck has to allow it." };
  return { to: "yolo" };
}
