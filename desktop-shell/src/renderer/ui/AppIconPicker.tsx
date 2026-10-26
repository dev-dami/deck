import { useEffect, useId, useRef, useSyncExternalStore } from "react";
import { cn } from "../lib/cn";
import { APP_ICONS, appIconInfo, iconForTheme } from "../appIcon/catalog";
import { drawAppIcon } from "../appIcon/draw";
import { currentOs, ICON_NOTE, type HostOs, iconPlatform } from "../appIcon/platform";
import { themeOnPage } from "../appIcon/sync";
import { setPrefs, usePrefs } from "../prefs";

// The app icon picker for Settings > Appearance: "Match theme" (the icon of
// the window's theme, which follows it) and then every icon by hand, each
// drawn as the system will show it, and a line on where the icon shows.
// The choice is prefs.appIcon; appIcon/sync.ts draws it and hands it to main.
export interface AppIconPickerProps {
  className?: string;
  // The system the previews and the note are for; the window's by default.
  os?: HostOs;
}

export function AppIconPicker({ className, os = currentOs() }: AppIconPickerProps) {
  const { appIcon } = usePrefs();
  const themeIcon = useThemeIcon();
  const platform = iconPlatform(os);
  const noteId = useId();
  const matchId = useId();
  const picked = appIcon === "theme" || !APP_ICONS.some((i) => i.id === appIcon) ? "theme" : appIcon;
  const card = (on: boolean) =>
    cn(
      "flex cursor-pointer flex-col items-center gap-1.5 rounded-lg border border-hairline p-2 text-center text-xs outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring",
      on && "border-foreground/40 bg-selected",
    );
  return (
    <div className={cn("space-y-2 p-3", className)}>
      <div className="grid grid-cols-3 gap-2 sm:grid-cols-6" role="radiogroup" aria-label="App icon" aria-describedby={noteId}>
        <button
          type="button"
          role="radio"
          aria-checked={picked === "theme"}
          aria-label="Match theme"
          aria-describedby={matchId}
          onClick={() => setPrefs({ appIcon: "theme" })}
          className={card(picked === "theme")}
          data-icon="theme"
        >
          <IconPreview id={themeIcon} platform={platform} />
          <span className="font-medium">Match theme</span>
          <span id={matchId} className="-mt-1 text-2xs text-muted-foreground">
            {appIconInfo(themeIcon).label}
          </span>
        </button>
        {APP_ICONS.map((i) => (
          <button
            key={i.id}
            type="button"
            role="radio"
            aria-checked={picked === i.id}
            onClick={() => setPrefs({ appIcon: i.id })}
            className={card(picked === i.id)}
            data-icon={i.id}
          >
            <IconPreview id={i.id} platform={platform} />
            <span className="font-medium">{i.label}</span>
          </button>
        ))}
      </div>
      <p id={noteId} className="px-1 text-muted-foreground text-xs leading-relaxed">
        {ICON_NOTE[os]}
      </p>
    </div>
  );
}

const PREVIEW = 44;

// One icon, drawn at the screen's pixel density.
function IconPreview({ id, platform }: { id: string; platform: "mac" | "other" }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const dpr = typeof window === "undefined" ? 1 : Math.min(3, Math.max(1, Math.round(window.devicePixelRatio || 1)));
  const px = PREVIEW * dpr;
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    if (ctx === null || ctx === undefined) return;
    drawAppIcon(ctx, appIconInfo(id), px, platform);
  }, [id, px, platform]);
  return <canvas ref={ref} aria-hidden width={px} height={px} style={{ width: PREVIEW, height: PREVIEW }} />;
}

// The icon of the window's theme, following <html data-palette data-mode>.
function subscribeTheme(fn: () => void): () => void {
  const watch = new MutationObserver(fn);
  watch.observe(document.documentElement, { attributes: true, attributeFilter: ["data-palette", "data-mode"] });
  return () => watch.disconnect();
}

function themeIconNow(): string {
  const t = themeOnPage();
  return t === null ? "classic" : iconForTheme(t.palette, t.mode);
}

function useThemeIcon(): string {
  return useSyncExternalStore(subscribeTheme, themeIconNow);
}
