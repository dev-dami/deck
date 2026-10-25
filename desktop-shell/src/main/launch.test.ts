import { describe, expect, test } from "vitest";
import { linkFromUrl, linksFromArgs, mustConfirm } from "./launch";

const dirs = new Set(["/home/me/app", "/home/me/src/app"]);
const isDir = (p: string) => dirs.has(p);

describe("linkFromUrl", () => {
  test("a pair link goes to the pairing form as it is", () => {
    const url = "deck://pair?h=192.168.1.5&p=7878&c=ABCDE&fp=SHA256:abc";
    expect(linkFromUrl(url, isDir)).toEqual({ type: "pair", url });
  });

  test("an open link names a folder that exists", () => {
    expect(linkFromUrl("deck://open?path=%2Fhome%2Fme%2Fapp", isDir)).toEqual({ type: "open", path: "/home/me/app" });
    expect(linkFromUrl("deck://open?path=%2Fno%2Fsuch", isDir)).toBeNull();
    expect(linkFromUrl("deck://open?path=app", isDir)).toBeNull();
    expect(linkFromUrl("deck://open", isDir)).toBeNull();
  });

  test("anything else is ignored", () => {
    expect(linkFromUrl("https://example.com", isDir)).toBeNull();
    expect(linkFromUrl("deck://shutdown", isDir)).toBeNull();
    expect(linkFromUrl("not a url", isDir)).toBeNull();
    expect(linkFromUrl(`deck://pair?c=${"A".repeat(3000)}`, isDir)).toBeNull();
  });
});

describe("linksFromArgs", () => {
  test("folders and links; switches and files are skipped", () => {
    expect(
      linksFromArgs(
        ["--no-sandbox", "src/app", "notes.txt", "deck://open?path=%2Fhome%2Fme%2Fapp", "deck://pair?c=X"],
        "/home/me",
        isDir,
      ),
    ).toEqual([
      { link: { type: "open", path: "/home/me/src/app" }, fromLink: false },
      { link: { type: "open", path: "/home/me/app" }, fromLink: true },
      { link: { type: "pair", url: "deck://pair?c=X" }, fromLink: true },
    ]);
  });
});

describe("mustConfirm", () => {
  test("a folder from a deck:// link asks first; a typed folder and a pair link do not", () => {
    const [typed, linked, pair] = linksFromArgs(
      ["src/app", "deck://open?path=%2Fhome%2Fme%2Fapp", "deck://pair?c=X"],
      "/home/me",
      isDir,
    );
    expect(typed !== undefined && mustConfirm(typed)).toBe(false);
    expect(linked !== undefined && mustConfirm(linked)).toBe(true);
    expect(pair !== undefined && mustConfirm(pair)).toBe(false);
    // macOS hands a link over through open-url.
    const viaOpenUrl = linkFromUrl("deck://open?path=%2Fhome%2Fme%2Fapp", isDir);
    expect(viaOpenUrl !== null && mustConfirm({ link: viaOpenUrl, fromLink: true })).toBe(true);
  });
});
