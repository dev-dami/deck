import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { useEffect, useMemo, useRef, useState } from "react";
import type { BrowserTab, DeckBridge } from "../../shared/bridge";
import { agentColour } from "../agents";
import { cn } from "../lib/cn";
import { type DetectedPort, portLabel } from "../ports";
import type { ProjectApi } from "../project";
import { ArrowLeft, ArrowRight, ChevronDown, Copy, ExternalLink, Globe, MonitorPlay, Plus, RefreshCw, X } from "./icons";
import { Button } from "./kit/button";
import { EmptyState } from "./kit/empty";
import { MenuItem, Popover } from "./kit/menu";
import { lastOpened, onAddressAsk, takeAddressAsk, useBrowserTabs } from "./useBrowser";
import { portFrom, useOpenPorts, usePreviewTarget } from "./usePorts";

const openInBrowser = (url: string) => window.open(url, "_blank", "noopener");

// A port as a chip: a click opens it in the Preview tab (an https server
// opens in the browser); the arrow has the other ways.
export function PortChip({ p, onPreview, className }: { p: DetectedPort; onPreview: (url: string) => void; className?: string }) {
  const [menu, setMenu] = useState(false);
  const http = p.url.startsWith("http:");
  const label = portLabel(p.port);
  const run = (f: () => void) => () => {
    setMenu(false);
    f();
  };
  return (
    <Popover
      open={menu}
      onClose={() => setMenu(false)}
      label={`Port ${p.port}`}
      className="w-48"
      trigger={
        <span
          className={cn("port-chip liquid-clear inline-flex h-6 shrink-0 items-center rounded-full font-mono text-2xs text-muted-foreground", className)}
          data-port={p.port}
        >
          <button
            type="button"
            className="flex h-full cursor-pointer items-center gap-1.5 rounded-l-full pr-1 pl-2 outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={http ? `Preview ${label}` : `Open ${label} in the browser`}
            title={`${p.url}, from ${portFrom(p)}`}
            onClick={() => (http ? onPreview(p.url) : openInBrowser(p.url))}
          >
            <span aria-hidden className="size-1.5 rounded-full bg-success" />
            {label}
          </button>
          <button
            type="button"
            className="grid h-full w-5 cursor-pointer place-items-center rounded-r-full pr-0.5 outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={`More for ${label}`}
            aria-expanded={menu}
            onClick={() => setMenu((m) => !m)}
          >
            <ChevronDown className="size-3" />
          </button>
        </span>
      }
    >
      {http && (
        <MenuItem onClick={run(() => onPreview(p.url))}>
          <MonitorPlay /> Open in preview
        </MenuItem>
      )}
      <MenuItem onClick={run(() => openInBrowser(p.url))}>
        <ExternalLink /> Open in browser
      </MenuItem>
      <MenuItem onClick={run(() => void navigator.clipboard.writeText(p.url).catch(() => {}))}>
        <Copy /> Copy URL
      </MenuItem>
    </Popover>
  );
}

const samePort = (url: string, port: number) => {
  try {
    return Number(new URL(url).port) === port;
  } catch {
    return false;
  }
};

const hostOf = (url: string) => {
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
};

// What a tab shows in the strip: its title, else its host.
const nameOf = (t: BrowserTab) => t.title || hostOf(t.url) || "New tab";

// Whether a dialog, the palette or a menu over the box would sit under the
// view: the view is drawn above the whole window.
function covered(box: DOMRect): boolean {
  if (document.querySelector('[aria-modal="true"]') !== null) return true;
  for (const el of document.querySelectorAll('[role="dialog"], [role="menu"], [role="listbox"]')) {
    const r = el.getBoundingClientRect();
    if (r.right > box.left && r.left < box.right && r.bottom > box.top && r.top < box.bottom) return true;
  }
  return false;
}

