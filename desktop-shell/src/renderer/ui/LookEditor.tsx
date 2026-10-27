import { type ReactNode, useMemo, useRef } from "react";
import { type Art, artFor, artProcess, type Tokens } from "../backdrop/inks";
import { type Look, readTokens, useLook } from "../backdrop/live";
import { useWallpaperVersion } from "../backdrop/wallpaper";
import { cn } from "../lib/cn";
import { type Prefs, setPrefs } from "../prefs";
import {
  type Aspects,
  type Choice,
  CORNERS,
  FACES,
  MATERIALS,
  type PictureProcess,
  PROCESSES,
  TEXTURES,
  THEMES,
  type ThemeInfo,
  TRANSLUCENCIES,
} from "../themes";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { small, ThemeGrid, useBoxSize, useMiniPrint } from "./ThemeCard";

// Settings > Appearance's look: a live preview of the window, the three
// presets, then each aspect on a row of its own (themes.ts), drawn as
// what it is: the colours as their swatches, each material and texture as
// a swatch made of it (a .look in styles.css), each process as a print of
// the picture through it, each face in itself. Any aspect goes with any
// other; changing one makes the look Custom.
export function LookEditor({ p }: { p: Prefs }) {
  const look = useLook();
  const tokens = useMemo(() => readTokens(), [look]);
  const pick = (a: Partial<Aspects>) => setPrefs(a);
  return (
    <div className="flex flex-col gap-5 py-4">
      <LookPreview look={look} tokens={tokens} sidebar={p.sidebarSide} wallpaper={p.wallpaper} />
      <ThemeGrid look={p} />
      <div className="flex flex-col">
        <h3 className="pb-1 text-[13px]">Mix your own</h3>
        <Aspect title="Colours" hint={THEMES.find((t) => t.id === p.palette)?.label}>
          <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Colours">
            {THEMES.map((t) => (
              <ColourChip key={t.id} t={t} mode={look.mode} on={p.palette === t.id} onClick={() => pick({ palette: t.id })} />
            ))}
          </div>
        </Aspect>
        <Aspect title="Material" hint={line(MATERIALS, p.material)}>
          <Tiles label="Material" choices={MATERIALS} value={p.material} onChange={(material) => pick({ material })}>
            {(id) => <MaterialSwatch material={id} texture={p.texture} mode={look.mode} />}
          </Tiles>
        </Aspect>
        <Aspect title="Picture" hint={line(PROCESSES, p.process)}>
          <Tiles label="Picture process" choices={PROCESSES} value={p.process} onChange={(process) => pick({ process })}>
            {(id) => <ProcessSwatch process={id} look={look} tokens={tokens} />}
          </Tiles>
        </Aspect>
        <Aspect title="Display face" hint={line(FACES, p.face)}>
          <Tiles label="Display face" choices={FACES} value={p.face} onChange={(face) => pick({ face })}>
            {(id) => (
              <span className="grid size-full place-items-center bg-surface-1 text-[22px] text-foreground leading-none" style={{ fontFamily: FACES.find((f) => f.id === id)?.family }}>
                Aa
              </span>
            )}
          </Tiles>
        </Aspect>
        <Aspect title="Texture" hint={line(TEXTURES, p.texture)}>
          <Tiles label="Texture" choices={TEXTURES} value={p.texture} onChange={(texture) => pick({ texture })} cols={3}>
            {(id) => (
              <span className="look block size-full" data-texture={id} data-mode={look.mode}>
                <span className="block size-full bg-surface-1 [background-image:var(--material-texture)]" />
              </span>
            )}
          </Tiles>
        </Aspect>
        <Aspect title="Corners" hint={line(CORNERS, p.corners)}>
          <Knob label="Corners" choices={CORNERS} value={p.corners} onChange={(corners) => pick({ corners })} />
        </Aspect>
        <Aspect
          title="Translucency"
          hint={p.material === "glass" ? line(TRANSLUCENCIES, p.translucency) : "Liquid glass only: the other materials are opaque"}
        >
          <Knob
            label="Translucency"
            choices={TRANSLUCENCIES}
            value={p.translucency}
            onChange={(translucency) => pick({ translucency })}
            disabled={p.material !== "glass"}
          />
        </Aspect>
      </div>
    </div>
  );
}

function line<T extends string>(choices: readonly Choice<T>[], id: T): string | undefined {
  return choices.find((c) => c.id === id)?.line;
}

