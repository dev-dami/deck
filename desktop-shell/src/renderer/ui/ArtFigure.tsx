import { useEffect, useRef, useState } from "react";
import { type PrintSize, usePrint } from "../backdrop/usePrint";
import { cn } from "../lib/cn";

// A first screen's picture (the Welcome, the connect screen): the theme's
// art direction printed at the size of its box, as a print on the page,
// with its credit under it in faint type. "theme" prints the backdrop's
// picture (the theme's own, or the person's); an id prints that one.
// Decorative: the credit is the only text.
export function ArtFigure({
  picture = "theme",
  className,
  plateClassName,
  creditClassName,
}: {
  picture?: string;
  className?: string;
  plateClassName?: string;
  creditClassName?: string;
}) {
  const box = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const [size, setSize] = useState<PrintSize | null>(null);

  useEffect(() => {
    const el = box.current;
    if (el === null) return;
    let t = 0;
    let first = true;
    const ro = new ResizeObserver(([e]) => {
      if (e === undefined) return;
      const next = { w: Math.round(e.contentRect.width), h: Math.round(e.contentRect.height), dpr: window.devicePixelRatio || 1 };
      // The first size prints at once; a resize waits for the box to settle.
      window.clearTimeout(t);
      const apply = () => setSize((s) => (s !== null && s.w === next.w && s.h === next.h && s.dpr === next.dpr ? s : next));
      if (first) apply();
      else t = window.setTimeout(apply, 160);
      first = false;
    });
    ro.observe(el);
    return () => {
      ro.disconnect();
      window.clearTimeout(t);
    };
  }, []);

  const printed = usePrint(canvas, size, picture, "print");
  const line = printed?.credit === null || printed === null ? null : [printed.credit, printed.source].filter(Boolean).join(". ");

  return (
    <figure className={cn("flex flex-col gap-1.5", className)}>
      <div
        ref={box}
        aria-hidden
        className={cn("art-print relative min-h-0 flex-1 overflow-hidden", plateClassName)}
        data-print={printed?.process}
      >
        <canvas
          ref={canvas}
          className={cn(
            "absolute inset-0 size-full opacity-0 transition-opacity duration-240 ease-(--ease)",
            printed !== null && "opacity-100",
          )}
        />
      </div>
      <figcaption className={cn("min-h-[1lh] text-pretty text-3xs text-faint/80 tracking-wide", creditClassName)}>{line}</figcaption>
    </figure>
  );
}
