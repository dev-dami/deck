import { useSyncExternalStore } from "react";

// The starter tour (ui/Tour.tsx, docs/design-glass.md, section 13): coach
// marks over the window that teach a new person Deck, a step at a time.
// Its steps, how it moves between them and where its card sits are here,
// plain enough to test; the overlay is in Tour.tsx.

// The screen a step belongs to: the Welcome of a new install, or a
// project's window (the Shell).
export type TourScreen = "welcome" | "shell";

export interface TourStep {
  id: string;
  screen: TourScreen;
  // The element the spotlight rings, by selector; null for a step with no
  // target, whose card sits in the middle of the window.
  target: string | null;
  // Shown in the middle when its target is not on screen, where any other
  // step is passed over: what it teaches matters on every screen.
  always?: boolean;
  title: string;
  // "{mod}" reads as the platform's shortcut key (⌘ or Ctrl).
  body: string;
}

// Whether a step shows, given what is on screen.
export function shows(step: TourStep, present: (selector: string) => boolean): boolean {
  return step.target === null || step.always === true || present(step.target);
}

export const TOUR_STEPS: readonly TourStep[] = [
  {
    id: "welcome",
    screen: "welcome",
    target: null,
    title: "Welcome to Deck",
    body: "One window for Claude Code, Codex, OpenCode, Antigravity and Pi. Hand a task to one agent, split it between two, or race three on the same bug.",
  },
  {
    id: "open-folder",
    screen: "welcome",
    target: ".welcome-screen .first-action.first-primary",
    title: "Open a folder",
    body: "Work on a project on this computer. Deck starts its own engine in the folder and the agents work there.",
  },
  {
    id: "pair",
    screen: "welcome",
    target: ".welcome-screen .first-action:not(.first-primary)",
    title: "Or pair with a Deck",
    body: "Connect to a Deck you run in a terminal, or one on another computer, with its pairing link.",
  },
  {
    id: "agents",
    screen: "welcome",
    target: ".welcome-screen .agent-clis",
    title: "Your agents",
    body: "Deck drives the agent CLIs it finds on your PATH. One is enough to start. Open a folder and the tour goes on inside.",
  },
  {
    id: "project",
    screen: "shell",
    target: null,
    title: "A quick look around",
    body: "Threads, the composer, modes and views, in a minute. The arrow keys step through, Esc leaves.",
  },
  {
    id: "threads",
    screen: "shell",
    target: 'nav[aria-label="Threads"]',
    title: "Threads",
    body: "Each task is a thread: its agents, their work and the diff. {mod}N starts one, {mod}1 to 9 jump between them.",
  },
  {
    id: "composer",
    screen: "shell",
    target: ".composer",
    title: "Ask, hand off, split",
    body: "Write the task and send. $claude or $codex hands it to an agent; name two and they split it. @ names agents and people, # adds a file.",
  },
  {
    id: "mode",
    screen: "shell",
    target: ".mode-picker",
    always: true,
    title: "Plan, Ask or Yolo",
    body: "In a thread, the dial in the composer sets how freely its agents act: Plan proposes and runs nothing, Ask checks before commands and edits, Yolo runs everything.",
  },
  {
    id: "home",
    screen: "shell",
    target: "section.home",
    title: "Your overview",
    body: "Under a new thread: your activity, the threads you touched last, what your plan has used and what waits on you.",
  },
  {
    id: "views",
    screen: "shell",
    target: ".room-views",
    title: "Conversation or Graph",
    body: "Read a thread as a conversation, or see its work as a graph of agents, tasks and branches.",
  },
  {
    id: "panels",
    screen: "shell",
    target: ".panel-toggles",
    title: "Diff, terminal, panels",
    body: "Open the diff, a terminal or the side panel beside the conversation, and lay the panes out as you like.",
  },
  {
    id: "palette",
    screen: "shell",
    target: null,
    title: "Everything in {mod}K",
    body: "The command palette finds any thread, message or command, this tour included.",
  },
  {
    id: "appearance",
    screen: "shell",
    target: '.side-foot [aria-label="Settings"]',
    title: "Make it yours",
    body: "Settings › Appearance has the themes, wallpapers, accent and layout. Settings › General can show this tour again.",
  },
];

export function stepsFor(screen: TourScreen): TourStep[] {
  return TOUR_STEPS.filter((s) => s.screen === screen);
}

