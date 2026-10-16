import type { QRBlob } from "../protocol3";
import { parsePairFragment } from "./pairing";

export type Route =
  | { name: "computers" }
  | { name: "inbox"; cid: string }
  | { name: "chat"; cid: string; pj: string; sessionId: string };

export function routeOf(hash: string): Route {
  const m = /^#\/c\/([A-Za-z0-9_-]+)(?:\/p\/([^/]+)\/s\/([^/]+))?$/.exec(hash);
  if (!m?.[1]) return { name: "computers" };
  try {
    if (m[2] && m[3])
      return {
        name: "chat",
        cid: m[1],
        pj: decodeURIComponent(m[2]),
        sessionId: decodeURIComponent(m[3]),
      };
  } catch {
    return { name: "computers" };
  }
  return { name: "inbox", cid: m[1] };
}

export function readPairOffer(
  hash: string,
  strip: () => void,
): { blob: QRBlob | null; error: string | null } {
  if (!hash.startsWith("#p=")) return { blob: null, error: null };
  try {
    return { blob: parsePairFragment(hash), error: null };
  } catch (err) {
    return { blob: null, error: err instanceof Error ? err.message : "Invalid pairing code." };
  } finally {
    strip();
  }
}

export function chatHash(cid: string, pj: string, sessionId: string): string {
  return `#/c/${cid}/p/${encodeURIComponent(pj)}/s/${encodeURIComponent(sessionId)}`;
}
