import { useEffect, useRef, useState } from "react";
import type { BrowserCommand, DeckBridge } from "../../shared/bridge";
import { activeTab, browserErrorText, driverLabel, normalizeUrl, tabLabel } from "../browser";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import { useRoomLive } from "../live";
import { ArrowLeft, ArrowRight, Globe, Plus, RefreshCw, X } from "./icons";
import { Button } from "./kit/button";
import { EmptyState } from "./kit/empty";
import { covered } from "./PreviewPanel";

// The Browser tab: the pages the room's agents open, in a view main owns
// (as Preview does). The person can look, type an address, and Take control,
// which stops the agent's commands on the tab until they release it.

export function BrowserPanel({
  c,
  bridge,
  sid,
  onError,
}: {
  c: DeckClient;
  bridge: DeckBridge;
  sid: string;
  onError: (t: string) => void;
}) {
  const live = useRoomLive(c, sid);
  const [picked, setPicked] = useState<string | null>(null);
  const tab = activeTab(live.tabs, picked);
  const [address, setAddress] = useState("");
  const box = useRef<HTMLDivElement>(null);
  const deckId = c.state.server?.server_id ?? "";
  const tabId = tab?.id ?? null;
  const tabUrl = tab?.url ?? "";
  useEffect(() => setAddress(tabUrl), [tabUrl, tabId]);

  const run = async (command: BrowserCommand) => {
    const r = await c.browserAction?.(sid, command);
    const text = r === null || r === undefined ? "" : browserErrorText(r.error_code);
    if (text !== "") onError(text);
    return r;
  };

  // Lays the view over the box, at most once a frame, and hides it when the
  // box is covered or gone.
  useEffect(() => {
    const el = box.current;
    if (tabId === null || el === null || bridge.browserView === undefined) return;
    const show = bridge.browserView.bind(bridge);
    let raf = 0;
    let last = "";
    const report = () => {
      raf = 0;
      const r = el.getBoundingClientRect();
      const hide = r.width < 1 || r.height < 1 || covered(r);
      const key = hide ? "hidden" : `${r.x},${r.y},${r.width},${r.height}`;
      if (key === last) return;
      last = key;
      const bounds = hide ? null : { x: r.x, y: r.y, w: r.width, h: r.height, zoom: 1 };
      void show({ deck_id: deckId, session_id: sid, tab_id: tabId, bounds }).catch((e: unknown) => onError(e instanceof Error ? e.message : "Deck cannot show the browser."));
    };
    const later = () => {
      if (raf === 0) raf = requestAnimationFrame(report);
    };
    const ro = new ResizeObserver(later);
    ro.observe(el);
    const mo = new MutationObserver(later);
    mo.observe(document.body, { childList: true, subtree: true });
    window.addEventListener("resize", later);
    later();
    return () => {
      ro.disconnect();
      mo.disconnect();
      window.removeEventListener("resize", later);
      cancelAnimationFrame(raf);
      void show({ deck_id: deckId, session_id: sid, tab_id: tabId, bounds: null }).catch(() => {});
    };
  }, [bridge, deckId, sid, tabId, onError]);

  const go = () => {
    const url = normalizeUrl(address);
    if (url === null) {
      onError("Deck opens http and https addresses only.");
      return;
    }
    void run(tab === null ? { action: "open", url } : { action: "navigate", tab_id: tab.id, url });
  };

  if (!live.browserAvailable) {
    return (
      <EmptyState
        icon={Globe}
        title="The browser is not available."
        hint="It runs in the Deck desktop app. Open this project there to let agents use it."
        className="py-16"
      />
    );
  }

  return (
    <section aria-label="Browser" className="browser flex min-h-0 flex-1 flex-col">
      <div role="tablist" aria-label="Open pages" className="flex h-9 shrink-0 items-center gap-1 overflow-x-auto border-hairline border-b px-2">
        {live.tabs.map((t) => (
          <div key={t.id} className={cn("group/tab flex h-7 max-w-40 shrink-0 items-center rounded-full pr-1 pl-3 text-xs", t.id === tab?.id ? "bg-segment text-foreground" : "text-muted-foreground hover:text-foreground")}>
            <button
              type="button"
              role="tab"
              aria-selected={t.id === tab?.id}
              className="min-w-0 flex-1 cursor-pointer truncate text-left outline-none focus-visible:underline"
              title={`${tabLabel(t)} · ${t.owner}`}
              onClick={() => setPicked(t.id)}
            >
              {tabLabel(t)}
            </button>
            <Button variant="ghost-muted" size="icon-xs" aria-label={`Close ${tabLabel(t)}`} title="Close" onClick={() => void run({ action: "close", tab_id: t.id })}>
              <X />
            </Button>
          </div>
        ))}
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label="New tab"
          title="New tab"
          disabled={live.tabs.length >= 16}
          onClick={() => {
            const url = normalizeUrl(address);
            if (url !== null) void run({ action: "open", url });
            else onError("Type an http or https address first.");
          }}
        >
          <Plus />
        </Button>
      </div>
      <div className="flex h-11 shrink-0 items-center gap-1 border-hairline border-b px-2 text-xs">
        <Button variant="ghost-muted" size="icon-xs" aria-label="Back" title="Back" disabled={tab?.can_back !== true} onClick={() => tab && void run({ action: "back", tab_id: tab.id })}>
          <ArrowLeft />
        </Button>
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label="Forward"
          title="Forward"
          disabled={tab?.can_forward !== true}
          onClick={() => tab && void run({ action: "forward", tab_id: tab.id })}
        >
          <ArrowRight />
        </Button>
        <Button variant="ghost-muted" size="icon-xs" aria-label="Reload" title="Reload" disabled={tab === null} onClick={() => tab && void run({ action: "reload", tab_id: tab.id })}>
          <RefreshCw className={cn(tab?.loading === true && "animate-spin")} />
        </Button>
        <div className="mx-1 flex h-7 min-w-0 flex-1 items-center rounded-full bg-hover font-mono text-2xs ring-1 ring-hairline transition-shadow focus-within:ring-2 focus-within:ring-ring">
          <input
            aria-label="Address"
            className="h-full min-w-0 flex-1 bg-transparent px-3 text-foreground outline-none"
            value={address}
            spellCheck={false}
            placeholder="Type an address"
            onChange={(e) => setAddress(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") go();
              else if (e.key === "Escape") setAddress(tabUrl);
            }}
          />
        </div>
        {tab !== null && (
          <Button
            variant={tab.user_in_control ? "default" : "outline"}
            size="xs"
            onClick={() => void run({ action: tab.user_in_control ? "release_control" : "take_control", tab_id: tab.id })}
          >
            {tab.user_in_control ? "Release" : "Take control"}
          </Button>
        )}
      </div>
      {tab !== null && (
        <p className="browser-driver flex h-7 shrink-0 items-center gap-1.5 border-hairline border-b px-3 text-2xs text-muted-foreground" role="status">
          <span aria-hidden className={cn("size-1.5 rounded-full", tab.user_in_control ? "bg-success" : "bg-info")} />
          {driverLabel(tab)}
        </p>
      )}
      <div ref={box} className="browser-box min-h-0 flex-1 bg-background" data-browser-tab={tabId ?? undefined}>
        {tab === null && (
          <EmptyState icon={Globe} title="No page is open." hint="When an agent opens a page it shows here. You can type an address above to open one yourself." />
        )}
      </div>
    </section>
  );
}
