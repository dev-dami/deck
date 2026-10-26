import { type CSSProperties, type ReactNode, useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { type PlateArt, paintPlate, plateFit } from "../../plates";

type Rgba = [number, number, number, number];

// A plate's bits, loaded once per page: 1 where the picture is light.
const loaded = new Map<string, Promise<Uint8Array>>();
function bitsOf(art: PlateArt): Promise<Uint8Array> {
  let p = loaded.get(art.src);
  if (p === undefined) {
    p = (async () => {
      const img = new Image();
      img.src = art.src;
      await img.decode();
      const c = document.createElement("canvas");
      c.width = art.w;
      c.height = art.h;
      const ctx = c.getContext("2d", { willReadFrequently: true });
      if (ctx === null) throw new Error("no 2d context");
      ctx.drawImage(img, 0, 0);
      const px = ctx.getImageData(0, 0, art.w, art.h).data;
      const bits = new Uint8Array(art.w * art.h);
      for (let i = 0; i < bits.length; i++) bits[i] = (px[i * 4] ?? 0) > 127 ? 1 : 0;
      return bits;
    })();
    p.catch(() => loaded.delete(art.src));
    loaded.set(art.src, p);
  }
  return p;
}

// Any CSS colour as RGBA bytes, by painting it.
let probe: CanvasRenderingContext2D | null = null;
function rgba(css: string): Rgba {
  probe ??= document.createElement("canvas").getContext("2d", { willReadFrequently: true });
  if (probe === null) return [0, 0, 0, 0];
  probe.clearRect(0, 0, 1, 1);
  probe.fillStyle = "#000";
  probe.fillStyle = css;
  probe.fillRect(0, 0, 1, 1);
  const d = probe.getImageData(0, 0, 1, 1).data;
  return [d[0] ?? 0, d[1] ?? 0, d[2] ?? 0, d[3] ?? 0];
}

// Bumps when the theme may have changed the plate's colours.
function useThemeTick(): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const bump = () => setTick((t) => t + 1);
    const mo = new MutationObserver(bump);
    mo.observe(document.documentElement, { attributes: true });
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    mq.addEventListener("change", bump);
    return () => {
      mo.disconnect();
      mq.removeEventListener("change", bump);
    };
  }, []);
  return tick;
}

// A dithered picture that covers its box: the plate's cells at whole
// pixels (2px at the least), cropped around the focus, in two tones. The
// tones are --plate-lo and --plate-hi on the box (styles.css, "Pages"), so
// a theme can recolour every plate. Decorative: the page sets its credit.
export function Plate({
  art,
  focus = [0.5, 0.5],
  min = 2,
  className,
  style,
}: {
  art: PlateArt;
  focus?: readonly [number, number];
  min?: number;
  className?: string;
  style?: CSSProperties;
}) {
  const box = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const lo = useRef<HTMLSpanElement>(null);
  const hi = useRef<HTMLSpanElement>(null);
  const [size, setSize] = useState<[number, number] | null>(null);
  const [bits, setBits] = useState<Uint8Array | null>(null);
  const [ready, setReady] = useState(false);
  const tick = useThemeTick();

  useEffect(() => {
    let live = true;
    bitsOf(art).then(
      (b) => live && setBits(b),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [art]);

  useEffect(() => {
    const el = box.current;
    if (el === null) return;
    const ro = new ResizeObserver(([e]) => {
      if (e === undefined) return;
      const w = Math.round(e.contentRect.width);
      const h = Math.round(e.contentRect.height);
      setSize((s) => (s !== null && s[0] === w && s[1] === h ? s : [w, h]));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const [fx, fy] = focus;
  useEffect(() => {
    const c = canvas.current;
    if (c === null || bits === null || size === null || lo.current === null || hi.current === null) return;
    const fit = plateFit(size[0], size[1], art.w, art.h, min, [fx, fy]);
    const px = paintPlate(bits, art.w, fit, rgba(getComputedStyle(lo.current).color), rgba(getComputedStyle(hi.current).color));
    c.width = fit.cols;
    c.height = fit.rows;
    c.style.width = `${fit.cols * fit.cell}px`;
    c.style.height = `${fit.rows * fit.cell}px`;
    c.getContext("2d")?.putImageData(new ImageData(px, fit.cols, fit.rows), 0, 0);
    setReady(true);
  }, [bits, size, art, min, fx, fy, tick]);

  return (
    <div ref={box} aria-hidden className={cn("plate relative overflow-hidden", className)} data-plate={art.name} style={style}>
      <span ref={lo} className="hidden text-(--plate-lo)" />
      <span ref={hi} className="hidden text-(--plate-hi)" />
      <canvas
        ref={canvas}
        className={cn(
          "absolute top-0 left-0 opacity-0 transition-opacity duration-240 ease-(--ease) [image-rendering:pixelated]",
          ready && "opacity-100",
        )}
      />
    </div>
  );
}

// An empty page as a print and its caption: a small plate on the left and,
// beside it, what shows here, how it gets here, the one thing to do next,
// and the picture's credit. In place of a blank panel or a centred icon.
export function EmptyPlate({
  art,
  focus,
  title,
  hint,
  action,
  plateClass = "h-[186px] w-[280px]",
  className,
}: {
  art: PlateArt;
  focus?: readonly [number, number];
  title: string;
  hint: ReactNode;
  action?: ReactNode;
  // The plate's size: whole 2px cells of the plate, no larger than it.
  plateClass?: string;
  className?: string;
}) {
  return (
    <figure className={cn("empty mx-auto flex w-full max-w-xl animate-enter flex-col gap-5 px-6 sm:flex-row sm:items-end sm:gap-7", className)}>
      <Plate art={art} {...(focus === undefined ? {} : { focus })} className={cn("shrink-0 rounded-[10px]", plateClass)} />
      <div className="flex min-w-0 flex-col items-start gap-1 pb-0.5">
        <p className="text-balance font-medium text-[15px] text-foreground">{title}</p>
        <p className="text-pretty text-muted-foreground text-xs leading-relaxed">{hint}</p>
        {Boolean(action) && <div className="mt-3">{action}</div>}
        <figcaption className="mt-5 text-pretty text-3xs text-faint/80 tracking-wide">{art.credit}</figcaption>
      </div>
    </figure>
  );
}
