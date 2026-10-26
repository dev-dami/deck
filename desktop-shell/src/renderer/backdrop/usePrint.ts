import { type RefObject, useEffect, useState } from "react";
import { loadPicture, render } from "./client";
import { artFor, type Process, type Use, withPicture } from "./inks";
import { readTokens, useLook } from "./live";
import { CUSTOM, framingOf, pictureInfo } from "./pictures";
import { useWallpaperVersion } from "./wallpaper";

// Prints a picture through the theme's process onto a canvas, once per
// theme, mode, picture or size: the window's backdrop (ui/Backdrop.tsx) and
// the first screens' pictures (ui/ArtFigure.tsx) share it.

export interface PrintSize {
  // CSS pixels, and the device pixel ratio.
  w: number;
  h: number;
  dpr: number;
}

export interface Printed {
  process: Process;
  // How long the render took, in ms.
  ms: number;
  // The picture's credit line, and where it came from; null for the
  // person's own picture's source.
  credit: string | null;
  source: string | null;
}

// `picture` is a library id, CUSTOM for the person's own, or "theme" for
// the theme's own. A picture that is gone (cleared storage) falls back to
// the theme's.
export function usePrint(canvas: RefObject<HTMLCanvasElement | null>, size: PrintSize | null, picture: string, use: Use): Printed | null {
  const look = useLook();
  const version = useWallpaperVersion();
  const [printed, setPrinted] = useState<Printed | null>(null);
  const w = size?.w ?? 0;
  const h = size?.h ?? 0;
  const dpr = size?.dpr ?? 1;

  useEffect(() => {
    if (w < 2 || h < 2) return;
    let live = true;
    void (async () => {
      const theme = artFor(look.palette, look.mode, readTokens(), use);
      let id = picture === "theme" ? theme.picture : picture;
      let img = await loadPicture(id, version);
      if (img === null && id !== theme.picture) {
        id = theme.picture;
        img = await loadPicture(id);
      }
      const info = pictureInfo(id);
      const job = { W: Math.round(w * dpr), H: Math.round(h * dpr), dpr, art: withPicture(theme, id), framing: framingOf(info) };
      const { bitmap, ms } = await render(job, img);
      const c = canvas.current;
      if (!live || c === null) {
        bitmap.close();
        return;
      }
      c.width = bitmap.width;
      c.height = bitmap.height;
      c.getContext("bitmaprenderer")?.transferFromImageBitmap(bitmap);
      const mine = id === CUSTOM;
      setPrinted({
        process: theme.process,
        ms,
        credit: img === null ? null : mine ? "Your picture" : (info?.credit ?? null),
        source: img === null || mine ? null : (info?.source ?? null),
      });
    })().catch(() => live && setPrinted(null));
    return () => {
      live = false;
    };
  }, [canvas, look, picture, version, w, h, dpr, use]);

  return printed;
}
