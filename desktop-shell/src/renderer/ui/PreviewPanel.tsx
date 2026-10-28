import { ArrowLeft, ArrowRight, ChevronDown, Copy, ExternalLink, Globe, MonitorPlay, RefreshCw } from "./icons";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { DeckBridge, PreviewState } from "../../shared/bridge";
import { cn } from "../lib/cn";
import { type DetectedPort, portLabel } from "../ports";
import type { ProjectApi } from "../project";
import { Button } from "./kit/button";
import { EmptyState } from "./kit/empty";
import { MenuItem, Popover } from "./kit/menu";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { portFrom, setPreviewTarget, useOpenPorts, usePreviewTarget } from "./usePorts";

// The Preview tab: a dev server's page in a view that main owns. The page
// is no part of this window; main lays it over the box below, at the bounds
// this tab reports, and hides it while something covers the box.

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

const DEVICES = [
  { label: "Responsive", width: 0 },
  { label: "390", width: 390 },
  { label: "768", width: 768 },
  { label: "1280", width: 1280 },
] as const;

const samePort = (url: string, port: number) => {
  try {
    return Number(new URL(url).port) === port;
  } catch {
    return false;
  }
};

const pathOf = (url: string) => {
  try {
    const u = new URL(url);
    return `${u.pathname}${u.search}${u.hash}`;
  } catch {
    return "/";
  }
};

const hostOf = (url: string) => {
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
};

// Whether a dialog, the palette or a menu over the box would sit under the
// view: the view is drawn above the whole window.
export function covered(box: DOMRect): boolean {
  if (document.querySelector('[aria-modal="true"]') !== null) return true;
  for (const el of document.querySelectorAll('[role="dialog"], [role="menu"], [role="listbox"]')) {
    const r = el.getBoundingClientRect();
    if (r.right > box.left && r.left < box.right && r.bottom > box.top && r.top < box.bottom) return true;
  }
  return false;
}