// A step's title and body with the platform's shortcut key.
export function stepText(step: TourStep, mod: string): { title: string; body: string } {
  const sub = (s: string) => s.replaceAll("{mod}", mod);
  return { title: sub(step.title), body: sub(step.body) };
}

// The step after (dir 1) or before (dir -1) index whose target is on
// screen, or a step with none; null when there is none that way. A step
// whose target is missing (the home cards in an open thread, the views on
// a new one) is passed over, unless it shows always.
export function stepFrom(steps: readonly TourStep[], index: number, dir: 1 | -1, present: (selector: string) => boolean): number | null {
  for (let i = index + dir; i >= 0 && i < steps.length; i += dir) {
    const s = steps[i];
    if (s !== undefined && shows(s, present)) return i;
  }
  return null;
}

// The first step to show: the first whose target is there.
export function firstStep(steps: readonly TourStep[], present: (selector: string) => boolean): number | null {
  return stepFrom(steps, -1, 1, present);
}

export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

export type Side = "below" | "above" | "right" | "left" | "center";

// Where the card sits: below the target if it fits, else above, else
// beside it (right, then left), else over it in the middle of the window.
// It lines up with the target's middle and stays margin from the window's
// edges. A step with no target puts it in the middle.
export function placeCard(
  target: Box | null,
  card: { w: number; h: number },
  view: { w: number; h: number },
  gap = 14,
  margin = 12,
): { x: number; y: number; side: Side } {
  // A card wider than the window keeps its leading edge in view.
  const clamp = (v: number, lo: number, hi: number) => (hi < lo ? lo : Math.min(hi, Math.max(lo, v)));
  const cx = (x: number) => clamp(x, margin, view.w - card.w - margin);
  const cy = (y: number) => clamp(y, margin, view.h - card.h - margin);
  const middle = { x: cx((view.w - card.w) / 2), y: cy((view.h - card.h) / 2), side: "center" as const };
  if (target === null) return middle;
  const midX = target.x + target.w / 2 - card.w / 2;
  const midY = target.y + target.h / 2 - card.h / 2;
  if (target.y + target.h + gap + card.h + margin <= view.h) return { x: cx(midX), y: target.y + target.h + gap, side: "below" };
  if (target.y - gap - card.h >= margin) return { x: cx(midX), y: target.y - gap - card.h, side: "above" };
  if (target.x + target.w + gap + card.w + margin <= view.w) return { x: target.x + target.w + gap, y: cy(midY), side: "right" };
  if (target.x - gap - card.w >= margin) return { x: target.x - gap - card.w, y: cy(midY), side: "left" };
  return middle;
}

// The spotlight's hole: the target grown by pad, kept inside the window.
export function spotlight(target: Box, view: { w: number; h: number }, pad = 6): Box {
  const x = Math.max(0, target.x - pad);
  const y = Math.max(0, target.y - pad);
  return { x, y, w: Math.min(view.w, target.x + target.w + pad) - x, h: Math.min(view.h, target.y + target.h + pad) - y };
}

// Whether the tour may start by itself: only on a real first run, never
// under automation (a browser driven by a test, or the app launched by
// one), so the end-to-end tests click through an untouched window.
export function autoStarts(o: { pref: "new" | "done"; webdriver: boolean; automated: boolean }): boolean {
  return o.pref === "new" && !o.webdriver && !o.automated;
}

// The open tour: the screen it runs on and its step there. One store for
// the window, so the palette, Settings and the first run all start the
// same tour.
export interface TourState {
  open: boolean;
  screen: TourScreen;
  index: number;
  // Started by the person (the palette, Settings), or by the first run.
  auto: boolean;
}

let state: TourState = { open: false, screen: "shell", index: 0, auto: false };
const listeners = new Set<() => void>();
const emit = (next: TourState) => {
  state = next;
  for (const l of listeners) l();
};

export function getTour(): TourState {
  return state;
}

export function startTour(screen: TourScreen = "shell", auto = false): void {
  emit({ open: true, screen, index: 0, auto });
}

export function setTourStep(index: number): void {
  if (state.open) emit({ ...state, index });
}

export function closeTour(): void {
  if (state.open) emit({ ...state, open: false });
}

export function subscribeTour(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useTour(): TourState {
  return useSyncExternalStore(subscribeTour, getTour);
}
