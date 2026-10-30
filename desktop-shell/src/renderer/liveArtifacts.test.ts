import { describe, expect, test } from "vitest";
import type { ArtifactSummary } from "../shared/artifacts";
import { LiveStore } from "./live";
import { toArtifactPage, toArtifactSummary, toEvent } from "./rpcMap";

const art = (over: Partial<ArtifactSummary> = {}): ArtifactSummary => ({
  id: "a1",
  session_id: "s1",
  title: "Calc",
  kind: "react",
  current_revision: 1,
  created_at: 1,
  updated_at: 1,
  agent: "claude",
  source: null,
  state_version: 1,
  build_status: "",
  ...over,
});

describe("artifacts in the live store", () => {
  test("a replayed or older state changes nothing; a newer one replaces", () => {
    const s = new LiveStore();
    s.apply({ k: "artifact", artifact: art({ state_version: 3, current_revision: 2 }) });
    s.apply({ k: "artifact", artifact: art({ state_version: 2, current_revision: 1 }) });
    expect(s.get("s1").artifacts).toEqual([art({ state_version: 3, current_revision: 2 })]);
    s.apply({ k: "artifact", artifact: art({ state_version: 4, build_status: "error" }) });
    expect(s.get("s1").artifacts[0]?.build_status).toBe("error");
  });

  test("a snapshot read before a delta never overwrites it", () => {
    const s = new LiveStore();
    s.apply({ k: "artifact", artifact: art({ state_version: 5 }) });
    s.loadArtifacts("s1", [art({ state_version: 2 }), art({ id: "a2", title: "Doc", state_version: 1 })]);
    expect(s.get("s1").artifacts.map((a) => [a.id, a.state_version])).toEqual([
      ["a1", 5],
      ["a2", 1],
    ]);
  });

  test("a room that is gone takes its artifacts", () => {
    const s = new LiveStore();
    s.loadArtifacts("s1", [art()]);
    s.drop("s1");
    expect(s.get("s1").artifacts).toEqual([]);
  });
});

describe("deck.v1 artifacts", () => {
  const wire = { id: "a1", session_id: "s1", title: "Calc", kind: "react", current_revision: 2, state_version: 7, build_status: "ok" };

  test("an artifact_changed event becomes a live artifact", () => {
    const ev = toEvent({ kind: "artifact_changed", seq: 9, artifact_changed: wire });
    expect(ev).toMatchObject({ k: "live", seq: 9, live: { k: "artifact", artifact: { id: "a1", current_revision: 2, build_status: "ok" } } });
  });

  test("a kind this app does not know is dropped", () => {
    expect(toArtifactSummary({ ...wire, kind: "pdf" })).toBeNull();
    expect(toArtifactSummary({ ...wire, id: "" })).toBeNull();
    expect(toArtifactSummary({ ...wire, build_status: "weird" })?.build_status).toBe("");
  });

  test("a read page keeps the manifest and the build reports", () => {
    const page = toArtifactPage({
      revision: 3,
      path: "",
      info: { revision: 3, entrypoint: "App.tsx", agent: "codex" },
      files: [{ path: "App.tsx" }, { path: "app.css" }],
      builds: [{ phase: "build", ok: false, diagnostics: [{ file: "App.tsx", line: 2, column: 4, message: "bad" }] }, { phase: "other" }],
    });
    expect(page?.files).toEqual(["App.tsx", "app.css"]);
    expect(page?.builds).toHaveLength(1);
    expect(page?.builds[0]?.diagnostics[0]).toMatchObject({ file: "App.tsx", line: 2 });
    expect(toArtifactPage({ revision: 0 })).toBeNull();
  });
});
