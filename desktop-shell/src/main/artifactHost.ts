import type { ArtifactShowInput, ArtifactShowResult } from "../shared/bridge";
import type { ArtifactPane } from "./artifact";
import type { BuildResult } from "./artifactBuild";
import type { ArtifactHost } from "./host";

// Joins the checked host methods to the pane: a React revision is built
// first, and only a build that worked is shown.
export function artifactHost(
  pane: Pick<ArtifactPane, "show" | "bounds" | "hide" | "close">,
  build: (entrypoint: string, files: ArtifactShowInput["files"], signal?: AbortSignal) => Promise<BuildResult>,
): ArtifactHost {
  // A later show replaces an earlier one that is still building.
  let latest = 0;
  let building: AbortController | null = null;
  return {
    async show(input: ArtifactShowInput): Promise<ArtifactShowResult> {
      const mine = ++latest;
      building?.abort();
      building = null;
      const files = new Map(input.files.map((f) => [f.path, f.content]));
      if (input.kind !== "react") {
        if (!files.has(input.entrypoint)) {
          return { ok: false, diagnostics: [{ file: "", line: 0, column: 0, message: `The entry point "${input.entrypoint}" is not among the files.` }] };
        }
        pane.show({ kind: input.kind, entrypoint: input.entrypoint, files }, input.request);
        return { ok: true };
      }
      const controller = new AbortController();
      building = controller;
      const built = await build(input.entrypoint, input.files, controller.signal);
      if (building === controller) building = null;
      // Superseded: an empty list tells the caller to drop this answer.
      if (mine !== latest) return { ok: false, diagnostics: [] };
      if (!built.ok) return { ok: false, diagnostics: built.diagnostics };
      pane.show({ kind: "react", entrypoint: input.entrypoint, files, built: { js: built.js, css: built.css } }, input.request);
      return { ok: true };
    },
    bounds: (b) => pane.bounds(b),
    hide: () => pane.hide(),
    close: () => {
      latest++;
      building?.abort();
      building = null;
      pane.close();
    },
  };
}
