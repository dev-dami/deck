import { type RefObject, useEffect, useRef, useState } from "react";
import { type Art, artFor, artProcess } from "../backdrop/inks";
import { swatchTokens, useLook } from "../backdrop/live";
import { type PrintSize, printTo } from "../backdrop/usePrint";
import { cn } from "../lib/cn";
import { setPrefs } from "../prefs";
import { type Aspects, DIRECTIONS, type Direction, nearestPreset, PRESET_IDS, presetOf, themeInfo } from "../themes";
import { CircleCheck } from "./icons";

// The presets of Settings > Appearance: the three art directions as prints
// of their own process. A pick sets every aspect of the look at once; a
// look that matches none of them is Custom, and says which it came from.
export function ThemeGrid({ look }: { look: Aspects }) {
  const preset = presetOf(look);
  const from = DIRECTIONS[nearestPreset(look)];
  return (
    <div className="flex flex-col gap-2.5">
      <div className="grid grid-cols-1 gap-3 @md:grid-cols-3" role="radiogroup" aria-label="Theme">
        {PRESET_IDS.map((id) => (
          <DirectionCard key={id} d={DIRECTIONS[id]} on={preset === id} />
        ))}
      </div>
      <p className="flex min-h-6 items-center gap-2 text-2xs text-faint" aria-live="polite">
        {preset === "custom" ? (
          <>
            <span className="font-medium text-foreground">Custom</span>
            <span>
              mixed from {from.look.palette === look.palette ? themeInfo(from.id).label : `${themeInfo(from.id).label} in ${themeInfo(look.palette).label}'s colours`}
            </span>
            <button
              type="button"
              className="ml-auto cursor-pointer rounded-full px-2 py-0.5 text-muted-foreground outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              onClick={() => setPrefs({ ...from.look })}
            >
              Back to {themeInfo(from.id).label}
            </button>
          </>
        ) : (
          <span>A preset sets every part of the look. Change any part below to mix your own.</span>
        )}
      </p>
    </div>
  );
}

// A preview is small: the processes work in a finer cell, so a hand of
// cards or a screen of dots still reads at the size of a card.
export function small(art: Art): Art {
  if (art.process === "stitch") return { ...art, cell: 3 };
  if (art.process === "halftone") return { ...art, pitch: 3 };
  return art;
}

// The size of a box, as the processes print to it.
export function useBoxSize(box: RefObject<HTMLElement | null>): PrintSize | null {
  const [size, setSize] = useState<PrintSize | null>(null);
  useEffect(() => {
    const el = box.current;
    if (el === null) return;
    const ro = new ResizeObserver(([e]) => {
      if (e === undefined) return;
      const next = { w: Math.round(e.contentRect.width), h: Math.round(e.contentRect.height), dpr: window.devicePixelRatio || 1 };
      setSize((s) => (s !== null && s.w === next.w && s.h === next.h ? s : next));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [box]);
  return size;
}

// Prints art onto a small canvas once per art, picture or size; true once
// it has drawn. Null art prints nothing.
export function useMiniPrint(canvas: RefObject<HTMLCanvasElement | null>, size: PrintSize | null, art: Art | null, picture = "theme", version = 0): boolean {
  const [drawn, setDrawn] = useState(false);
  const key = art === null ? "" : JSON.stringify(art);
  useEffect(() => {
    if (size === null || size.w < 2 || size.h < 2 || key === "") return;
    let live = true;
    printTo(canvas, JSON.parse(key) as Art, picture, size, version, () => live)
      .then((p) => live && p !== null && setDrawn(true))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [canvas, size, key, picture, version]);
  return drawn && key !== "";
}

// A print's corners: glass is rounded, a riso is cut square, felt is soft.
const RADIUS: Record<Direction["id"], string> = { glass: "rounded-[10px]", riso: "rounded-[3px]", stitch: "rounded-[12px]" };

// An art direction: its picture printed through its process, in the mode
// on screen, its name in its own display face, and what it is made of.
function DirectionCard({ d, on }: { d: Direction; on: boolean }) {
  const { mode } = useLook();
  const t = themeInfo(d.id);
  const box = useRef<HTMLSpanElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const size = useBoxSize(box);
  const drawn = useMiniPrint(canvas, size, small(artFor(t.id, mode, swatchTokens(t, mode), "print", artProcess(d.look.process))));

  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      aria-label={t.label}
      onClick={() => setPrefs({ ...d.look })}
      className="group flex cursor-pointer flex-col gap-2 text-left outline-none"
      data-direction={d.id}
    >
      <span
        ref={box}
        className={cn(
          RADIUS[d.id],
          "theme-print relative block aspect-[16/10] w-full overflow-hidden ring-1 ring-hairline transition-[box-shadow,transform] duration-200 group-hover:-translate-y-px group-focus-visible:ring-2 group-focus-visible:ring-ring",
          on && "ring-2 ring-primary ring-offset-2 ring-offset-background group-focus-visible:ring-primary",
        )}
        data-print={d.id}
      >
        <canvas
          ref={canvas}
          aria-hidden
          className={cn("absolute inset-0 size-full opacity-0 transition-opacity duration-240 ease-(--ease)", drawn && "opacity-100")}
        />
      </span>
      <span className="flex items-baseline gap-1.5">
        <span className={cn("min-w-0 truncate text-[15px] leading-tight", on ? "text-foreground" : "text-muted-foreground")} style={{ fontFamily: d.family }}>
          {t.label}
        </span>
        {on && <CircleCheck aria-hidden weight="fill" className="ml-auto size-3.5 shrink-0 self-center text-primary" />}
      </span>
      <span aria-hidden className="-mt-1 text-2xs text-faint">
        {d.process} · {d.material}
      </span>
    </button>
  );
}