// One tab of the strip. An agent's tab has its mark and hue, and says what
// the agent is doing for a moment.
function TabChip({ tab, on, theme, onPick, onClose }: { tab: BrowserTab; on: boolean; theme: Theme | null; onPick: () => void; onClose: () => void }) {
  const o = tab.owner;
  const hue = o === null ? undefined : agentColour(o.agent, o.kind);
  const doing = o !== null && tab.doing !== null && !tab.paused ? `@${o.name} · ${tab.doing}` : null;
  return (
    <div
      role="tab"
      aria-selected={on}
      tabIndex={0}
      data-tab={tab.id}
      data-owner={o === null ? "you" : o.agent}
      data-live={tab.live ? "" : undefined}
      title={doing ?? (o === null ? tab.url : `@${o.name}: ${tab.url}`)}
      className={cn(
        "group/tab flex h-7 min-w-[5.5rem] max-w-[11rem] shrink-0 cursor-pointer items-center gap-1.5 rounded-full pr-1 pl-2.5 text-xs outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
        on ? "bg-segment text-foreground shadow-card inset-shadow-hl" : "text-muted-foreground hover:bg-hover hover:text-foreground",
        !tab.live && !on && "opacity-70",
      )}
      onClick={onPick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onPick();
        }
      }}
    >
      {o === null ? (
        <Globe aria-hidden className="size-3 shrink-0 text-muted-foreground" />
      ) : (
        <span
          aria-hidden
          className={cn("w-3 shrink-0 text-center font-mono text-xs leading-none", tab.doing !== null && !tab.paused && "animate-deck-pulse")}
          style={{ color: hue }}
        >
          {kindMark(theme, o.kind) || "●"}
        </span>
      )}
      <span className={cn("min-w-0 flex-1 truncate", doing !== null && "font-mono text-2xs")}>{doing ?? nameOf(tab)}</span>
      <button
        type="button"
        tabIndex={-1}
        aria-label={`Close ${nameOf(tab)}`}
        className="grid size-4 shrink-0 cursor-pointer place-items-center rounded-full text-faint opacity-0 outline-none hover:bg-hover hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-hover/tab:opacity-100 group-aria-selected/tab:opacity-100"
        onClick={(e) => {
          e.stopPropagation();
          onClose();
        }}
      >
        <X className="size-3" />
      </button>
    </div>
  );
}

