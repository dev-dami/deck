// iOS Safari keeps the layout viewport full-height when the keyboard opens and
// only shrinks the visual viewport, so 100dvh leaves the composer underneath it.
// Mirroring the visual viewport into --app-height lets #app shrink with it.
export function trackKeyboard(
  win: Window = window,
  root: HTMLElement = document.documentElement,
): () => void {
  const vv = win.visualViewport;
  if (vv === null) return () => undefined;
  const sync = (): void => {
    root.style.setProperty("--app-height", `${vv.height}px`);
    // Safari scrolls the page to reveal the focused field; the shrunken #app
    // already keeps it visible, so undo the shift.
    if (win.scrollY !== 0) win.scrollTo(0, 0);
  };
  sync();
  vv.addEventListener("resize", sync);
  vv.addEventListener("scroll", sync);
  return () => {
    vv.removeEventListener("resize", sync);
    vv.removeEventListener("scroll", sync);
    root.style.removeProperty("--app-height");
  };
}
