import { useSyncExternalStore } from "react";
import { lookFromPalette, type PictureProcess, PROCESSES, type ThemeInfo } from "../themes";
import { mix, type RGB } from "./core";
import type { Mode, Tokens } from "./inks";

// The page's side of choosing the art: which theme, mode and process
// <html> shows (appearance.ts writes them), and the theme's colours as RGB.

export interface Look {
  palette: string;
  mode: Mode;
  accent: string;
  process: PictureProcess;
}

const PROCESS_IDS: readonly string[] = PROCESSES.map((p) => p.id);

let look: Look | null = null;
function read(): Look {
  const root = document.documentElement;
  const process = root.getAttribute("data-process") ?? "";
  const next: Look = {
    palette: root.getAttribute("data-palette") ?? "glass",
    mode: root.getAttribute("data-mode") === "light" ? "light" : "dark",
    accent: root.getAttribute("data-accent") ?? "theme",
    // Before appearance.ts has written one, the theme's own.
    process: PROCESS_IDS.includes(process) ? (process as PictureProcess) : lookFromPalette(root.getAttribute("data-palette") ?? "glass").process,
  };
  if (look === null || look.palette !== next.palette || look.mode !== next.mode || look.accent !== next.accent || look.process !== next.process) look = next;
  return look;
}

function subscribe(l: () => void): () => void {
  const watch = new MutationObserver(l);
  watch.observe(document.documentElement, { attributes: true, attributeFilter: ["data-palette", "data-mode", "data-accent", "data-process"] });
  return () => watch.disconnect();
}

// The theme, mode, accent and process on screen, as <html> names them.
export function useLook(): Look {
  return useSyncExternalStore(subscribe, read);
}

// Any CSS colour (oklch, color-mix, a name) as RGB: a canvas parses it.
let probe: OffscreenCanvasRenderingContext2D | null = null;
export function parseColour(c: string): RGB {
  probe ??= new OffscreenCanvas(1, 1).getContext("2d", { willReadFrequently: true });
  if (probe === null) return [0, 0, 0];
  probe.clearRect(0, 0, 1, 1);
  probe.fillStyle = "#000";
  probe.fillStyle = c;
  probe.fillRect(0, 0, 1, 1);
  const [r = 0, g = 0, b = 0] = probe.getImageData(0, 0, 1, 1).data;
  return [r, g, b];
}

// The live theme's colours, accent included.
export function readTokens(): Tokens {
  const el = document.createElement("span");
  el.style.display = "none";
  document.body.append(el);
  const get = (v: string) => {
    el.style.color = `var(${v})`;
    return parseColour(getComputedStyle(el).color);
  };
  const t = { backdrop: get("--backdrop"), canvas: get("--canvas"), primary: get("--primary"), text: get("--text") };
  el.remove();
  return t;
}

// A theme's colours from its swatches, for a theme that is not on screen
// (the theme cards).
export function swatchTokens(theme: ThemeInfo, mode: Mode): Tokens {
  const [bg, , text, accent] = (mode === "light" ? theme.light : undefined) ?? theme.swatch;
  const canvas = parseColour(bg);
  return {
    backdrop: mode === "dark" ? mix(canvas, [0, 0, 0], 0.4) : mix(canvas, [138, 144, 160], 0.16),
    canvas,
    primary: parseColour(accent),
    text: parseColour(text),
  };
}
