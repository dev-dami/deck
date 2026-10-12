import { expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { findViolations } from "./innerhtml";

test("app sources never use innerHTML-style APIs", () => {
  expect(findViolations(join(import.meta.dir, "..", "src"))).toEqual([]);
});

test("the scanner flags what it should", () => {
  const dir = mkdtempSync(join(tmpdir(), "deck-scan-"));
  try {
    const bad = ["el.innerHTML = x;", "const y = <div dangerouslySetInnerHTML={z} />;", ""];
    writeFileSync(join(dir, "bad.tsx"), bad.join(String.fromCharCode(10)));
    writeFileSync(join(dir, "good.ts"), "const ok = textContent;");
    const hits = findViolations(dir);
    expect(hits.map((h) => h.line)).toEqual([1, 2]);
    expect(hits.every((h) => h.file.endsWith("bad.tsx"))).toBe(true);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
