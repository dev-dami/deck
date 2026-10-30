import { expect, test } from "vitest";
import { sourceDownload, sourceZip } from "./artifactExport";

test("single-file downloads preserve Unicode source exactly", async () => {
  const result = sourceDownload({ kind: "markdown" } as never, { files: [{ path: "notes.md", content: "# Résumé\n🙂" }] } as never);
  expect(result.name).toBe("notes.md");
  expect(await result.blob.text()).toBe("# Résumé\n🙂");
});

test("source exports reject traversal and duplicate file names", () => {
  expect(() => sourceZip([{ path: "../escape.tsx", content: "x" }])).toThrow("path");
  expect(() => sourceZip([{ path: "App.tsx", content: "a" }, { path: "App.tsx", content: "b" }])).toThrow("path");
});