// One aspect: its name and the chosen one's line on the left, the choices
// on the right; stacked in a narrow column.
function Aspect({ title, hint, children }: { title: string; hint?: string | undefined; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 border-hairline border-t py-3 @md:flex-row @md:gap-4">
      <div className="min-w-0 @md:w-32 @md:shrink-0 @md:pt-1">
        <h4 className="text-xs">{title}</h4>
        {hint !== undefined && <p className="text-pretty text-2xs text-faint leading-snug">{hint}</p>}
      </div>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}

// A row of choices drawn as swatches, each with its name under it.
function Tiles<T extends string>({
  label,
  choices,
  value,
  onChange,
  children,
  cols = 4,
}: {
  label: string;
  choices: readonly Choice<T>[];
  value: T;
  onChange: (v: T) => void;
  children: (id: T) => ReactNode;
  cols?: 3 | 4;
}) {
  return (
    <div className={cn("grid gap-2", cols === 4 ? "grid-cols-4" : "grid-cols-3")} role="radiogroup" aria-label={label}>
      {choices.map((c) => {
        const on = value === c.id;
        return (
          <button
            key={c.id}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={c.label}
            title={`${c.label}: ${c.line}`}
            onClick={() => onChange(c.id)}
            className="group flex min-w-0 cursor-pointer flex-col gap-1 text-left outline-none"
          >
            <span
              className={cn(
                "relative block h-12 w-full overflow-hidden rounded-lg ring-1 ring-hairline transition-[box-shadow,transform] duration-200 group-hover:-translate-y-px group-focus-visible:ring-2 group-focus-visible:ring-ring",
                on && "ring-2 ring-primary ring-offset-2 ring-offset-background",
              )}
            >
              {children(c.id)}
            </span>
            <span className={cn("truncate text-2xs", on ? "font-medium text-foreground" : "text-muted-foreground")}>{c.label}</span>
          </button>
        );
      })}
    </div>
  );
}

// A knob: a segmented control of a few steps.
function Knob<T extends string>({
  label,
  choices,
  value,
  onChange,
  disabled = false,
}: {
  label: string;
  choices: readonly Choice<T>[];
  value: T;
  onChange: (v: T) => void;
  disabled?: boolean;
}) {
  return (
    <div className={cn(segmentTrack(), "inline-grid auto-cols-fr grid-flow-col", disabled && "opacity-50")} role="radiogroup" aria-label={label} aria-disabled={disabled}>
      {choices.map((c) => (
        <button
          key={c.id}
          type="button"
          role="radio"
          aria-checked={value === c.id}
          disabled={disabled}
          title={c.line}
          onClick={() => onChange(c.id)}
          className={cn(segmentItem({ on: value === c.id }), disabled && "cursor-default")}
        >
          {c.label}
        </button>
      ))}
    </div>
  );
}

// A theme's colours as a chip: its ground with its accent on it, the
// light half beside the dark for a theme that follows the mode, and its
// name.
function ColourChip({ t, mode, on, onClick }: { t: ThemeInfo; mode: "light" | "dark"; on: boolean; onClick: () => void }) {
  const [bg, , text, accent] = (mode === "light" ? t.light : undefined) ?? t.swatch;
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      aria-label={t.label}
      onClick={onClick}
      className={cn(
        "flex h-7 cursor-pointer items-center gap-1.5 rounded-full py-0.5 pr-2.5 pl-1 text-xs outline-none ring-1 ring-hairline transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring",
        on ? "bg-selected font-medium text-foreground ring-2 ring-primary" : "text-muted-foreground",
      )}
    >
      <span aria-hidden className="relative grid size-5 shrink-0 place-items-center overflow-hidden rounded-full ring-1 ring-black/15" style={{ background: bg }}>
        <span className="size-2 rounded-full" style={{ background: accent, boxShadow: `0 0 0 1.5px ${text}22` }} />
      </span>
      {t.label}
    </button>
  );
}

// A material as a swatch made of it: a panel of it over stripes of the
// accent, so a glass shows its blur, paper its rule and its shadow, felt
// its seam.
function MaterialSwatch({ material, texture, mode }: { material: string; texture: string; mode: string }) {
  return (
    <span
      className="look relative block size-full"
      data-material={material}
      data-texture={texture}
      data-mode={mode}
      style={{
        background:
          "repeating-linear-gradient(135deg, color-mix(in srgb, var(--primary) 45%, transparent) 0 5px, transparent 5px 11px), var(--backdrop)",
      }}
    >
      <span className="liquid absolute inset-y-2 right-2 left-[30%] rounded-[calc(var(--panel-radius)*0.6)]" />
    </span>
  );
}

