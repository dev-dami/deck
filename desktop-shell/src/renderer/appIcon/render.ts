// Draws an icon at the sizes main wants and encodes each as a PNG, for
// setAppIcon. Runs in the window, on OffscreenCanvas.

import type { AppIconImage } from "../../shared/bridge";
import type { AppIconInfo } from "./catalog";
import { drawAppIcon } from "./draw";
import { iconPlatform, iconSizes, type HostOs } from "./platform";

export async function renderAppIcon(icon: AppIconInfo, os: HostOs): Promise<AppIconImage[]> {
  const out: AppIconImage[] = [];
  for (const size of iconSizes(os)) {
    const canvas = new OffscreenCanvas(size, size);
    const ctx = canvas.getContext("2d");
    if (ctx === null) throw new Error("no 2d context");
    drawAppIcon(ctx, icon, size, iconPlatform(os));
    const blob = await canvas.convertToBlob({ type: "image/png" });
    out.push({ size, png: new Uint8Array(await blob.arrayBuffer()) });
  }
  return out;
}
