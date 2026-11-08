import { type RefObject, useEffect, useRef, useState } from "react";
import { type Art, artFor, artProcess } from "../backdrop/inks";
import { swatchTokens, useLook } from "../backdrop/live";
import { type PrintSize, printTo } from "../backdrop/usePrint";
import { cn } from "../lib/cn";
import { setPrefs } from "../prefs";
import { type Aspects, DIRECTIONS, type Direction, nearestPreset, PRESET_IDS, presetOf, themeInfo } from "../themes";
import { CircleCheck } from "./icons";

// The presets of Settings > Appearance: the art directions as prints
// of their own process. A pick sets every aspect of the look at once; a
// look that matches none of them is Custom, and says which it came from.
export function ThemeGrid({ look }: { look: Aspects }) {
  const preset = presetOf(look);
  const from = DIRECTIONS[nearestPreset(look)];
  return (
    <div className="flex flex-col gap-2.5">
      <div className="grid grid-cols-2 gap-3 @xl:grid-cols-3" role="radiogroup" aria-label="Theme">
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
const RADIUS: Record<Direction["id"], string> = { horizon: "rounded-[8px]", aurora: "rounded-[8px]", dune: "rounded-[8px]", nebula: "rounded-[8px]", glass: "rounded-[10px]", riso: "rounded-[3px]", stitch: "rounded-[12px]" };

// The window in miniature over a look's picture, in the look's colours: for
// smoked glass the flush sidebar, the composer and the home's cards over the
// picture; for the other materials the floating sidebar and the room's
// sheet, made of that material. Drawn in the swatches, since the look is not
// the one on screen.
function MiniWindow({ material, colours, light }: { material: string; colours: readonly [string, string, string, string]; light: boolean }) {
  const [bg, side, text, accent] = colours;
  const line = (w: string, o = 0.5) => <span className="block h-[3px] rounded-full" style={{ width: w, background: text, opacity: o }} />;
  if (material === "smoke") {
    const card = { background: `color-mix(in srgb, ${side} 62%, transparent)`, boxShadow: `inset 0 0 0 0.5px color-mix(in srgb, ${text} 16%, transparent)`, backdropFilter: "blur(3px)" };
    return (
      <span aria-hidden className="mini-window absolute inset-0 flex">
        <span className="flex h-full w-[19%] shrink-0 flex-col gap-[5px] px-[5%] pt-[9%]" style={{ background: side, boxShadow: `inset -0.5px 0 0 color-mix(in srgb, ${text} 14%, transparent)` }}>
          {line("80%", 0.55)}
          {line("60%", 0.35)}
          {line("70%", 0.35)}
          {line("50%", 0.35)}
        </span>
        <span className="flex min-w-0 flex-1 flex-col items-center justify-center gap-[4%] px-[12%]">
          <span className="flex h-[16%] w-full flex-col justify-between rounded-[3px] p-[3%]" style={card}>
            {line("45%", 0.45)}
            <span className="ml-auto block size-[7px] rounded-full" style={{ background: text, opacity: 0.7 }} />
          </span>
          <span className="grid w-full grid-cols-4 gap-[4px]">
            <span className="col-span-2 flex h-[30px] flex-col gap-[3px] rounded-[3px] p-[4px]" style={card}>
              <span className="grid grid-cols-8 gap-[1.5px]">
                {Array.from({ length: 16 }, (_, i) => (
                  <span key={i} className="aspect-square rounded-[0.5px]" style={{ background: accent, opacity: [0.15, 0.6, 0.3, 1, 0.15, 0.45, 0.8, 0.2][(i * 5) % 8] }} />
                ))}
              </span>
            </span>
            <span className="col-span-2 flex h-[30px] flex-col gap-[4px] rounded-[3px] p-[5px]" style={card}>
              {line("70%", 0.5)}
              {line("55%", 0.35)}
              {line("62%", 0.35)}
            </span>
            <span className="flex h-[20px] items-end rounded-[3px] p-[4px]" style={card}>
              {line("60%", 0.6)}
            </span>
            <span className="col-span-2 flex h-[20px] flex-col gap-[3px] rounded-[3px] p-[4px]" style={card}>
              {line("50%", 0.45)}
              {line("35%", 0.3)}
            </span>
            <span className="flex h-[20px] items-end rounded-[3px] p-[4px]" style={card}>
              <span className="block h-[4px] w-[40%] rounded-full" style={{ background: accent }} />
            </span>
          </span>
        </span>
      </span>
    );
  }
  // The floating panels, made of the material.
  const r = material === "paper" ? "1px" : material === "felt" ? "5px" : "6px";
  const glass =
    material === "glass"
      ? {
          background: `color-mix(in srgb, ${light ? "#fff" : side} ${light ? 40 : 34}%, transparent)`,
          backdropFilter: "blur(2px) saturate(1.8)",
          boxShadow: `inset 1px 1px 0 -0.5px rgb(255 255 255 / ${light ? 0.95 : 0.5}), inset -1px -1px 0 -0.5px rgb(255 255 255 / 0.25), 0 4px 10px -4px rgb(0 0 0 / 0.5)`,
        }
      : material === "paper"
        ? { background: side, boxShadow: `inset 0 0 0 0.5px ${text}, 2px 2px 0 0 ${accent}` }
        : material === "felt"
          ? { background: side, outline: `1px dashed color-mix(in srgb, ${text} 45%, transparent)`, outlineOffset: "-3px", boxShadow: "0 3px 8px -3px rgb(0 0 0 / 0.6)" }
          : { background: side };
  return (
    <span aria-hidden className="mini-window absolute inset-0 flex gap-[3%] p-[4%]">
      <span className="flex h-full w-[24%] shrink-0 flex-col gap-[5px] px-[5%] pt-[10%]" style={{ ...glass, borderRadius: r }}>
        {line("80%", 0.6)}
        {line("60%", 0.4)}
        {line("70%", 0.4)}
      </span>
      <span className="relative flex min-w-0 flex-1 flex-col gap-[6px] px-[7%] pt-[9%]" style={{ background: bg, borderRadius: r, boxShadow: material === "paper" ? `inset 0 0 0 0.5px ${text}, 2px 2px 0 0 ${accent}` : "0 4px 12px -4px rgb(0 0 0 / 0.5)" }}>
        {line("55%", 0.6)}
        {line("80%", 0.3)}
        {line("70%", 0.3)}
        <span className="ml-auto block h-[9px] w-[34%] rounded-full" style={{ background: accent, opacity: 0.85 }} />
        <span className="absolute inset-x-[7%] bottom-[8%] flex h-[16%] items-center justify-end rounded-full px-[3%]" style={{ ...glass, borderRadius: material === "paper" ? r : "999px" }}>
          <span className="block size-[7px] rounded-full" style={{ background: accent }} />
        </span>
      </span>
    </span>
  );
}

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
        <MiniWindow material={d.look.material} colours={(mode === "light" ? t.light : undefined) ?? t.swatch} light={mode === "light"} />
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
