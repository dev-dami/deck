import type { AgentLine, Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { useEffect, useRef, useState } from "react";
import type { BrowserCommand, BrowserTab, DeckBridge } from "../../shared/bridge";
import { agentColour, agentKind, agentTint } from "../agents";
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

// One tab of the strip. An agent's tab has its mark and hue, which pulses
// while the agent works (what it does is in the bar under the toolbar, and on
// hover). A page let go to save memory is dimmed: it comes back at its
// address when its agent returns.
function TabChip({
  tab,
  on,
  agent,
  theme,
  onPick,
  onClose,
}: {
  tab: BrowserTab;
  on: boolean;
  agent: { name: string; kind: string; hue: string } | null;
  theme: Theme | null;
  onPick: () => void;
  onClose: () => void;
}) {
  const working = agent !== null && tab.activity !== "" && !tab.user_in_control;
  const label = tabLabel(tab);
  return (
    <div
      data-tab={tab.id}
      data-owner={tab.owner || "you"}
      data-asleep={tab.asleep ? "" : undefined}
      data-working={working ? "" : undefined}
      className={cn(
        "group/tab flex h-7 min-w-[5.5rem] max-w-[11rem] items-center gap-1.5 rounded-full pr-1 pl-2.5 text-xs transition-colors",
        on ? "bg-segment text-foreground shadow-card inset-shadow-hl" : "text-muted-foreground hover:bg-hover hover:text-foreground",
        tab.asleep && !on && "opacity-70",
      )}
    >
      {agent === null ? (
        <Globe aria-hidden className="size-3 shrink-0 text-muted-foreground" />
      ) : (
        <span
          aria-hidden
          className={cn("w-3 shrink-0 text-center font-mono text-xs leading-none", working && "animate-deck-pulse")}
          style={{ color: agent.hue }}
        >
          {kindMark(theme, agent.kind) || "●"}
        </span>
      )}
      <button
        type="button"
        role="tab"
        aria-selected={on}
        aria-label={label}
        title={working ? `@${agent.name} · ${tab.activity}` : agent === null ? tab.url : `@${agent.name}: ${tab.url}`}
        className="min-w-0 flex-1 cursor-pointer truncate text-left outline-none focus-visible:underline"
        onClick={onPick}
      >
        {label}
      </button>
      <Button variant="ghost-muted" size="icon-xs" aria-label={`Close ${label}`} title="Close" onClick={onClose}>
        <X />
      </Button>
    </div>
  );
}

export function BrowserPanel({
  c,
  bridge,
  sid,
  roster,
  theme,
  onError,
}: {
  c: DeckClient;
  bridge: DeckBridge;
  sid: string;
  roster: readonly AgentLine[];
  theme: Theme | null;
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

  // The agent behind a tab: its name and kind, from the room's roster.
  const agentOf = (t: BrowserTab): { name: string; kind: string; hue: string } | null => {
    if (t.owner === "") return null;
    const kind = agentKind(t.owner, [...roster]);
    return { name: t.owner, kind, hue: agentColour(t.owner, kind, agentTint(t.owner, roster)) };
  };

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

  const agent = tab === null ? null : agentOf(tab);
  return (
    <section aria-label="Browser" className="browser flex min-h-0 flex-1 flex-col">
      <div role="tablist" aria-label="Open pages" className="flex h-10 shrink-0 items-center gap-1 overflow-x-auto border-hairline border-b px-2 [scrollbar-width:none]">
        {live.tabs.map((t) => (
          <TabChip
            key={t.id}
            tab={t}
            on={t.id === tab?.id}
            agent={agentOf(t)}
            theme={theme}
            onPick={() => setPicked(t.id)}
            onClose={() => void run({ action: "close", tab_id: t.id })}
          />
        ))}
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label="New tab"
          title="New tab"
          onClick={() => {
            const url = normalizeUrl(address);
            if (url !== null) void run({ action: "open", url });
            else onError("Type an http or https address first.");
          }}
        >
          <Plus />
        </Button>
      </div>
      <div className="panel-tools relative flex h-11 shrink-0 items-center gap-1 border-hairline border-b px-2 text-xs">
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
        {/* The address: a recessed capsule, as Safari draws it. */}
        <div className="mx-1 flex h-7 min-w-0 flex-1 items-center rounded-full bg-hover font-mono text-2xs ring-1 ring-hairline transition-shadow focus-within:ring-2 focus-within:ring-ring">
          <input
            aria-label="Address"
            className="h-full min-w-0 flex-1 bg-transparent px-3 text-foreground outline-none placeholder:text-faint"
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
        {/* A hairline that runs while the page loads. */}
        {tab?.loading === true && (
          <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-[-1px] h-px overflow-hidden" data-loading="">
            <div className="browser-load h-full w-1/4 bg-primary" />
          </div>
        )}
      </div>
      {tab !== null && agent !== null && (
        <div className="browser-driver flex h-8 shrink-0 items-center gap-2 border-hairline border-b px-3 text-2xs text-muted-foreground" role="status" data-taken={tab.user_in_control ? "" : undefined}>
          <span
            aria-hidden
            className={cn("size-1.5 shrink-0 rounded-full", tab.activity !== "" && !tab.user_in_control && "animate-deck-pulse")}
            style={{ background: tab.user_in_control ? "var(--success)" : agent.hue }}
          />
          <span className="min-w-0 flex-1 truncate font-mono">
            {tab.activity !== "" && !tab.user_in_control ? `@${agent.name} · ${tab.activity}` : driverLabel({ ...tab, owner: agent.name })}
          </span>
          <Button variant="outline" size="xs" onClick={() => void run({ action: tab.user_in_control ? "release_control" : "take_control", tab_id: tab.id })}>
            {tab.user_in_control ? "Release" : "Take control"}
          </Button>
        </div>
      )}
      <div ref={box} className="browser-box min-h-0 flex-1 bg-background" data-browser-tab={tabId ?? undefined}>
        {tab === null && (
          <EmptyState icon={Globe} title="No page is open." hint="When an agent opens a page it shows here. You can type an address above to open one yourself." />
        )}
      </div>
    </section>
  );
}
