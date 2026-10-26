import { useEffect, useRef, useState } from "react";
import { loadPicture, render } from "../backdrop/client";
import { artFor, withPicture } from "../backdrop/inks";
import { readTokens, useLook } from "../backdrop/live";
import { CUSTOM, framingOf, pictureInfo } from "../backdrop/pictures";
import { useWallpaperVersion } from "../backdrop/wallpaper";
import { usePrefs } from "../prefs";

// The window's backdrop: the theme's picture (or the person's) printed
// through the theme's process, once per theme, mode, size or picture, on
// a canvas behind the app. Nothing animates. Until it has drawn, and with
// reduced transparency, the body's plain colour stands in (styles.css).

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

function useReducedTransparency(): boolean {
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
  const look = useLook();
  const { wallpaper } = usePrefs();
  const version = useWallpaperVersion();
  const size = useSettledSize();
  const reduced = useReducedTransparency();
  const canvas = useRef<HTMLCanvasElement>(null);
  const [credit, setCredit] = useState<string | null>(null);

  useEffect(() => {
    const root = document.documentElement;
    if (reduced) {
      root.removeAttribute("data-backdrop");
      return;
    }
    let live = true;
    void (async () => {
      const theme = artFor(look.palette, look.mode, readTokens());
      let id = wallpaper === "theme" ? theme.picture : wallpaper;
      let img = await loadPicture(id, version);
      // A wallpaper that is gone (cleared storage) falls back to the theme's.
      if (img === null && id !== theme.picture) {
        id = theme.picture;
        img = await loadPicture(id);
      }
      const info = pictureInfo(id);
      const W = Math.round(size.w * size.dpr);
      const H = Math.round(size.h * size.dpr);
      const { bitmap, ms } = await render({ W, H, dpr: size.dpr, art: withPicture(theme, id), framing: framingOf(info) }, img);
      const c = canvas.current;
      if (!live || c === null) {
        bitmap.close();
        return;
      }
      c.width = bitmap.width;
      c.height = bitmap.height;
      c.getContext("bitmaprenderer")?.transferFromImageBitmap(bitmap);
      root.setAttribute("data-backdrop", theme.process);
      root.setAttribute("data-backdrop-ms", ms.toFixed(1));
      setCredit(img === null ? null : id === CUSTOM ? "Your picture" : (info?.credit ?? null));
    })().catch(() => root.removeAttribute("data-backdrop"));
    return () => {
      live = false;
    };
  }, [look, wallpaper, version, size, reduced]);

  if (reduced) return null;
  return (
    <>
      <canvas ref={canvas} aria-hidden className="backdrop" />
      {credit !== null && <p className="backdrop-credit">{credit}</p>}
    </>
  );
}
