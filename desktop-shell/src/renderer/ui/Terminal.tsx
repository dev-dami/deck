import "@xterm/xterm/css/xterm.css";
import { FitAddon } from "@xterm/addon-fit";
import { type ITheme, Terminal as XTerm } from "@xterm/xterm";
import { Square } from "./icons";
import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
import { cn } from "../lib/cn";
import { base64Bytes, type ProjectApi } from "../project";
import { PortChip } from "./PreviewPanel";
import { dropPorts, feedTerminal, useOpenPorts } from "./usePorts";

// xterm.js writes its layout rules into <style> elements, which the
// window's CSP (style-src 'self') does not apply. This keeps the same rules
// in a constructed stylesheet instead, which the CSP allows, so the policy
// stays strict.
function mirrorStyles(root: HTMLElement): () => void {
  const sheet = new CSSStyleSheet();
  document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
  const sync = () => {
    const css = [...root.querySelectorAll("style")].map((s) => s.textContent ?? "").join("\n");
    try {
      sheet.replaceSync(css);
    } catch {
      // A rule this engine cannot parse; the rest of the terminal still works.
    }
  };
  const watch = new MutationObserver(sync);
  watch.observe(root, { subtree: true, childList: true, characterData: true });
  sync();
  return () => {
    watch.disconnect();
    document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== sheet);
  };
}

function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// The terminal's colours from the window's theme tokens.
function themeNow(): ITheme {
  const light = document.documentElement.getAttribute("data-mode") === "light";
  return {
    background: cssVar("--background") || (light ? "#fcfcfc" : "#0a0a0a"),
    foreground: cssVar("--foreground") || (light ? "#27272a" : "#ededed"),
    cursor: light ? "#26384e" : "#b4cbff",
    selectionBackground: light ? "rgba(37,63,99,0.2)" : "rgba(180,203,255,0.25)",
  };
}

const MONO = 'ui-monospace, "SF Mono", SFMono-Regular, "JetBrains Mono", Menlo, Consolas, monospace';
// One shell's terminal. It belongs to its project, not to the drawer: the
// drawer mounts el while it shows, and a project switch only takes el out
// of the page, so the shell and its scrollback keep running.
interface Term {
  id: string;
  n: number;
  xterm: XTerm;
  fit: FitAddon;
  el: HTMLDivElement;
  opened: boolean;
  dispose: () => void;
}

function createTerm(project: ProjectApi, id: string, n: number): Term {
  const el = document.createElement("div");
  el.style.height = "100%";
  el.style.width = "100%";
  const unmirror = mirrorStyles(el);
  const xterm = new XTerm({
    fontFamily: MONO,
    fontSize: 12.5,
    lineHeight: 1.2,
    cursorBlink: true,
    allowProposedApi: false,
    scrollback: 5000,
    theme: themeNow(),
  });
  const fit = new FitAddon();
  xterm.loadAddon(fit);
  // xterm keeps what it gets before it is opened and draws it on open.
  const text = new TextDecoder();
  const unwatch = project.watchTerminal(id, (ev) => {
    if (ev.type === "term") {
      const bytes = base64Bytes(ev.data);
      xterm.write(bytes);
      feedTerminal(project, id, `Terminal ${n}`, text.decode(bytes, { stream: true }));
    } else if (ev.type === "term-exit") xterm.write(`\r\n\x1b[2m[process exited with code ${ev.code}]\x1b[0m\r\n`);
  });
  const input = xterm.onData((d) => project.terminalWrite(id, d));
  const resize = xterm.onResize(({ cols, rows }) => project.terminalResize(id, cols, rows));
  const ro = new ResizeObserver(() => refit(t));
  ro.observe(el);
  // The window's light or dark follows into the terminal.
  const mode = new MutationObserver(() => {
    xterm.options.theme = themeNow();
  });
  mode.observe(document.documentElement, { attributes: true, attributeFilter: ["data-mode", "data-palette", "data-accent"] });
  const t: Term = {
    id,
    n,
    xterm,
    fit,
    el,
    opened: false,
    dispose: () => {
      ro.disconnect();
      mode.disconnect();
      input.dispose();
      resize.dispose();
      unwatch();
      xterm.dispose();
      unmirror();
      el.remove();
    },
  };
  return t;
}

function refit(t: Term): void {
  if (!t.opened || t.el.offsetParent === null) return;
  try {
    t.fit.fit();
  } catch {
    // Not laid out yet.
  }
}

// The terminals of each project, keyed by its ProjectApi (one per open
// local Deck), each in the slot of its pane in the layout (layout/tree.ts,
// "term:…"). A shell starts the first time its pane shows and runs on while
// the pane hides, until the pane closes. Shells close when the project
// closes or the app quits.
interface Drawer {
  terms: Readonly<Record<string, Term>>;
  shown: boolean;
}

interface DrawerEntry {
  view: Drawer;
  next: number;
  opening: Set<string>;
}

const NO_DRAWER: Drawer = { terms: {}, shown: false };
const drawers = new Map<ProjectApi, DrawerEntry>();
const drawerListeners = new Set<() => void>();

function drawerEntry(project: ProjectApi): DrawerEntry {
  let e = drawers.get(project);
  if (e === undefined) {
    e = { view: NO_DRAWER, next: 1, opening: new Set() };
    drawers.set(project, e);
  }
  return e;
}

function updateDrawer(project: ProjectApi, f: (d: Drawer) => Drawer): void {
  const e = drawerEntry(project);
  const view = f(e.view);
  if (view === e.view) return;
  e.view = view;
  for (const l of drawerListeners) l();
}

