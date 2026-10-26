import { useEffect, useRef, useState } from "react";
import { usePrint } from "../backdrop/usePrint";
import { usePrefs } from "../prefs";

// The window's backdrop: the theme's picture (or the person's) printed
// through the theme's process, once per theme, mode, size or picture, on
// a canvas behind the app. Nothing animates. It shows behind the glass
// chrome, around the sheets; the first screens print their own picture
// (ArtFigure) and hide it. Until it has drawn, and with reduced
// transparency, the body's plain colour stands in (styles.css).

// A size change waits for the window to settle.
const SETTLE_MS = 160;

function useSettledSize(): { w: number; h: number; dpr: number } {
  const now = () => ({ w: window.innerWidth, h: window.innerHeight, dpr: window.devicePixelRatio || 1 });
  const [size, setSize] = useState(now);
  useEffect(() => {
    let t = 0;
    const on = () => {
      window.clearTimeout(t);
      t = window.setTimeout(() => setSize(now()), SETTLE_MS);
    };
    window.addEventListener("resize", on);
    return () => {
      window.removeEventListener("resize", on);
      window.clearTimeout(t);
    };
  }, []);
  return size;
}

export function useReducedTransparency(): boolean {
  const q = "(prefers-reduced-transparency: reduce)";
  const [on, setOn] = useState(() => window.matchMedia(q).matches);
  useEffect(() => {
    const m = window.matchMedia(q);
    const f = () => setOn(m.matches);
    m.addEventListener("change", f);
    return () => m.removeEventListener("change", f);
  }, []);
  return on;
}

export function Backdrop() {
  const { wallpaper } = usePrefs();
  const size = useSettledSize();
  const reduced = useReducedTransparency();
  const canvas = useRef<HTMLCanvasElement>(null);
  const printed = usePrint(canvas, reduced ? null : size, wallpaper, "window");

  // <html> says that the backdrop has drawn, with which process and in how
  // long, for the CSS and for the tests.
  useEffect(() => {
    const root = document.documentElement;
    if (reduced || printed === null) {
      root.removeAttribute("data-backdrop");
      root.removeAttribute("data-backdrop-ms");
      return;
    }
    root.setAttribute("data-backdrop", printed.process);
    root.setAttribute("data-backdrop-ms", printed.ms.toFixed(1));
  }, [printed, reduced]);

  if (reduced) return null;
  return <canvas ref={canvas} aria-hidden className="backdrop" />;
}
