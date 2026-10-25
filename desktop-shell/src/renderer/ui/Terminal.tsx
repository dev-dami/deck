import "@xterm/xterm/css/xterm.css";
import { FitAddon } from "@xterm/addon-fit";
import { type ITheme, Terminal as XTerm } from "@xterm/xterm";
import { Plus, SquareTerminal, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { cn } from "../lib/cn";
import { getPrefs, setPrefs } from "../prefs";
import { base64Bytes, type ProjectApi } from "../project";
import { Button } from "./kit/button";

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
  const unwatch = project.watchTerminal(id, (ev) => {
    if (ev.type === "term") xterm.write(base64Bytes(ev.data));
    else if (ev.type === "term-exit") xterm.write(`\r\n\x1b[2m[process exited with code ${ev.code}]\x1b[0m\r\n`);
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
// local Deck). Shells close when the project closes or the app quits.
interface Drawer {
  tabs: Term[];
  active: string | null;
  shown: boolean;
}

interface DrawerEntry {
  view: Drawer;
  next: number;
  opening: boolean;
}

const NO_DRAWER: Drawer = { tabs: [], active: null, shown: false };
const drawers = new Map<ProjectApi, DrawerEntry>();
const drawerListeners = new Set<() => void>();

function drawerEntry(project: ProjectApi): DrawerEntry {
  let e = drawers.get(project);
  if (e === undefined) {
    e = { view: NO_DRAWER, next: 1, opening: false };
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

async function openTerminal(project: ProjectApi, onError: (text: string) => void): Promise<void> {
  const e = drawerEntry(project);
  if (e.opening) return;
  e.opening = true;
  const r = await project.terminalOpen(100, 24);
  e.opening = false;
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
  updateDrawer(project, (d) => ({ ...d, tabs: [...d.tabs, t], active: r }));
}

function closeTerminal(project: ProjectApi, id: string): void {
  const t = drawerOf(project).tabs.find((x) => x.id === id);
  if (t === undefined) return;
  t.dispose();
  project.terminalClose(id);
  updateDrawer(project, (d) => {
    const tabs = d.tabs.filter((x) => x.id !== id);
    return { ...d, tabs, active: d.active === id ? (tabs[tabs.length - 1]?.id ?? null) : d.active };
  });
}

// Every shell of a project ends: it closed, or its engine stopped.
export function closeTerminals(project: ProjectApi | undefined): void {
  if (project === undefined) return;
  const e = drawers.get(project);
  if (e === undefined) return;
  drawers.delete(project);
  for (const t of e.view.tabs) {
    t.dispose();
    project.terminalClose(t.id);
  }
  for (const l of drawerListeners) l();
}

// Whether the project's drawer shows, kept with its terminals.
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

const MIN_HEIGHT = 140;

// The bottom drawer of terminals, after T3 Code's ThreadTerminalDrawer:
// tabs of shells in the project folder that keep running while it is
// hidden, and a top edge to drag its height.
export function TerminalDrawer({
  project,
  shown,
  onHide,
  onError,
}: {
  project: ProjectApi;
  shown: boolean;
  onHide: () => void;
  onError: (text: string) => void;
}) {
  const { tabs, active } = useSyncExternalStore(subscribeDrawers, () => drawerOf(project));
  const [height, setHeight] = useState(() => Math.max(MIN_HEIGHT, getPrefs().terminalHeight));

  const add = useCallback(() => openTerminal(project, onError), [project, onError]);

  // The first time the drawer shows, it starts a shell.
  useEffect(() => {
    if (shown && tabs.length === 0) void add();
  }, [shown, tabs.length, add]);

  const close = (id: string) => {
    closeTerminal(project, id);
    if (drawerOf(project).tabs.length === 0) onHide();
  };
  const setActive = (id: string) => updateDrawer(project, (d) => ({ ...d, active: id }));

  const drag = (e: React.PointerEvent) => {
    const startY = e.clientY;
    const startH = height;
    let h = startH;
    const move = (ev: PointerEvent) => {
      const max = window.innerHeight * 0.75;
      h = Math.min(max, Math.max(MIN_HEIGHT, startH + (startY - ev.clientY)));
      setHeight(h);
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      if (h !== startH) setPrefs({ terminalHeight: Math.round(h) });
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  return (
    <section
      className={cn("terminal-drawer relative flex shrink-0 flex-col border-t bg-background", !shown && "hidden")}
      style={{ height }}
      aria-label="Terminal"
    >
      <div
        className="absolute inset-x-0 -top-1 z-10 h-2 cursor-row-resize"
        onPointerDown={drag}
        role="separator"
        aria-orientation="horizontal"
        aria-label="Resize the terminal"
      />
      <header className="flex h-9 shrink-0 items-center gap-1 border-b px-2">
        <div className="flex min-w-0 items-center gap-0.5 overflow-x-auto" role="tablist" aria-label="Terminals">
          {tabs.map((t) => (
            <div
              key={t.id}
              className={cn(
                "group flex h-7 items-center gap-1.5 rounded-md pr-1 pl-2.5 text-xs",
                active === t.id ? "bg-selected text-foreground" : "text-muted-foreground hover:bg-hover",
              )}
            >
              <button type="button" role="tab" aria-selected={active === t.id} className="flex cursor-pointer items-center gap-1.5" onClick={() => setActive(t.id)}>
                <SquareTerminal className="size-3.5" />
                Terminal {t.n}
              </button>
              <button
                type="button"
                aria-label={`Close terminal ${t.n}`}
                className="grid size-4 cursor-pointer place-items-center rounded text-muted-foreground opacity-0 outline-none hover:bg-hover hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-focus-within:opacity-100 group-hover:opacity-100"
                onClick={() => close(t.id)}
              >
                <X className="size-3" />
              </button>
            </div>
          ))}
          <Button variant="ghost-muted" size="icon-xs" aria-label="New terminal" title="New terminal" onClick={() => void add()}>
            <Plus />
          </Button>
        </div>
        <Button variant="ghost-muted" size="icon-xs" className="ml-auto" aria-label="Hide the terminal" title="Hide the terminal" onClick={onHide}>
          <X />
        </Button>
      </header>
      <div className="min-h-0 flex-1">
        {tabs.map((t) => (
          <TerminalView key={t.id} t={t} visible={shown && active === t.id} />
        ))}
      </div>
    </section>
  );
}
