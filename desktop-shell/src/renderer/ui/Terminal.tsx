import "@xterm/xterm/css/xterm.css";
import { FitAddon } from "@xterm/addon-fit";
import { type ITheme, Terminal as XTerm } from "@xterm/xterm";
import { Plus, SquareTerminal, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "../lib/cn";
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

function TerminalView({ project, id, visible }: { project: ProjectApi; id: string; visible: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const termRef = useRef<XTerm | null>(null);

  useEffect(() => {
    const el = host.current;
    if (el === null) return;
    const unmirror = mirrorStyles(el);
    const term = new XTerm({
      fontFamily: MONO,
      fontSize: 12.5,
      lineHeight: 1.2,
      cursorBlink: true,
      allowProposedApi: false,
      scrollback: 5000,
      theme: themeNow(),
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    termRef.current = term;
    fitRef.current = fit;
    const unwatch = project.watchTerminal(id, (ev) => {
      if (ev.type === "term") term.write(base64Bytes(ev.data));
      else if (ev.type === "term-exit") term.write(`\r\n\x1b[2m[process exited with code ${ev.code}]\x1b[0m\r\n`);
    });
    const input = term.onData((d) => project.terminalWrite(id, d));
    const resize = term.onResize(({ cols, rows }) => project.terminalResize(id, cols, rows));
    const refit = () => {
      if (el.offsetParent === null) return;
      try {
        fit.fit();
      } catch {
        // Not laid out yet.
      }
    };
    const ro = new ResizeObserver(refit);
    ro.observe(el);
    // The window's light or dark follows into the terminal.
    const mode = new MutationObserver(() => {
      term.options.theme = themeNow();
    });
    mode.observe(document.documentElement, { attributes: true, attributeFilter: ["data-mode", "data-palette", "data-accent"] });
    refit();
    return () => {
      ro.disconnect();
      mode.disconnect();
      input.dispose();
      resize.dispose();
      unwatch();
      term.dispose();
      unmirror();
      termRef.current = null;
    };
  }, [project, id]);

  useEffect(() => {
    if (!visible) return;
    requestAnimationFrame(() => {
      try {
        fitRef.current?.fit();
      } catch {
        // Not laid out yet.
      }
      termRef.current?.focus();
    });
  }, [visible]);

  return <div ref={host} className={cn("terminal-view h-full w-full px-3 py-2", !visible && "hidden")} data-terminal={id} />;
}

interface Tab {
  id: string;
  n: number;
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
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [active, setActive] = useState<string | null>(null);
  const [height, setHeight] = useState(280);
  const next = useRef(1);
  const opening = useRef(false);

  const add = useCallback(async () => {
    if (opening.current) return;
    opening.current = true;
    const r = await project.terminalOpen(100, 24);
    opening.current = false;
    if (typeof r !== "string") {
      onError(r.error);
      return;
    }
    const tab = { id: r, n: next.current++ };
    setTabs((t) => [...t, tab]);
    setActive(r);
  }, [project, onError]);

  // The first time the drawer shows, it starts a shell.
  useEffect(() => {
    if (shown && tabs.length === 0) void add();
  }, [shown, tabs.length, add]);

  const close = (id: string) => {
    project.terminalClose(id);
    setTabs((t) => {
      const rest = t.filter((x) => x.id !== id);
      if (active === id) setActive(rest[rest.length - 1]?.id ?? null);
      if (rest.length === 0) onHide();
      return rest;
    });
  };

  // Every shell ends with the drawer's Deck.
  const live = useRef(tabs);
  live.current = tabs;
  useEffect(() => () => {
    for (const t of live.current) project.terminalClose(t.id);
  }, [project]);

  const drag = (e: React.PointerEvent) => {
    const startY = e.clientY;
    const startH = height;
    const move = (ev: PointerEvent) => {
      const max = window.innerHeight * 0.75;
      setHeight(Math.min(max, Math.max(MIN_HEIGHT, startH + (startY - ev.clientY))));
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
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
          <TerminalView key={t.id} project={project} id={t.id} visible={shown && active === t.id} />
        ))}
      </div>
    </section>
  );
}
