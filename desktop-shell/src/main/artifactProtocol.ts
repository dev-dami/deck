import { randomBytes } from "node:crypto";
import { type ArtifactKind, mediaTypeOf } from "../shared/artifacts";

// What the artifact view may load: the files of one revision, over the
// deck-artifact scheme, and nothing else. Each shown revision gets a random
// host name, so a page can only ask for the revision it was opened with.

export const ARTIFACT_SCHEME = "deck-artifact";

// No network: scripts and styles run inline or from the revision's own
// files, and nothing in the page can open a frame, post a form or retarget
// relative URLs.
export const ARTIFACT_CSP = [
  "default-src 'none'",
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self' data:",
  "form-action 'none'",
  "base-uri 'none'",
  "frame-src 'none'",
  "frame-ancestors 'none'",
].join("; ");

export const BUILT_SCRIPT = "__deck-app.js";
export const BUILT_STYLE = "__deck-app.css";

export interface Served {
  kind: Exclude<ArtifactKind, "markdown">;
  entrypoint: string;
  files: ReadonlyMap<string, string>;
  // The output of a React build.
  built?: { js: string; css: string };
}

export interface ArtifactResponse {
  status: number;
  type: string;
  body: string;
}

function shell(entry: Served): string {
  const css = entry.built?.css === "" ? "" : `<link rel="stylesheet" href="/${BUILT_STYLE}">`;
  return `<!doctype html><html><head><meta charset="utf-8">${css}</head><body><div id="root"></div><script src="/${BUILT_SCRIPT}"></script></body></html>`;
}

const GONE: ArtifactResponse = { status: 404, type: "text/plain; charset=utf-8", body: "not found" };

// The content of one URL, or a 404 for anything the revision does not hold.
export function respond(entry: Served | undefined, pathname: string): ArtifactResponse {
  if (entry === undefined) return GONE;
  let rel: string;
  try {
    rel = decodeURIComponent(pathname).replace(/^\/+/, "");
  } catch {
    return GONE;
  }
  if (rel.includes("\\") || rel.split("/").some((s) => s === ".." || s === ".")) return GONE;
  if (entry.kind === "react") {
    if (rel === "" || rel === "index.html") return { status: 200, type: "text/html; charset=utf-8", body: shell(entry) };
    if (entry.built === undefined) return GONE;
    if (rel === BUILT_SCRIPT) return { status: 200, type: "text/javascript; charset=utf-8", body: entry.built.js };
    if (rel === BUILT_STYLE) return { status: 200, type: "text/css; charset=utf-8", body: entry.built.css };
    return GONE;
  }
  const path = rel === "" ? entry.entrypoint : rel;
  const content = entry.files.get(path);
  const type = mediaTypeOf(path);
  if (content === undefined || type === null) return GONE;
  return { status: 200, type, body: content };
}

export class ArtifactHosts {
  private readonly shown = new Map<string, Served>();

  // A new host name for this content; the page's URL is `urlOf(host)`.
  add(entry: Served): string {
    const host = randomBytes(16).toString("hex");
    this.shown.set(host, entry);
    return host;
  }

  get(host: string): Served | undefined {
    return this.shown.get(host);
  }

  remove(host: string): void {
    this.shown.delete(host);
  }

  clear(): void {
    this.shown.clear();
  }

  handle(raw: string): Response {
    let u: URL;
    try {
      u = new URL(raw);
    } catch {
      return toResponse(GONE);
    }
    if (u.protocol !== `${ARTIFACT_SCHEME}:`) return toResponse(GONE);
    return toResponse(respond(this.shown.get(u.hostname), u.pathname));
  }
}

export function urlOf(host: string): string {
  return `${ARTIFACT_SCHEME}://${host}/`;
}

function toResponse(r: ArtifactResponse): Response {
  return new Response(r.body, {
    status: r.status,
    headers: {
      "content-type": r.type,
      "content-security-policy": ARTIFACT_CSP,
      "x-content-type-options": "nosniff",
      "cross-origin-resource-policy": "same-origin",
      "cache-control": "no-store",
    },
  });
}