function subscribeDrawers(l: () => void): () => void {
  drawerListeners.add(l);
  return () => {
    drawerListeners.delete(l);
  };
}

function drawerOf(project: ProjectApi | undefined): Drawer {
  return project === undefined ? NO_DRAWER : (drawers.get(project)?.view ?? NO_DRAWER);
}

// Starts the shell of a pane's slot, once.
async function ensureTerm(project: ProjectApi, slot: string, onError: (text: string) => void): Promise<void> {
  const e = drawerEntry(project);
  if (e.view.terms[slot] !== undefined || e.opening.has(slot)) return;
  e.opening.add(slot);
  const r = await project.terminalOpen(100, 24);
  e.opening.delete(slot);
  if (typeof r !== "string") {
    onError(r.error);
    return;
  }
  // The project closed while the shell started.
  if (drawers.get(project) !== e) {
    project.terminalClose(r);
    return;
  }
  const t = createTerm(project, r, e.next++);
  updateDrawer(project, (d) => ({ ...d, terms: { ...d.terms, [slot]: t } }));
}

// Ends the shell of a slot (its pane closed).
export function closeTerm(project: ProjectApi | undefined, slot: string): void {
  const t = drawerOf(project).terms[slot];
  if (project === undefined || t === undefined) return;
  t.dispose();
  project.terminalClose(t.id);
  updateDrawer(project, (d) => {
    const { [slot]: _gone, ...terms } = d.terms;
    return { ...d, terms };
  });
}

// Ends the shells whose panes the layout no longer has (a preset, or a pane
// closed while another project showed).
export function pruneTerms(project: ProjectApi | undefined, keep: ReadonlySet<string>): void {
  if (project === undefined) return;
  for (const slot of Object.keys(drawerOf(project).terms)) if (!keep.has(slot)) closeTerm(project, slot);
}

// Every shell of a project ends: it closed, or its engine stopped.
export function closeTerminals(project: ProjectApi | undefined): void {
  dropPorts(project);
  if (project === undefined) return;
  const e = drawers.get(project);
  if (e === undefined) return;
  drawers.delete(project);
  for (const t of Object.values(e.view.terms)) {
    t.dispose();
    project.terminalClose(t.id);
  }
  for (const l of drawerListeners) l();
}

// Whether the project's terminals show, kept with them.
export function useTerminalShown(project: ProjectApi | undefined): [boolean, (v: boolean | ((v: boolean) => boolean)) => void] {
  const shown = useSyncExternalStore(subscribeDrawers, () => drawerOf(project).shown);
  const set = useCallback(
    (v: boolean | ((v: boolean) => boolean)) => {
      if (project !== undefined) updateDrawer(project, (d) => {
        const next = typeof v === "function" ? v(d.shown) : v;
        return next === d.shown ? d : { ...d, shown: next };
      });
    },
    [project],
  );
  return [shown, set];
}

function useTerm(project: ProjectApi, slot: string): Term | undefined {
  return useSyncExternalStore(subscribeDrawers, () => drawerOf(project).terms[slot]);
}

function TerminalView({ t, visible }: { t: Term; visible: boolean }) {
  const host = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = host.current;
    if (el === null) return;
    el.appendChild(t.el);
    if (!t.opened) {
      t.xterm.open(t.el);
      t.opened = true;
    }
    refit(t);
    return () => {
      if (t.el.parentElement === el) t.el.remove();
    };
  }, [t]);

  useEffect(() => {
    if (!visible) return;
    requestAnimationFrame(() => {
      refit(t);
      t.xterm.focus();
    });
  }, [t, visible]);

  return <div ref={host} className={cn("terminal-view h-full w-full px-3 py-2", !visible && "hidden")} data-terminal={t.id} />;
}

// A terminal pane: its slot's shell, started the first time it shows.
export function TerminalPane({ project, slot, visible, onError }: { project: ProjectApi; slot: string; visible: boolean; onError: (text: string) => void }) {
  const t = useTerm(project, slot);
  useEffect(() => {
    if (visible) void ensureTerm(project, slot, onError);
  }, [visible, project, slot, onError]);
  return (
    <div className="terminal-pane min-h-0 flex-1 bg-canvas">
      {t === undefined ? <p className="px-4 py-3 text-faint text-xs">Starting a shell…</p> : <TerminalView t={t} visible={visible} />}
    </div>
  );
}

// What a terminal's tab shows after its name: the dev servers it runs,
// with a Stop that sends Ctrl+C.
export function TerminalTabExtras({ project, slot, n, onPreview }: { project: ProjectApi; slot: string; n: number; onPreview: (url: string) => void }) {
  const t = useTerm(project, slot);
  const ports = useOpenPorts(project);
  const own = t === undefined ? [] : ports.filter((p) => p.source.kind === "terminal" && p.source.term === t.id);
  if (t === undefined || own.length === 0) return null;
  return (
    <>
      {own.map((p) => (
        <PortChip key={p.port} p={p} onPreview={onPreview} className="h-5" />
      ))}
      <button
        type="button"
        aria-label={`Stop the dev server in terminal ${n}`}
        title="Stop (sends Ctrl+C)"
        className="grid size-5 cursor-pointer place-items-center rounded-full text-muted-foreground outline-none hover:bg-hover hover:text-destructive-foreground focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => project.terminalWrite(t.id, "\x03")}
      >
        <Square className="size-3" weight="fill" />
      </button>
    </>
  );
}

// Gives a terminal pane's shell the keys, for the keys that move between panes.
export function focusTerm(project: ProjectApi | undefined, slot: string): boolean {
  const t = drawerOf(project).terms[slot];
  if (t === undefined || !t.opened) return false;
  t.xterm.focus();
  return true;
}
