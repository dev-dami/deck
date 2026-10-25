import type { MenuItemConstructorOptions } from "electron";
import { describe, expect, test } from "vitest";
import { type ContextActions, type ContextParams, contextMenuTemplate } from "./contextMenu";

const none: ContextParams = {
  x: 10,
  y: 20,
  isEditable: false,
  selectionText: "",
  linkURL: "",
  mediaType: "none",
  misspelledWord: "",
  dictionarySuggestions: [],
  editFlags: { canCut: false, canCopy: false, canPaste: false, canSelectAll: false },
};

function record() {
  const calls: string[] = [];
  const a: ContextActions = {
    replaceMisspelling: (w) => calls.push(`replace ${w}`),
    copyText: (t) => calls.push(`copy ${t}`),
    openExternal: (u) => calls.push(`open ${u}`),
    copyImage: (x, y) => calls.push(`image ${x},${y}`),
  };
  return { a, calls };
}

// "role", "label" or "-" for each item.
const names = (t: MenuItemConstructorOptions[]) => t.map((i) => (i.type === "separator" ? "-" : (i.role ?? i.label)));
const click = (t: MenuItemConstructorOptions[], label: string) =>
  (t.find((i) => i.label === label)?.click as unknown as (() => void) | undefined)?.();

describe("contextMenuTemplate", () => {
  test("nothing to offer, no menu", () => {
    expect(contextMenuTemplate(none, record().a)).toEqual([]);
    expect(contextMenuTemplate({ ...none, selectionText: "  " }, record().a)).toEqual([]);
  });

  test("a text field gets the edit actions, enabled by what it allows", () => {
    const t = contextMenuTemplate(
      { ...none, isEditable: true, editFlags: { canCut: false, canCopy: false, canPaste: true, canSelectAll: true } },
      record().a,
    );
    expect(names(t)).toEqual(["cut", "copy", "paste", "-", "selectAll"]);
    expect(t.map((i) => i.enabled)).toEqual([false, false, true, undefined, true]);
  });

  test("spelling suggestions come first and replace the word", () => {
    const { a, calls } = record();
    const t = contextMenuTemplate(
      { ...none, isEditable: true, misspelledWord: "teh", dictionarySuggestions: ["the", "tech"] },
      a,
    );
    expect(names(t)).toEqual(["the", "tech", "-", "cut", "copy", "paste", "-", "selectAll"]);
    click(t, "tech");
    expect(calls).toEqual(["replace tech"]);
  });

  test("a selection gets Copy", () => {
    expect(names(contextMenuTemplate({ ...none, selectionText: "hello" }, record().a))).toEqual(["copy"]);
  });

  test("an http(s) link can be copied or opened in the browser; others are not links", () => {
    const { a, calls } = record();
    const t = contextMenuTemplate({ ...none, linkURL: "https://example.com/a" }, a);
    expect(names(t)).toEqual(["Copy link", "Open in browser"]);
    click(t, "Copy link");
    click(t, "Open in browser");
    expect(calls).toEqual(["copy https://example.com/a", "open https://example.com/a"]);
    expect(contextMenuTemplate({ ...none, linkURL: "file:///etc/passwd" }, a)).toEqual([]);
    expect(contextMenuTemplate({ ...none, linkURL: "javascript:alert(1)" }, a)).toEqual([]);
  });

  test("an image can be copied where it was clicked", () => {
    const { a, calls } = record();
    const t = contextMenuTemplate({ ...none, mediaType: "image", selectionText: "alt" }, a);
    expect(names(t)).toEqual(["copy", "-", "Copy image"]);
    click(t, "Copy image");
    expect(calls).toEqual(["image 10,20"]);
  });
});
