import { type RefObject, useEffect, useState } from "react";
import { loadPicture, render } from "./client";
import { type Art, artFor, artProcess, type Process, type Use, withPicture } from "./inks";
import { readTokens, useLook } from "./live";
import { CUSTOM, framingOf, pictureInfo } from "./pictures";
import { useWallpaperVersion } from "./wallpaper";

// Prints a picture through an art direction's process onto a canvas: the
// window's backdrop (ui/Backdrop.tsx), the first screens' pictures
// (ui/ArtFigure.tsx) and the theme cards' previews (ui/ThemeCard.tsx).

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
  // The picture's credit line and where it came from; null when there was
  // no picture, and the source is null for the person's own.
  credit: string | null;
  source: string | null;
}

// Prints `picture` (a library id, CUSTOM, or "theme" for the art's own)
// in `art` onto the canvas. A picture that is gone (cleared storage) falls
// back to the art's own. Null when `live()` turned false meanwhile.
export async function printTo(
  canvas: RefObject<HTMLCanvasElement | null>,
  art: Art,
  picture: string,
  size: PrintSize,
  version: number,
  live: () => boolean,
): Promise<Printed | null> {
  let id = picture === "theme" ? art.picture : picture;
  let img = await loadPicture(id, version);
  if (img === null && id !== art.picture) {
    id = art.picture;
    img = await loadPicture(id);
  }
  const info = pictureInfo(id);
  const job = { W: Math.round(size.w * size.dpr), H: Math.round(size.h * size.dpr), dpr: size.dpr, art: withPicture(art, id), framing: framingOf(info) };
  const { bitmap, ms } = await render(job, img);
  const c = canvas.current;
  if (!live() || c === null) {
    bitmap.close();
    return null;
  }
  c.width = bitmap.width;
  c.height = bitmap.height;
  c.getContext("bitmaprenderer")?.transferFromImageBitmap(bitmap);
  const mine = id === CUSTOM;
  return {
    process: art.process,
    ms,
    credit: img === null ? null : mine ? "Your picture" : (info?.credit ?? null),
    source: img === null || mine ? null : (info?.source ?? null),
  };
}

// The theme on screen's art, printed once per theme, mode, picture or size.
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
    const art = artFor(look.palette, look.mode, readTokens(), use, artProcess(look.process));
    printTo(canvas, art, picture, { w, h, dpr }, version, () => live)
      .then((p) => live && p !== null && setPrinted(p))
      .catch(() => live && setPrinted(null));
    return () => {
      live = false;
    };
  }, [canvas, look, picture, version, w, h, dpr, use]);

  return printed;
}
