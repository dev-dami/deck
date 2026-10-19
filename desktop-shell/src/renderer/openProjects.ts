import type { DeckClient } from "./client";

// A project open in the window: a folder whose Deck the app runs, or a
// paired Deck. Several are open at once, as projects are in Claude Desktop.
export interface OpenProject {
  key: string;
  // "folder:<dir>" or "paired:<server id>": what reopens it next time.
  source: string;
  // The name the sidebar shows.
  name: string;
  // The folder, or which paired Deck: never an address.
  where: string;
  transport: string;
  c: DeckClient;
}

// The projects open when the window last closed, in the order they were
// opened. They live in this window's localStorage; nothing here is secret.
const KEY = "deck.desktop.open";
const MAX = 12;

function read(): string[] {
  try {
    const v: unknown = JSON.parse(window.localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string" && /^(folder|paired):./.test(x)).slice(0, MAX) : [];
  } catch {
    return [];
  }
}

function write(list: string[]): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(list.slice(0, MAX)));
  } catch {
    // A locked storage forgets the open set; projects still open by hand.
  }
}

export function restoredOpen(): string[] {
  return read();
}

export function rememberOpen(source: string): void {
  const list = read();
  if (!list.includes(source)) write([...list, source]);
}

export function forgetOpen(source: string): void {
  write(read().filter((s) => s !== source));
}
