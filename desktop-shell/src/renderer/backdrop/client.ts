// The page's side of the backdrop: decodes pictures and hands renders to
// the worker, or renders here where there is no worker.

import { type Job, renderBitmap } from "./render";
import { CUSTOM, pictureInfo } from "./pictures";
import { loadWallpaper } from "./wallpaper";
import type { Reply, Request } from "./worker";

// The longest edge a picture is decoded at: past the screen's own size
// nothing shows.
const MAX_EDGE = 2400;

async function decode(src: Blob | HTMLImageElement): Promise<ImageBitmap> {
  const probe = await createImageBitmap(src);
  const k = Math.min(1, MAX_EDGE / Math.max(probe.width, probe.height));
  if (k === 1) return probe;
  const w = Math.round(probe.width * k);
  const h = Math.round(probe.height * k);
  probe.close();
  return createImageBitmap(src, { resizeWidth: w, resizeHeight: h, resizeQuality: "high" });
}

const pictures = new Map<string, Promise<ImageBitmap | null>>();

// A picture by id, decoded once. The custom one is read again for each
// version of it (wallpaper.ts).
export function loadPicture(id: string, version = 0): Promise<ImageBitmap | null> {
  const key = id === CUSTOM ? `${CUSTOM}:${version}` : id;
  let p = pictures.get(key);
  if (p === undefined) {
    p = (async () => {
      if (id === CUSTOM) {
        const blob = await loadWallpaper();
        return blob === null ? null : decode(blob);
      }
      const info = pictureInfo(id);
      if (info === undefined) return null;
      const img = new Image();
      img.src = info.url;
      await img.decode();
      return decode(img);
    })().catch(() => null);
    pictures.set(key, p);
  }
  return p;
}

let worker: Worker | null | undefined;
let seq = 0;
const waiting = new Map<number, { resolve: (r: { bitmap: ImageBitmap; ms: number }) => void; reject: (e: Error) => void }>();

function getWorker(): Worker | null {
  if (worker !== undefined) return worker;
  if (typeof Worker === "undefined" || typeof OffscreenCanvas === "undefined") return (worker = null);
  try {
    worker = new Worker(new URL("./worker.ts", import.meta.url), { type: "module" });
  } catch {
    return (worker = null);
  }
  worker.onmessage = (e: MessageEvent<Reply>) => {
    const w = waiting.get(e.data.seq);
    if (w === undefined) return;
    waiting.delete(e.data.seq);
    if ("error" in e.data) w.reject(new Error(e.data.error));
    else w.resolve({ bitmap: e.data.bitmap, ms: e.data.ms });
  };
  // A worker that cannot start (a policy, a missing file): the renders
  // waiting on it run here instead, and so does every one after.
  worker.onerror = () => {
    worker?.terminate();
    worker = null;
    for (const [, w] of waiting) w.reject(new Error("worker failed"));
    waiting.clear();
  };
  return worker;
}

// Renders a backdrop. The bitmap belongs to the caller, who closes it.
export async function render(job: Job, img: ImageBitmap | null): Promise<{ bitmap: ImageBitmap; ms: number }> {
  const w = getWorker();
  if (w !== null) {
    try {
      return await new Promise((resolve, reject) => {
        const n = ++seq;
        waiting.set(n, { resolve, reject });
        const req: Request = { seq: n, job, img };
        w.postMessage(req);
      });
    } catch {
      // Fall through and render here.
    }
  }
  return renderBitmap(job, img);
}
