// The backdrop's worker: renders off the page's thread, so a resize or a
// theme change never stalls the window.

import { type Job, renderBitmap } from "./render";

export interface Request {
  seq: number;
  job: Job;
  img: ImageBitmap | null;
}

export type Reply = { seq: number; bitmap: ImageBitmap; ms: number } | { seq: number; error: string };

const scope = self as unknown as {
  onmessage: ((e: MessageEvent<Request>) => void) | null;
  postMessage(m: Reply, transfer?: Transferable[]): void;
};

scope.onmessage = (e) => {
  const { seq, job, img } = e.data;
  try {
    const { bitmap, ms } = renderBitmap(job, img);
    scope.postMessage({ seq, bitmap, ms }, [bitmap]);
  } catch (err) {
    scope.postMessage({ seq, error: String(err) });
  }
};
