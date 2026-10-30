import { describe, expect, test } from "vitest";
import { limitDiagnostics } from "../shared/artifacts";
import { conflictText, entryText, isolated, revisionLabel, runtimeDiagnostic } from "./artifacts";

describe("artifacts", () => {
  test("only Markdown renders outside the isolated view", () => {
    expect(["markdown", "html", "svg", "react"].map((k) => isolated(k as "html"))).toEqual([false, true, true, true]);
  });

  test("entryText finds the entry file, or says nothing", () => {
    expect(entryText([{ path: "a.md", content: "# hi" }], "a.md")).toBe("# hi");
    expect(entryText([], "a.md")).toBe("");
  });

  test("a stale edit names both revisions", () => {
    expect(conflictText({ id: "x", baseRevision: 2, currentRevision: 5, reason: "stale_revision" })).toContain("revision 5");
  });

  test("a runtime message is clipped by UTF-8 bytes, between characters", () => {
    expect(new TextEncoder().encode(runtimeDiagnostic("x".repeat(9000)).message).length).toBeLessThanOrEqual(512);
    const wide = runtimeDiagnostic("é".repeat(600)).message;
    expect(new TextEncoder().encode(wide).length).toBeLessThanOrEqual(512);
    expect(wide).not.toContain("�");
  });

  test("reports reserve room for both phases and their engine envelopes", () => {
    const many = Array.from({ length: 20 }, (_, i) => ({ file: "App.tsx", line: i, column: 1, message: "m".repeat(512) }));
    const kept = limitDiagnostics(many);
    expect(kept.length).toBeLessThanOrEqual(8);
    expect(kept.length).toBeGreaterThan(0);
    expect(new TextEncoder().encode(JSON.stringify(kept)).length).toBeLessThanOrEqual(1536 + kept.length + 1);
  });

  test("a revision says who made it and whether it was a restore", () => {
    const r = { revision: 3, title: "t", entrypoint: "e", agent: "codex", created_at: 0, note: "", restored_from_revision: 1 };
    expect(revisionLabel(r, 3)).toBe("r3 (latest) · codex · restored r1");
    expect(revisionLabel({ ...r, agent: "", restored_from_revision: 0 }, 5)).toBe("r3 · you");
  });
});
