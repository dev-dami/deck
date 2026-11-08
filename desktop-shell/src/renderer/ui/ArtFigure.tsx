import { type ReactNode, useEffect, useRef, useState } from "react";
import { type PrintSize, usePrint } from "../backdrop/usePrint";
import { cn } from "../lib/cn";

// The first screens' picture (the Welcome, the connect screen): the
// backdrop's picture (the theme's own, or the person's) printed through
// the theme's process at the size of its box, with its credit. The
// screens set it full bleed behind everything (Welcome.tsx FirstFrame).
// Decorative: the credit is the only text. Children lie over the picture
// and under the credit (the first screens' scrim).
export function ArtFigure({ className, creditClassName, children }: { className?: string; creditClassName?: string; children?: ReactNode }) {
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

  const printed = usePrint(canvas, size, "theme", "print");
  const line = printed?.credit === null || printed === null ? null : [printed.credit, printed.source].filter(Boolean).join(". ");

  return (
    <figure className={cn("flex flex-col", className)}>
      <div ref={box} aria-hidden className="art-print relative min-h-0 flex-1 overflow-hidden" data-print={printed?.process}>
        <canvas
          ref={canvas}
          className={cn(
            "absolute inset-0 size-full opacity-0 transition-opacity duration-240 ease-(--ease)",
            printed !== null && "opacity-100",
          )}
        />
      </div>
      {children}
      <figcaption className={cn("text-pretty text-3xs tracking-wide", creditClassName)}>{line}</figcaption>
    </figure>
  );
}
