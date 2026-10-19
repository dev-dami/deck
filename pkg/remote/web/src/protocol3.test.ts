import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonicalJson, prettyJson } from "./canonical";
import { FIXTURES_V3 } from "./protocol3";

// The v3 + relay fixtures are the contract shared with the Go backend
// (docs/mobile-contract.md). Noise vectors (noise_*.json) are covered by B2,
// not by guards.
const TESTDATA = join(import.meta.dir, "..", "..", "wire", "testdata");

function readFixture(name: string): string {
  return readFileSync(join(TESTDATA, `${name}.json`), "utf8").replaceAll("\r\n", "\n");
}

describe("v3 fixtures", () => {
  const registered = new Set<string>();
  for (const [name] of FIXTURES_V3) {
    test(`${name} is registered once`, () => {
      expect(registered.has(name)).toBe(false);
      registered.add(name);
    });
  }

  for (const [name, decode] of FIXTURES_V3) {
    test(`${name} decodes and round-trips byte-equal`, () => {
      const raw = readFixture(name);
      const parsed: unknown = JSON.parse(raw);
      // Canonical pretty form: sorted keys, 2-space indent, trailing newline.
      expect(raw).toBe(prettyJson(parsed));
      const decoded = decode(parsed);
      expect(decoded).not.toBeNull();
      // A field the decoder does not declare is dropped and fails here, as
      // does a value the decoder rewrote.
      expect(canonicalJson(decoded)).toBe(canonicalJson(parsed));
    });
  }

  test("every v3_/relay_ testdata file is registered", () => {
    const names = new Set(FIXTURES_V3.map(([n]) => n));
    const files = readdirSync(TESTDATA)
      .filter((f) => f.endsWith(".json"))
      .map((f) => f.slice(0, -".json".length))
      .filter((f) => f.startsWith("v3_") || f.startsWith("relay_"));
    expect(files.length).toBeGreaterThan(0);
    expect(files.filter((f) => !names.has(f))).toEqual([]);
  });

  test("every registered v3 fixture has a file", () => {
    const files = new Set(readdirSync(TESTDATA));
    const missing = FIXTURES_V3.map(([n]) => n).filter((n) => !files.has(`${n}.json`));
    expect(missing).toEqual([]);
  });

  test("v3 and relay fixtures match the Go registry size", () => {
    // Go FixturesV3 has 31 entries, FixturesRelay 19.
    expect(FIXTURES_V3).toHaveLength(50);
  });
});
