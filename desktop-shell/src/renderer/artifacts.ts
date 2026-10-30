import { clipBytes, type ArtifactConflict, type ArtifactDiagnostic, type ArtifactFile, type ArtifactKind, type ArtifactRevisionInfo } from "../shared/artifacts";

// What the Artifacts panel decides apart from drawing: which kinds main
// shows in its isolated view, and the words for what went wrong.

// Markdown renders in the panel itself; the rest need the isolated view.
export function isolated(kind: ArtifactKind): kind is "html" | "svg" | "react" {
  return kind !== "markdown";
}

export function entryText(files: readonly ArtifactFile[], entrypoint: string): string {
  return files.find((f) => f.path === entrypoint)?.content ?? "";
}

export function conflictText(c: ArtifactConflict): string {
  return c.reason === "stale_revision"
    ? `An agent saved revision ${c.currentRevision} while you looked at ${c.baseRevision}. Open the latest and try again.`
    : "That artifact was already created with different content.";
}

// A page error main saw, as a diagnostic the engine can keep for the agents.
export function runtimeDiagnostic(message: string): ArtifactDiagnostic {
  return { file: "", line: 0, column: 0, message: clipBytes(message, 512) };
}

// Most runtime errors a revision reports: a page that loops on an error
// would otherwise write to the engine without end.
export const MAX_RUNTIME_REPORTS = 5;

export function revisionLabel(r: ArtifactRevisionInfo, current: number): string {
  const by = r.agent === "" ? "you" : r.agent;
  return `r${r.revision}${r.revision === current ? " (latest)" : ""} · ${by}${r.restored_from_revision > 0 ? ` · restored r${r.restored_from_revision}` : ""}`;
}

export function fileName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}