// The Preview tab: a small browser. Its pages are views that main owns and
// lays over the box below, at the bounds this tab reports; main hides a
// page while something covers the box. The person's tabs and the tabs of
// the agents that work in the room are in one strip: the dev server opens
// first, any address can be typed, and an agent's tab can be taken over.
export function BrowserPanel({ api, bridge, theme }: { api: ProjectApi; bridge: DeckBridge; theme: Theme | null }) {
  const deck = api.deckId;
  const all = useOpenPorts(api);
  const ports = useMemo(() => all.filter((p) => p.url.startsWith("http:")), [all]);
  const ask = usePreviewTarget(api);
  // The target while its port is open, else the first open port.
  const url = ask !== null && ports.some((p) => samePort(ask.url, p.port)) ? ask.url : (ports[0]?.url ?? null);
  const nonce = ask?.nonce ?? 0;
  const tabs = useBrowserTabs(deck);
  const [picked, setPicked] = useState<string | null>(null);
  const active = tabs.find((t) => t.id === picked) ?? tabs.find((t) => t.owner === null) ?? tabs[0] ?? null;
  const [draft, setDraft] = useState<string | null>(null);
  const [fresh, setFresh] = useState(false);
  const [refused, setRefused] = useState<string | null>(null);
  const address = useRef<HTMLInputElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const kick = useRef<(() => void) | null>(null);
  const tabsNow = useRef(tabs);
  tabsNow.current = tabs;

  const open = (to: string, extra: { tab?: string; newTab?: boolean } = {}) => {
    setRefused(null);
    void bridge.browserOpen({ deck, url: to, ...extra }).then((r) => {
      if (r.ok) setPicked(r.tab);
      else setRefused(r.message);
    });
  };

  // The dev server is where the person starts: it opens when it first
  // answers, or when it is asked for again (a port chip), but not each time
  // this panel mounts, which would take the person's page away.
  useEffect(() => {
    if (url === null) return;
    const key = `${url}#${nonce}`;
    if (lastOpened.get(deck) === key && tabsNow.current.some((t) => t.owner === null)) return;
    lastOpened.set(deck, key);
    open(url);
  }, [url, nonce, deck]);

  // The address shows the tab's page until the person types.
  useEffect(() => setDraft(null), [active?.id, active?.url]);

  // Ctrl or Cmd+L.
  useEffect(() => {
    const focus = () => {
      if (!takeAddressAsk()) return;
      address.current?.focus();
      address.current?.select();
    };
    focus();
    return onAddressAsk(focus);
  }, []);

  // Lays the active page over the box, at most once a frame, and hides it
  // when the box is covered or gone.
  const showing = active?.id ?? null;
  useEffect(() => {
    const el = box.current;
    if (showing === null || el === null) {
      void bridge.browserHide();
      return;
    }
    let raf = 0;
    let last = "";
    const report = () => {
      raf = 0;
      const r = el.getBoundingClientRect();
      const hide = r.width < 1 || r.height < 1 || covered(r);
      const key = hide ? "hidden" : `${r.x},${r.y},${r.width},${r.height}`;
      if (key === last) return;
      last = key;
      if (hide) void bridge.browserHide();
      else void bridge.browserShow(showing, { x: r.x, y: r.y, w: r.width, h: r.height });
    };
    const later = () => {
      if (raf === 0) raf = requestAnimationFrame(report);
    };
    kick.current = later;
    const ro = new ResizeObserver(later);
    ro.observe(el);
    const mo = new MutationObserver(later);
    mo.observe(document.body, { childList: true, subtree: true });
    window.addEventListener("resize", later);
    later();
    return () => {
      kick.current = null;
      ro.disconnect();
      mo.disconnect();
      window.removeEventListener("resize", later);
      cancelAnimationFrame(raf);
      void bridge.browserHide();
    };
  }, [bridge, showing]);

  const go = () => {
    const text = (draft ?? "").trim();
    if (text === "") return;
    open(text, fresh || active === null ? { newTab: active === null || fresh } : { tab: active.id });
    setFresh(false);
    address.current?.blur();
  };
  const nav = (n: "back" | "forward" | "reload") => {
    if (active !== null) void bridge.browserNav(active.id, { action: n });
  };
  const agent = active?.owner ?? null;
  const shown = fresh ? "" : (draft ?? active?.url ?? "");

  return (
    <section aria-label="Browser" className="browser flex min-h-0 flex-1 flex-col">
      <div role="tablist" aria-label="Tabs" className="flex h-10 shrink-0 items-center gap-1 overflow-x-auto border-hairline border-b px-2 [scrollbar-width:none]">
        {tabs.map((t) => (
          <TabChip
            key={t.id}
            tab={t}
            on={t.id === active?.id && !fresh}
            theme={theme}
            onPick={() => {
              setFresh(false);
              setPicked(t.id);
            }}
            onClose={() => {
              if (picked === t.id) setPicked(null);
              void bridge.browserClose(t.id);
            }}
          />
        ))}
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label="New tab"
          title="New tab"
          onClick={() => {
            setFresh(true);
            setDraft("");
            requestAnimationFrame(() => address.current?.focus());
          }}
        >
          <Plus />
        </Button>
      </div>
      <div className="panel-tools relative flex h-11 shrink-0 items-center gap-1 border-hairline border-b px-2 text-xs">
        <Button variant="ghost-muted" size="icon-xs" aria-label="Back" title="Back" disabled={active?.canBack !== true} onClick={() => nav("back")}>
          <ArrowLeft />
        </Button>
        <Button variant="ghost-muted" size="icon-xs" aria-label="Forward" title="Forward" disabled={active?.canForward !== true} onClick={() => nav("forward")}>
          <ArrowRight />
        </Button>
        <Button variant="ghost-muted" size="icon-xs" aria-label="Reload" title="Reload" disabled={active === null} onClick={() => nav("reload")}>
          <RefreshCw className={cn(active?.loading === true && "animate-spin")} />
        </Button>
        {/* The address: a recessed capsule, as Safari draws it. */}
        <div
          className="mx-1 flex h-7 min-w-0 flex-1 items-center rounded-full bg-hover font-mono text-2xs ring-1 ring-hairline transition-shadow focus-within:ring-2 focus-within:ring-ring"
          title={active?.title || active?.url}
        >
          <input
            ref={address}
            aria-label="Address"
            className="h-full min-w-0 flex-1 bg-transparent px-3 text-foreground outline-none placeholder:text-faint"
            value={shown}
            placeholder="Search or enter an address"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            onFocus={(e) => e.currentTarget.select()}
            onChange={(e) => setDraft(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") go();
              else if (e.key === "Escape") {
                setDraft(null);
                setFresh(false);
                setRefused(null);
                e.currentTarget.blur();
              }
            }}
          />
        </div>
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label="Open in browser"
          title="Open in your browser"
          disabled={active === null}
          onClick={() => active !== null && openInBrowser(active.url)}
        >
          <ExternalLink />
        </Button>
        {/* A hairline that runs while the page loads. */}
        {active?.loading === true && (
          <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-[-1px] h-px overflow-hidden" data-loading="">
            <div className="browser-load h-full w-1/4 bg-primary" />
          </div>
        )}
      </div>
      {agent !== null && active !== null && (
        <div
          className="flex h-8 shrink-0 items-center gap-2 border-hairline border-b px-3 text-2xs text-muted-foreground"
          data-agent-bar={agent.agent}
          data-taken={active.paused ? "" : undefined}
        >
          <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", active.doing !== null && !active.paused && "animate-deck-pulse")} style={{ background: agentColour(agent.agent, agent.kind) }} />
          <span className="min-w-0 flex-1 truncate font-mono">
            {active.paused ? `You have this tab. @${agent.name} waits.` : active.doing !== null ? `@${agent.name} · ${active.doing}` : `@${agent.name} is using this tab`}
          </span>
          <Button variant="outline" size="xs" onClick={() => void bridge.browserPause(active.id, !active.paused)}>
            {active.paused ? "Hand back" : "Take over"}
          </Button>
        </div>
      )}
      <div className="relative flex min-h-0 flex-1 flex-col bg-surface-1">
        <div ref={box} className="browser-box min-h-0 flex-1 bg-background" data-browser={active?.id ?? ""}>
          {active === null || fresh ? (
            <Blank ports={ports} onOpen={(u) => open(u, { newTab: fresh })} refused={refused} />
          ) : (
            refused !== null && <p className="px-4 py-3 text-center text-muted-foreground text-xs">{refused}</p>
          )}
        </div>
      </div>
    </section>
  );
}

// A tab with nothing in it: the dev servers that answer, as the Preview tab
// always showed, and where the person can type any address above.
function Blank({ ports, onOpen, refused }: { ports: DetectedPort[]; onOpen: (url: string) => void; refused: string | null }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-4 text-center">
      {ports.length === 0 ? (
        <EmptyState
          icon={Globe}
          title="No dev server is running."
          hint="Start one in the terminal or ask an agent. Its port shows here when it answers. Type an address above to browse."
        />
      ) : (
        <>
          <EmptyState icon={Globe} title="Dev servers" hint="Open one, or type an address above." className="py-2" />
          <div className="flex flex-wrap justify-center gap-1.5">
            {ports.map((p) => (
              <PortChip key={p.port} p={p} onPreview={onOpen} />
            ))}
          </div>
        </>
      )}
      {refused !== null && <p className="text-muted-foreground text-xs">{refused}</p>}
    </div>
  );
}
