// The window runs on the person's own machine, so the user agent names it.
export function isMac(): boolean {
  return typeof navigator !== "undefined" && /Mac/.test(navigator.platform || navigator.userAgent);
}

// The label of the shortcut modifier: ⌘ on a Mac, Ctrl elsewhere.
export function modKey(mac: boolean): string {
  return mac ? "⌘" : "Ctrl ";
}
