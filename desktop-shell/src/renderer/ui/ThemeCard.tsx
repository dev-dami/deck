import { type CSSProperties, useEffect, useRef, useState } from "react";
import { type Art, artFor } from "../backdrop/inks";
import { swatchTokens, useLook } from "../backdrop/live";
import { type PrintSize, printTo } from "../backdrop/usePrint";
import { cn } from "../lib/cn";
import { setPrefs } from "../prefs";
import { DIRECTIONS, type Direction, THEMES, type ThemeInfo } from "../themes";
import { CircleCheck } from "./icons";

// The theme picker of Settings > Appearance: the three art directions as
// prints of their own process, then the colourways of Liquid Glass as
// windows in miniature. One radiogroup, so the arrow keys and the tests
// see every theme.
export function ThemeGrid({ palette }: { palette: string }) {
  const directions = THEMES.filter((t) => t.id in DIRECTIONS);
  const colourways = THEMES.filter((t) => !(t.id in DIRECTIONS));
  return (
    <div className="flex flex-col gap-4 py-4" role="radiogroup" aria-label="Theme">
      <div className="grid grid-cols-1 gap-3 @md:grid-cols-3">
        {directions.map((t) => (
          <DirectionCard key={t.id} t={t} d={DIRECTIONS[t.id as Direction["id"]]} on={palette === t.id} />
        ))}
      </div>
      <p className="pt-1 text-2xs text-faint">Colourways of Liquid Glass</p>
      <div className="grid grid-cols-2 gap-x-3 gap-y-3 @md:grid-cols-3 @xl:grid-cols-4">
        {colourways.map((t) => (
          <ThemeCard key={t.id} t={t} on={palette === t.id} />
        ))}
      </div>
    </div>
  );
}

// A preview is small: the processes work in a finer cell, so a hand of
// cards or a screen of dots still reads at the size of a card.
function small(art: Art): Art {
  if (art.process === "stitch") return { ...art, cell: 3 };
  if (art.process === "halftone") return { ...art, pitch: 3 };
  return art;
}

// A print's corners: glass is rounded, a riso is cut square, felt is soft.
const RADIUS: Record<Direction["id"], string> = { glass: "rounded-[10px]", riso: "rounded-[3px]", stitch: "rounded-[12px]" };

// An art direction: its picture printed through its process, in the mode
// on screen, its name in its own display face, and what it is made of.
function DirectionCard({ t, d, on }: { t: ThemeInfo; d: Direction; on: boolean }) {
  const { mode } = useLook();
  const box = useRef<HTMLSpanElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const [size, setSize] = useState<PrintSize | null>(null);
  const [drawn, setDrawn] = useState(false);

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
  }, []);

  useEffect(() => {
    if (size === null || size.w < 2 || size.h < 2) return;
    let live = true;
    const art = small(artFor(t.id, mode, swatchTokens(t, mode), "print"));
    printTo(canvas, art, "theme", size, 0, () => live)
      .then((p) => live && p !== null && setDrawn(true))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [t, mode, size]);

  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      aria-label={t.label}
      onClick={() => setPrefs({ palette: t.id })}
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

// A colourway: its miniature, the dark half and the light half split on a
// slant for a theme that follows the mode, and its name.
export function ThemeCard({ t, on }: { t: ThemeInfo; on: boolean }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      title={t.light !== undefined ? `${t.label}: light and dark` : undefined}
      onClick={() => setPrefs({ palette: t.id })}
      className="group flex cursor-pointer flex-col gap-2 text-left outline-none"
    >
      <span
        className={cn(
          "relative block aspect-[16/10] w-full overflow-hidden rounded-[10px] ring-1 ring-hairline transition-[box-shadow,transform] duration-200 group-hover:-translate-y-px group-focus-visible:ring-2 group-focus-visible:ring-ring",
          on && "ring-2 ring-primary ring-offset-2 ring-offset-background group-focus-visible:ring-primary",
        )}
      >
        <Miniature s={t.swatch} light={t.light === undefined && t.mode === "light"} />
        {t.light !== undefined && <Miniature s={t.light} light style={{ clipPath: "polygon(64% 0, 100% 0, 100% 100%, 36% 100%)" }} />}
      </span>
      <span className="flex items-center gap-1.5 text-xs">
        <span className={cn("min-w-0 truncate", on ? "font-medium text-foreground" : "text-muted-foreground")}>{t.label}</span>
        {on && <CircleCheck aria-hidden weight="fill" className="ml-auto size-3.5 shrink-0 text-primary" />}
      </span>
    </button>
  );
}

// A theme in miniature, as a window of it: its backdrop lit by its accent,
// a glass sidebar with the chosen row, and a sheet with text, the person's
// bubble and the composer. The glass is drawn, not blurred: a tint, a rim
// and a highlight.
function Miniature({ s, light, style }: { s: readonly [string, string, string, string]; light: boolean; style?: CSSProperties }) {
  const [bg, side, text, accent] = s;
  const mix = (c: string, pct: number) => `color-mix(in srgb, ${c} ${pct}%, transparent)`;
  const glass = light
    ? "inset 0 0 0 0.5px rgb(255 255 255 / 0.9), inset 0 1px 0 rgb(255 255 255 / 1), 0 1px 3px rgb(0 0 0 / 0.08)"
    : "inset 0 0 0 0.5px rgb(255 255 255 / 0.14), inset 0 1px 0 rgb(255 255 255 / 0.16), 0 1px 3px rgb(0 0 0 / 0.3)";
  return (
    <span
      aria-hidden
      className="absolute inset-0"
      style={{
        background: `radial-gradient(90% 80% at 0% 0%, ${mix(accent, light ? 30 : 42)}, transparent 70%), radial-gradient(70% 70% at 100% 100%, ${mix(text, light ? 8 : 10)}, transparent 70%), color-mix(in oklab, ${bg} ${light ? 82 : 62}%, ${light ? "#8a90a0" : "#000"})`,
        ...style,
      }}
    >
      <span
        className="absolute top-[10%] bottom-[10%] left-[6%] flex w-[25%] flex-col gap-[7%] rounded-[6px] p-[5%] pt-[14%]"
        style={{ background: mix(side, light ? 62 : 58), boxShadow: glass }}
      >
        <span className="h-[7%] min-h-[4px] rounded-full" style={{ background: accent }} />
        <span className="h-[5%] min-h-[3px] w-4/5 rounded-full" style={{ background: mix(text, 32) }} />
        <span className="h-[5%] min-h-[3px] w-3/5 rounded-full" style={{ background: mix(text, 32) }} />
      </span>
      <span
        className="absolute top-[10%] right-[6%] bottom-[10%] left-[35%] flex flex-col gap-[7%] rounded-[6px] p-[6%]"
        style={{ background: bg, boxShadow: `inset 0 0 0 0.5px ${mix(text, 10)}, 0 2px 8px rgb(0 0 0 / ${light ? 0.1 : 0.35})` }}
      >
        <span className="h-[6%] min-h-[3px] w-3/5 rounded-full" style={{ background: mix(text, 70) }} />
        <span className="h-[6%] min-h-[3px] w-4/5 rounded-full" style={{ background: mix(text, 30) }} />
        <span className="h-[12%] min-h-[6px] w-2/5 self-end rounded-full" style={{ background: mix(accent, light ? 22 : 34) }} />
        <span className="mt-auto h-[16%] min-h-[8px] rounded-full" style={{ background: mix(side, light ? 70 : 80), boxShadow: glass }} />
      </span>
    </span>
  );
}