// The art of a process for a small box, in the theme on screen: the
// window's own, without the band held back for the sidebar or the margins
// round it.
function forBox(art: Art): Art {
  if (art.process === "scene") return { ...art, side: 0, margin: 0 };
  if (art.process === "halftone") return { ...art, margin: 4 };
  return { ...art, clear: 0 };
}

function artOf(look: Look, tokens: Tokens, process: PictureProcess): Art | null {
  if (process === "none") return null;
  return forBox(artFor(look.palette, look.mode, tokens, "window", artProcess(process)));
}

// A process as a print of the theme's picture through it; "none" is the
// plain ground the window keeps.
function ProcessSwatch({ process, look, tokens }: { process: PictureProcess; look: Look; tokens: Tokens }) {
  const box = useRef<HTMLSpanElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const size = useBoxSize(box);
  const art = artOf(look, tokens, process);
  const drawn = useMiniPrint(canvas, size, art === null ? null : small(art));
  return (
    <span ref={box} className="relative block size-full bg-(--backdrop)">
      {art === null ? (
        <span aria-hidden className="absolute inset-0 grid place-items-center text-2xs text-faint">
          Plain
        </span>
      ) : (
        <canvas ref={canvas} aria-hidden className={cn("absolute inset-0 size-full opacity-0 transition-opacity duration-200", drawn && "opacity-100")} />
      )}
    </span>
  );
}

// The window in miniature, live: the picture (or the person's) through
// the process, the sidebar of the material on the side the layout puts
// it, and the room's sheet with a heading in the display face, the
// person's bubble and the composer. Every token is the window's own.
function LookPreview({ look, tokens, sidebar, wallpaper }: { look: Look; tokens: Tokens; sidebar: "left" | "right"; wallpaper: string }) {
  const box = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const size = useBoxSize(box);
  const version = useWallpaperVersion();
  const art = artOf(look, tokens, look.process);
  const drawn = useMiniPrint(canvas, size, art, wallpaper, version);
  return (
    <div
      ref={box}
      aria-label="Preview of the look"
      role="img"
      className="look-preview relative aspect-[16/7] w-full overflow-hidden rounded-xl ring-1 ring-hairline"
      style={{
        background:
          "radial-gradient(60% 80% at 0% 0%, var(--ambient-1), transparent 70%), radial-gradient(50% 70% at 100% 100%, var(--ambient-2), transparent 70%), var(--backdrop)",
      }}
    >
      {art !== null && (
        <canvas ref={canvas} aria-hidden className={cn("absolute inset-0 size-full opacity-0 transition-opacity duration-240", drawn && "opacity-100")} />
      )}
      <div className={cn("absolute inset-0 flex gap-2 p-2", sidebar === "right" && "flex-row-reverse")}>
        <div className="liquid flex w-[27%] flex-col gap-1.5 rounded-(--panel-radius) p-2.5 pt-3">
          <span className="pb-1 font-semibold text-foreground text-xs">Deck</span>
          <span className="h-5 rounded-(--control-radius) bg-selected" />
          <span className="h-2 w-4/5 rounded-full bg-hairline-strong" />
          <span className="h-2 w-3/5 rounded-full bg-hairline-strong" />
          <span className="mt-auto h-2 w-2/5 rounded-full bg-hairline" />
        </div>
        <div className="sheet flex min-w-0 flex-1 flex-col gap-2 overflow-hidden rounded-(--sheet-radius) p-3">
          <h4 className="headline truncate text-xl">
            Fix the lockout <span className="headline-quiet">in deck</span>
          </h4>
          <span className="h-2 w-11/12 rounded-full bg-hairline-strong" />
          <span className="h-2 w-3/4 rounded-full bg-hairline-strong" />
          <span className="self-end rounded-[14px_14px_4px_14px] bg-bubble px-2.5 py-1 text-[11px] text-foreground">Ship it</span>
          <div className="liquid mt-auto flex h-8 items-center rounded-full pr-1 pl-3 text-[11px] text-faint">
            Message the crew
            <span className="liquid-tint ml-auto size-6 rounded-full" />
          </div>
        </div>
      </div>
    </div>
  );
}
