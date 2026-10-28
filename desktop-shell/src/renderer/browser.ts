import type { BrowserErrorCode, BrowserTab } from "../shared/bridge";

// The Browser tab's pure parts: what the address bar accepts and which tab
// shows. Pure, so the tests drive it.

const LOCAL = /^(localhost|127\.\d+\.\d+\.\d+|\[::1\])(:\d+)?(\/|$)/i;
const HOSTLIKE = /^[a-z0-9-]+(\.[a-z0-9-]+)+(:\d+)?(\/|$)/i;

// The address typed in the bar as a URL to open, or null when it is not one
// the browser opens: http and https only. A bare host gets https, and a
// local address http, as the person's own dev server answers.
export function normalizeUrl(input: string): string | null {
  const text = input.trim();
  if (text === "" || /\s/.test(text)) return null;
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(text);
  if (scheme !== null && !LOCAL.test(text)) {
    const s = scheme[1]?.toLowerCase();
    if ((s !== "http" && s !== "https") || !/^https?:\/\/[^/]/i.test(text)) return null;
    return text;
  }
  if (LOCAL.test(text)) return `http://${text}`;
  return HOSTLIKE.test(text) ? `https://${text}` : null;
}

export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
}

// The tab to show: the one picked if it is still there, else the one the
// person holds, else the first.
export function activeTab(tabs: readonly BrowserTab[], picked: string | null): BrowserTab | null {
  return tabs.find((t) => t.id === picked) ?? tabs.find((t) => t.user_in_control) ?? tabs[0] ?? null;
}

// Who has the tab, for the line above the page.
export function driverLabel(t: BrowserTab): string {
  if (t.user_in_control) return "You are in control";
  return t.owner === "" ? "No agent is using this tab" : `${t.owner} is driving`;
}

const ERROR_TEXT: Record<BrowserErrorCode, string> = {
  browser_unavailable: "The browser is not available.",
  user_in_control: "You are in control of this tab.",
  stale_snapshot: "The page changed; the agent must look again.",
  not_owner: "That tab belongs to another agent.",
  origin_denied: "That site was not allowed.",
  cancelled: "The command was cancelled.",
  timeout: "The page took too long.",
  invalid_request: "Deck cannot open that address.",
  unsupported_image: "Deck cannot show that image.",
};

// What to tell the person when a command of theirs is refused; empty when
// it was not.
export function browserErrorText(code: string): string {
  return code === "" ? "" : (ERROR_TEXT[code as BrowserErrorCode] ?? "The browser refused that.");
}

export function tabLabel(t: BrowserTab): string {
  return t.title !== "" ? t.title : hostOf(t.url) || "New tab";
}
