import { type CSSProperties, type ReactNode, useEffect, useRef, useState } from "react";
import { artFor } from "../backdrop/inks";
import { CUSTOM, type Picture, pictureInfo, SETS } from "../backdrop/pictures";
import { clearWallpaper, loadWallpaper, saveWallpaper, useWallpaperVersion } from "../backdrop/wallpaper";
import { cn } from "../lib/cn";
import { setPrefs, usePrefs } from "../prefs";
import { directionOf } from "../themes";
import { Button } from "./kit/button";

// The picture behind the window, for Settings > Appearance: the theme's
// own, the other pictures of its art direction's set, or the person's own.
// Whichever it is, the theme's process prints it (backdrop/). The
// thumbnails are the pictures as they are, so the choice is of a picture;
// the window shows the print. The person's own stays in this window's
// storage (backdrop/wallpaper.ts) and never leaves it.
export function WallpaperPicker({ className }: { className?: string }) {
  const { palette, wallpaper } = usePrefs();
  // The theme's own picture first (Tokyo Night has its own), then the rest
  // of its direction's set, then a picture from another set that was chosen
  // by hand.
  const own = artFor(palette, "dark").picture;
  const set = [own, ...SETS[directionOf(palette).id].filter((id) => id !== own)];
  const ids = wallpaper !== "theme" && wallpaper !== CUSTOM && !set.includes(wallpaper) ? [...set, wallpaper] : set;
  const picked = wallpaper === "theme" ? own : wallpaper;
  const theirs = useCustomUrl();
  const file = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const choose = async (f: File | undefined) => {
    if (f === undefined) return;
    setBusy(true);
    const err = await saveWallpaper(f);
    setBusy(false);
    setError(err);
    if (err === null) setPrefs({ wallpaper: CUSTOM });
  };

  return (
    <div className={cn("flex flex-col gap-2.5 py-3", className)}>
      <div className="space-y-0.5">
        <h3 className="text-[13px]">Picture</h3>
        <p className="text-pretty text-muted-foreground text-xs leading-relaxed">
          Behind the window and on the first screens, printed through the theme's process. The sheets you read on stay plain.
        </p>
      </div>
      <div className="grid grid-cols-3 gap-2 @md:grid-cols-6" role="radiogroup" aria-label="Picture">
        {ids.map((id) => {
          const p = pictureInfo(id);
          if (p === undefined) return null;
          return (
            <Thumb
              key={id}
              label={id === own ? `${p.credit} (the theme's own)` : p.credit}
              on={picked === id}
              onClick={() => setPrefs({ wallpaper: id === own ? "theme" : id })}
            >
              <img src={p.url} alt="" draggable={false} className="size-full object-cover" style={place(p)} />
            </Thumb>
          );
        })}
        {theirs !== null && (
          <Thumb label="Your picture" on={wallpaper === CUSTOM} onClick={() => setPrefs({ wallpaper: CUSTOM })}>
            <img src={theirs} alt="" draggable={false} className="size-full object-cover" />
          </Thumb>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <input
          ref={file}
          type="file"
          accept="image/*"
          className="hidden"
          aria-label="Your own picture"
          onChange={(e) => {
            void choose(e.target.files?.[0]);
            e.target.value = "";
          }}
        />
        <Button variant="outline" size="sm" disabled={busy} onClick={() => file.current?.click()}>
          {theirs === null ? "Use your own…" : "Replace yours…"}
        </Button>
        {theirs !== null && (
          <Button
            variant="ghost-muted"
            size="sm"
            onClick={() => {
              setError(null);
              if (wallpaper === CUSTOM) setPrefs({ wallpaper: "theme" });
              void clearWallpaper();
            }}
          >
            Remove yours
          </Button>
        )}
        <span className="ml-auto min-w-0 truncate text-2xs text-faint" aria-live="polite">
          {error ?? creditOf(picked)}
        </span>
      </div>
    </div>
  );
}

// The credit under the row: whose picture is behind the window.
function creditOf(id: string): string {
  if (id === CUSTOM) return "Your picture, kept in this window only";
  const p = pictureInfo(id);
  return p === undefined ? "" : `${p.credit}. ${p.source}`;
}

// A thumbnail shows the picture's subject, with its paper margin or mount
// cut away as the processes cut it.
function place(p: Picture): CSSProperties {
  const [t, r, b, l] = (p.crop ?? [0, 0, 0, 0]).map((v) => `${v * 100}%`);
  return { objectPosition: `${p.ax * 100}% ${p.ay * 100}%`, objectViewBox: `inset(${t} ${r} ${b} ${l})` } as CSSProperties;
}

function Thumb({ label, on, onClick, children }: { label: string; on: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      aria-label={label}
      title={label}
      onClick={onClick}
      className={cn(
        "relative block aspect-[4/3] cursor-pointer overflow-hidden rounded-lg bg-surface-1 outline-none ring-1 ring-hairline transition-[box-shadow,transform] duration-200 hover:-translate-y-px focus-visible:ring-2 focus-visible:ring-ring",
        on && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
    >
      {children}
    </button>
  );
}

// The person's picture as a blob URL while there is one.
function useCustomUrl(): string | null {
  const version = useWallpaperVersion();
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    let made: string | null = null;
    void loadWallpaper().then((b) => {
      if (!live) return;
      made = b === null ? null : URL.createObjectURL(b);
      setUrl(made);
    });
    return () => {
      live = false;
      if (made !== null) URL.revokeObjectURL(made);
    };
  }, [version]);
  return url;
}
