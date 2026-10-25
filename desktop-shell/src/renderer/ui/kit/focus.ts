import { useEffect, useRef } from "react";

// Where a key moves the focus in a list of count items, from current (-1
// when no item has it): the arrows wrap, Home and End go to the ends. null
// means the key does not move the focus.
export function moveIndex(key: string, current: number, count: number): number | null {
  if (count === 0) return null;
  switch (key) {
    case "ArrowDown":
      return current < 0 ? 0 : (current + 1) % count;
    case "ArrowUp":
      return current < 0 ? count - 1 : (current - 1 + count) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}

// Where Tab (or Shift+Tab, back) moves the focus inside a trap of count
// elements: it wraps at both ends, and starts at an end when no element
// inside has the focus.
export function trapIndex(current: number, count: number, back: boolean): number {
  if (current < 0) return back ? count - 1 : 0;
  return back ? (current - 1 + count) % count : (current + 1) % count;
}

const FOCUSABLE = [
  "a[href]",
  "button:not(:disabled)",
  "input:not(:disabled):not([type=hidden])",
  "select:not(:disabled)",
  "textarea:not(:disabled)",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

export function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => !el.hidden && el.getClientRects().length > 0);
}

// Gives the focus back to prev when a popup closes, unless something else
// took it on the way (a menu item that focuses the composer, say).
export function restoreFocus(prev: Element | null, popup: Element | null): void {
  const now = document.activeElement;
  const lost = now === null || now === document.body || (popup !== null && popup.contains(now));
  if (lost && prev instanceof HTMLElement && prev.isConnected) prev.focus();
}

// Keeps Tab inside a dialog while it is open and gives the focus back to
// what had it before. The dialog's own autofocus wins; otherwise its first
// field, else its first control, takes the focus.
export function useFocusTrap(ref: React.RefObject<HTMLElement | null>): void {
  const back = useRef<Element | null>(null);
  useEffect(() => {
    const root = ref.current;
    back.current = document.activeElement;
    if (root !== null && !root.contains(document.activeElement)) {
      const list = focusables(root);
      (list.find((el) => el.matches("input, textarea, select")) ?? list[0] ?? root).focus();
    }
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Tab" || root === null) return;
      const list = focusables(root);
      if (list.length === 0) return;
      e.preventDefault();
      list[trapIndex(list.indexOf(document.activeElement as HTMLElement), list.length, e.shiftKey)]?.focus();
    };
    root?.addEventListener("keydown", key);
    return () => {
      root?.removeEventListener("keydown", key);
      restoreFocus(back.current, root);
    };
  }, [ref]);
}
