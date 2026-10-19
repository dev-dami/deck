import { delimiter } from "node:path";
import { describe, expect, test } from "vitest";
import { loginShellPath, mergePath, pathFromOutput, wellKnownDirs } from "./shellPath";

describe("the engine's PATH", () => {
  test("merges lists and keeps the first place of each folder", () => {
    expect(mergePath(["/a", "/b"].join(delimiter), undefined, ["/b", "", "/c"].join(delimiter))).toBe(["/a", "/b", "/c"].join(delimiter));
  });

  test("reads the PATH between the markers, whatever rc files print", () => {
    expect(pathFromOutput("welcome!\n__DECK_PATH__/x:/y__DECK_PATH__\nbye")).toBe("/x:/y");
    expect(pathFromOutput("no markers")).toBeNull();
  });

  test("names the usual install folders", () => {
    expect(wellKnownDirs("/home/u")).toContain("/home/u/.local/bin");
    expect(wellKnownDirs("/home/u")).toContain("/opt/homebrew/bin");
  });

  test.skipIf(process.platform === "win32")("asks the login shell", async () => {
    const p = await loginShellPath({ ...process.env, SHELL: "/bin/sh", PATH: "/usr/bin:/bin:/opt/test-bin" });
    expect(p).toContain("/usr/bin");
  });
});
