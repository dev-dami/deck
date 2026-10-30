import { expect, test } from "vitest";
import { leaseGlobs } from "./leases";

test("the globs of a lease summary, each with its lease's note", () => {
  expect(leaseGlobs("")).toEqual([]);
  expect(leaseGlobs("pkg/remote/** (exclusive)")).toEqual([{ glob: "pkg/remote/**", note: "exclusive" }]);
  expect(leaseGlobs("a/**, b.go (exclusive); c/**")).toEqual([
    { glob: "a/**", note: "exclusive" },
    { glob: "b.go", note: "exclusive" },
    { glob: "c/**", note: "" },
  ]);
  // Spaces and empty pieces are dropped; a note is whatever the brackets say.
  expect(leaseGlobs(" a ,, ; b (shared) ")).toEqual([
    { glob: "a", note: "" },
    { glob: "b", note: "shared" },
  ]);
});
