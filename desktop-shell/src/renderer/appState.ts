import type { Store } from "@web/controller";
import type { Session } from "@web/protocol";
import type { AppState } from "@web/state";
import { useSyncExternalStore } from "react";
import type { EngineStop } from "../shared/bridge";
import { loadDraft, saveDraft } from "./drafts";
import { useSelect } from "./select";
import { initialUi, reduceUi, type UiAction, type UiState } from "./ui";

// The window's own state for each open project, keyed by the project's key
// (OpenProject.key). It lives here and not in Shell, so a switch to another
// project and back keeps the open thread, the panel, the unread marks and
// the drafts. Each project's unread marks follow its Deck from the moment
// it opens, whether its Shell shows or not.

export interface ProjectState {
  ui: UiState;
  // Set while the project's engine is stopped (a local Deck only).
  engine: EngineStop | null;
}

const EMPTY: ProjectState = { ui: initialUi, engine: null };

const states = new Map<string, ProjectState>();
// Each project's drafts by thread ("" is the new-thread screen), read from
// localStorage the first time a thread's draft is asked for.
const drafts = new Map<string, Map<string, string>>();
const attached = new Map<string, { source: string; off: () => void }>();
const listeners = new Set<() => void>();

function notify(): void {
  for (const l of listeners) l();
}

export function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

export function projectState(key: string): ProjectState {
  return states.get(key) ?? EMPTY;
}

function put(key: string, next: ProjectState): void {
  if (next === projectState(key)) return;
  states.set(key, next);
  notify();
}

export function dispatchUi(key: string, a: UiAction): void {
  const cur = projectState(key);
  const ui = reduceUi(cur.ui, a);
  if (ui !== cur.ui) put(key, { ...cur, ui });
}

export function setEngine(key: string, engine: EngineStop | null): void {
  const cur = projectState(key);
  if (cur.engine !== engine) put(key, { ...cur, engine });
}

function draftsOf(key: string): Map<string, string> {
  let d = drafts.get(key);
  if (d === undefined) {
    d = new Map();
    drafts.set(key, d);
  }
  return d;
}

export function draftOf(key: string, thread: string): string {
  const d = draftsOf(key);
  const text = d.get(thread);
  if (text !== undefined) return text;
  const source = attached.get(key)?.source;
  if (source === undefined) return "";
  const saved = loadDraft(source, thread);
  d.set(thread, saved);
  return saved;
}

export function setDraft(key: string, thread: string, text: string): void {
  if (draftOf(key, thread) === text) return;
  draftsOf(key).set(thread, text);
  const source = attached.get(key)?.source;
  if (source !== undefined) saveDraft(source, thread, text);
  notify();
}

// Starts to keep a project's state: its Deck's sessions and messages feed
// the unread marks. source names where its drafts persist.
export function attachProject(key: string, source: string, store: Store): void {
  if (attached.has(key)) return;
  let sessions: Session[] | null = null;
  let messages: AppState["messages"] | null = null;
  const feed = () => {
    const s = store.state;
    if (s.sessionsLoaded && s.sessions !== sessions) {
      sessions = s.sessions;
      dispatchUi(key, { type: "sessions", sessions });
    }
    if (s.messages !== messages) {
      messages = s.messages;
      dispatchUi(key, { type: "messages", messages });
    }
  };
  attached.set(key, { source, off: store.subscribe(feed) });
  feed();
}

// Forgets a closed project. Its saved drafts stay for the next time it opens.
export function dropProject(key: string): void {
  attached.get(key)?.off();
  attached.delete(key);
  drafts.delete(key);
  if (states.delete(key)) notify();
}

// The parts of UiState a view reads. The seen texts and message lists change
// with every token and are left out.
export type UiView = Omit<UiState, "seen" | "lists">;

function uiView(p: ProjectState): UiView {
  const { seen: _s, lists: _l, ...view } = p.ui;
  return view;
}

const sameView = (a: UiView, b: UiView): boolean =>
  (Object.keys(a) as (keyof UiView)[]).every((k) => Object.is(a[k], b[k]));

export function useProjectUi(key: string): UiView {
  return useSelect(subscribe, () => projectState(key), uiView, sameView);
}

export function useEngine(key: string): EngineStop | null {
  return useSyncExternalStore(subscribe, () => projectState(key).engine);
}

export function useDraft(key: string, thread: string): string {
  return useSyncExternalStore(subscribe, () => draftOf(key, thread));
}
