import { useEffect, useSyncExternalStore } from "react";
import type { DeckBridge } from "../../shared/bridge";
import type { DeckClient } from "../client";
import { type DetectedPort, detect, type FoundPort, findPorts, messagePorts, type PortSource, probed, splitLines, toProbe } from "../ports";
import type { ProjectApi } from "../project";

// The dev servers of each local project: the ports its terminals and its
// agents' tool output name, kept while a probe in main finds them open. It
// belongs to the project, like its terminals, so a project in the
// background keeps watching.

const PROBE_EVERY_MS = 5000;
// One burst of output makes one probe.
const SOON_MS = 250;
// How often a project's messages are read again while they stream.
const SCAN_MS = 500;

interface Entry {
  list: DetectedPort[];
  // The open ports, the same array until one opens or closes.
  open: DetectedPort[];
  // The URL the Preview tab shows; null: the first open port. The nonce
  // changes with each ask, so asking for the same page again loads it again.
  target: { url: string; nonce: number } | null;
  // The unfinished last line of each terminal.
  rest: Map<string, string>;
  // The message parts already read (ports.ts, messagePorts).
  seen: Set<string>;
  timer: ReturnType<typeof setTimeout> | null;
  due: number;
}

const NONE: DetectedPort[] = [];
const entries = new Map<ProjectApi, Entry>();
const listeners = new Set<() => void>();
// Main's probe; the shell sets it when it mounts.
let probe: DeckBridge["probePorts"] | null = null;

function entryOf(api: ProjectApi): Entry {
  let e = entries.get(api);
  if (e === undefined) {
    e = { list: [], open: NONE, target: null, rest: new Map(), seen: new Set(), timer: null, due: 0 };
    entries.set(api, e);
  }
  return e;
}

function changed(): void {
  for (const l of listeners) l();
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

function schedule(api: ProjectApi, e: Entry, ms: number): void {
  const due = Date.now() + ms;
  if (e.timer !== null) {
    if (e.due <= due) return;
    clearTimeout(e.timer);
  }
  e.due = due;
  e.timer = setTimeout(() => void tick(api, e), ms);
}

async function tick(api: ProjectApi, e: Entry): Promise<void> {
  e.timer = null;
  const asked = toProbe(e.list);
  if (probe === null || asked.length === 0) return;
  let open: number[];
  try {
    open = await probe(asked);
  } catch {
    open = [];
  }
  // The project closed meanwhile.
  if (entries.get(api) !== e) return;
  e.list = probed(e.list, asked, open, Date.now());
  const now = e.list.filter((d) => d.open);
  if (now.length !== e.open.length || now.some((d, i) => d !== e.open[i])) {
    e.open = now.length === 0 ? NONE : now;
    changed();
  }
  if (e.list.length > 0) schedule(api, e, PROBE_EVERY_MS);
}

function add(api: ProjectApi, e: Entry, found: FoundPort[], source: PortSource): void {
  const next = detect(e.list, found, source, Date.now());
  if (next === e.list) return;
  e.list = next;
  schedule(api, e, SOON_MS);
}

// What a terminal printed. Only whole lines are read.
export function feedTerminal(api: ProjectApi, term: string, name: string, text: string): void {
  const e = entryOf(api);
  const { lines, rest } = splitLines(e.rest.get(term) ?? "", text);
  e.rest.set(term, rest);
  if (lines.length > 0) add(api, e, findPorts(lines.join("\n")), { kind: "terminal", term, name });
}

// The project closed or its engine stopped: its ports go.
export function dropPorts(api: ProjectApi | undefined): void {
  const e = api === undefined ? undefined : entries.get(api);
  if (api === undefined || e === undefined) return;
  if (e.timer !== null) clearTimeout(e.timer);
  entries.delete(api);
  changed();
}

// Reads the project's agents' tool output for ports while the shell shows
// the project, and gives the store main's probe.
export function useWatchPorts(c: DeckClient, bridge: DeckBridge): void {
  useEffect(() => {
    probe = (ports) => bridge.probePorts(ports);
    const api = c.project;
    if (api === undefined) return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const scan = () => {
      timer = null;
      const e = entryOf(api);
      for (const list of Object.values(c.store.state.messages)) {
        for (const { found, source } of messagePorts(list, e.seen)) add(api, e, [found], source);
      }
    };
    scan();
    const off = c.store.subscribe(() => {
      if (timer === null) timer = setTimeout(scan, SCAN_MS);
    });
    return () => {
      off();
      if (timer !== null) clearTimeout(timer);
    };
  }, [c, bridge]);
}

// The project's open ports, in the order they were seen.
export function useOpenPorts(api: ProjectApi | undefined): DetectedPort[] {
  return useSyncExternalStore(subscribe, () => (api === undefined ? NONE : (entries.get(api)?.open ?? NONE)));
}

export function usePreviewTarget(api: ProjectApi | undefined): { url: string; nonce: number } | null {
  return useSyncExternalStore(subscribe, () => (api === undefined ? null : (entries.get(api)?.target ?? null)));
}

// Points the Preview tab at url.
export function setPreviewTarget(api: ProjectApi, url: string): void {
  const e = entryOf(api);
  e.target = { url, nonce: (e.target?.nonce ?? 0) + 1 };
  changed();
}

// Who printed the port: a terminal's name or an agent's.
export const portFrom = (p: DetectedPort): string => (p.source.kind === "terminal" ? p.source.name : p.source.agent);
