// Artifacts as the desktop sees them: the shapes mirror
// proto/deck/v1/deck.proto and .deck/plans/artifacts-api-contract.md.

export type ArtifactKind = "markdown" | "html" | "svg" | "react";

export interface ArtifactFile {
  path: string;
  content: string;
}

export interface ArtifactDiagnostic {
  file: string;
  line: number;
  column: number;
  message: string;
}

export const ARTIFACT_LIMITS = {
  // What the engine keeps of a report: at most this many diagnostics of this
  // many UTF-8 bytes each, and this much serialized data for both phases.
  diagnostics: 8,
  diagnosticBytes: 512,
  reportBytes: 4096,
  // Output of one React build; the engine caps source, not what it compiles to.
  buildOutputBytes: 8 << 20,
  buildMs: 30_000,
} as const;

const ENTRY_EXTENSIONS: Record<ArtifactKind, readonly string[]> = {
  markdown: [".md"],
  html: [".html"],
  svg: [".svg"],
  react: [".tsx", ".jsx"],
};

export function entryFits(kind: ArtifactKind, entrypoint: string): boolean {
  return ENTRY_EXTENSIONS[kind].some((ext) => entrypoint.endsWith(ext));
}

const MEDIA_TYPES: Record<string, string> = {
  ".md": "text/markdown; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
};

// The media type of a served file, or null for an extension artifacts may
// not serve: the extension decides, never anything the artifact claims.
export function mediaTypeOf(path: string): string | null {
  const dot = path.lastIndexOf(".");
  if (dot < 0) return null;
  return MEDIA_TYPES[path.slice(dot).toLowerCase()] ?? null;
}

// The conflict detail the main bridge keeps from a gRPC Aborted status.
export interface ArtifactConflict {
  id: string;
  baseRevision: number;
  currentRevision: number;
  reason: "stale_revision" | "create_key_reused";
}

export interface ArtifactSource {
  message_id: string;
  task_id: string;
  turn_id: string;
}

// Metadata of an artifact; its files are read apart, one revision at a time.
export interface ArtifactSummary {
  id: string;
  session_id: string;
  title: string;
  kind: ArtifactKind;
  current_revision: number;
  created_at: number;
  updated_at: number;
  agent: string;
  source: ArtifactSource | null;
  // Counts provenance and build reports too, so it orders what updated_at cannot.
  state_version: number;
  build_status: "" | "ok" | "error";
}

export interface ArtifactRevisionInfo {
  revision: number;
  title: string;
  entrypoint: string;
  agent: string;
  created_at: number;
  note: string;
  restored_from_revision: number;
}

// The files of one pinned revision, each read whole.
export interface ArtifactRevisionFiles {
  revision: number;
  info: ArtifactRevisionInfo;
  files: ArtifactFile[];
}

// What the desktop said of a build, as the engine kept it.
export interface ArtifactBuildInfo {
  phase: "build" | "runtime";
  ok: boolean;
  runtime_version: string;
  diagnostics: ArtifactDiagnostic[];
}

const encoder = new TextEncoder();
export const utf8Bytes = (text: string): number => encoder.encode(text).length;

// The longest prefix of text that fits in max UTF-8 bytes, cut between characters.
export function clipBytes(text: string, max: number): string {
  if (utf8Bytes(text) <= max) return text;
  let out = "";
  let used = 0;
  for (const ch of text) {
    const n = utf8Bytes(ch);
    if (used + n > max - 3) break;
    out += ch;
    used += n;
  }
  return out + "…";
}

// A report the engine will take: few diagnostics, short messages, small in all.
export function limitDiagnostics(list: readonly ArtifactDiagnostic[]): ArtifactDiagnostic[] {
  const out: ArtifactDiagnostic[] = [];
  let total = 0;
  for (const d of list.slice(0, ARTIFACT_LIMITS.diagnostics)) {
    const next = { ...d, file: clipBytes(d.file, 256), message: clipBytes(d.message, ARTIFACT_LIMITS.diagnosticBytes) };
    total += utf8Bytes(JSON.stringify(next));
    // The engine retains both phases, including their report envelopes.
    // Reserve 1 KiB for those envelopes and split the rest between phases.
    if (total > (ARTIFACT_LIMITS.reportBytes - 1024) / 2) break;
    out.push(next);
  }
  return out;
}