export function PreviewPanel({ api, bridge }: { api: ProjectApi; bridge: DeckBridge }) {
  const all = useOpenPorts(api);
  const ports = useMemo(() => all.filter((p) => p.url.startsWith("http:")), [all]);
  const target = usePreviewTarget(api);
  // The target while its port is open, else the first open port.
  const url = target !== null && ports.some((p) => samePort(target, p.port)) ? target : (ports[0]?.url ?? null);
  const [state, setState] = useState<PreviewState | null>(null);
  const [refused, setRefused] = useState(false);
  const [device, setDevice] = useState(0);
  const [avail, setAvail] = useState(0);
  const frame = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const shown = state?.url || url || "";
  const [path, setPath] = useState(pathOf(shown));
  useEffect(() => setPath(pathOf(shown)), [shown]);

  const width = device === 0 || avail === 0 ? avail : Math.min(device, avail);
  const zoom = device > avail && avail > 0 ? Math.max(0.25, avail / device) : 1;
  const zoomNow = useRef(zoom);
  zoomNow.current = zoom;
  const kick = useRef<(() => void) | null>(null);

  useEffect(() => bridge.onPreview(setState), [bridge]);

  useEffect(() => {
    if (url === null) return;
    setState(null);
    setRefused(false);
    void bridge.previewOpen({ url }).then((ok) => setRefused(!ok));
  }, [bridge, url]);

  useLayoutEffect(() => {
    const el = frame.current;
    if (el === null) return;
    const ro = new ResizeObserver(() => setAvail(el.clientWidth));
    ro.observe(el);
    setAvail(el.clientWidth);
    return () => ro.disconnect();
  }, [url]);

  // Lays the view over the box, at most once a frame, and hides it when the
  // box is covered or gone.
  useEffect(() => {
    const el = box.current;
    if (url === null || el === null) return;
    let raf = 0;
    let last = "";
    const report = () => {
      raf = 0;
      const r = el.getBoundingClientRect();
      const hide = r.width < 1 || r.height < 1 || covered(r);
      const z = zoomNow.current;
      const key = hide ? "hidden" : `${r.x},${r.y},${r.width},${r.height},${z}`;
      if (key === last) return;
      last = key;
      if (hide) void bridge.previewHide();
      else void bridge.previewBounds({ x: r.x, y: r.y, w: r.width, h: r.height, zoom: z });
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
      void bridge.previewHide();
    };
  }, [bridge, url]);

  useEffect(() => kick.current?.(), [zoom, width]);

  if (url === null) {
    return (
      <EmptyState
        icon={Globe}
        title="No dev server is running."
        hint="Start one in the terminal or ask an agent. Its port shows here when it answers."
        className="py-16"
      />
    );
  }
  const go = () => {
    const p = path.trim();
    void bridge.previewNav({ action: "path", path: p.startsWith("/") ? p : `/${p}` });
  };
  return (
    <section aria-label="Preview" className="preview flex min-h-0 flex-1 flex-col">
      <div className="panel-tools flex h-11 shrink-0 items-center gap-1 border-hairline border-b px-2 text-xs">
        <Button variant="ghost-muted" size="icon-xs" aria-label="Back" title="Back" disabled={state?.canBack !== true} onClick={() => void bridge.previewNav({ action: "back" })}>
          <ArrowLeft />
        </Button>
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label="Forward"
          title="Forward"
          disabled={state?.canForward !== true}
          onClick={() => void bridge.previewNav({ action: "forward" })}
        >
          <ArrowRight />
        </Button>
        <Button variant="ghost-muted" size="icon-xs" aria-label="Reload" title="Reload" onClick={() => void bridge.previewNav({ action: "reload" })}>
          <RefreshCw className={cn(state?.loading === true && "animate-spin")} />
        </Button>
        {/* The address: a recessed capsule, as Safari draws it. */}
        <div
          className="mx-1 flex h-7 min-w-0 flex-1 items-center rounded-full bg-hover font-mono text-2xs ring-1 ring-hairline transition-shadow focus-within:ring-2 focus-within:ring-ring"
          title={state?.title || shown}
        >
          {ports.length > 1 ? (
            <select
              aria-label="Port"
              className="h-full shrink-0 cursor-pointer rounded-l-full bg-transparent pl-3 text-muted-foreground outline-none"
              value={ports.find((p) => samePort(url, p.port))?.port}
              onChange={(e) => {
                const p = ports.find((x) => x.port === Number(e.currentTarget.value));
                if (p !== undefined) setPreviewTarget(api, p.url);
              }}
            >
              {ports.map((p) => (
                <option key={p.port} value={p.port}>
                  {hostOf(p.url)}
                </option>
              ))}
            </select>
          ) : (
            <span className="preview-host shrink-0 pl-3 text-muted-foreground">{hostOf(shown)}</span>
          )}
          <input
            aria-label="Path"
            className="h-full min-w-0 flex-1 bg-transparent pr-3 text-foreground outline-none"
            value={path}
            spellCheck={false}
            onChange={(e) => setPath(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") go();
              else if (e.key === "Escape") setPath(pathOf(shown));
            }}
          />
        </div>
        <Button variant="ghost-muted" size="icon-xs" aria-label="Open in browser" title="Open in browser" onClick={() => openInBrowser(shown)}>
          <ExternalLink />
        </Button>
      </div>
      <div className="flex h-10 shrink-0 items-center gap-2 border-hairline border-b px-2.5">
        <div className={segmentTrack()} role="group" aria-label="Device width">
          {DEVICES.map((d) => (
            <button
              key={d.width}
              type="button"
              className={cn(segmentItem({ on: device === d.width }), "px-2.5 font-mono text-2xs")}
              aria-pressed={device === d.width}
              onClick={() => setDevice(d.width)}
            >
              {d.label}
            </button>
          ))}
        </div>
        {zoom < 1 && <span className="ml-auto font-mono text-2xs text-faint tabular-nums">{Math.round(zoom * 100)}%</span>}
      </div>
      <div ref={frame} className="flex min-h-0 flex-1 justify-center bg-surface-1">
        <div ref={box} className="preview-box h-full bg-background" style={{ width }} data-preview={url}>
          {refused && <p className="px-4 py-16 text-center text-muted-foreground text-sm">Deck cannot show this address.</p>}
        </div>
      </div>
    </section>
  );
}
