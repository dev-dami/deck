import type { MenuItemConstructorOptions } from "electron";
import type { ArchiveOffer, ThreadChoice } from "../shared/bridge";

// The parts of Electron's ContextMenuParams the menu reads.
export interface ContextParams {
  x: number;
  y: number;
  isEditable: boolean;
  selectionText: string;
  linkURL: string;
  mediaType: string;
  misspelledWord: string;
  dictionarySuggestions: string[];
  editFlags: { canCut: boolean; canCopy: boolean; canPaste: boolean; canSelectAll: boolean };
}

export interface ContextActions {
  replaceMisspelling: (word: string) => void;
  copyText: (text: string) => void;
  openExternal: (url: string) => void;
  copyImage: (x: number, y: number) => void;
}

const isWebLink = (url: string) => /^https?:\/\//i.test(url);

// The right-click menu for what is under the pointer: edit actions in a text
// field (with spelling suggestions), Copy on a selection, link and image
// actions. Empty when there is nothing to offer.
export function contextMenuTemplate(p: ContextParams, a: ContextActions): MenuItemConstructorOptions[] {
  const groups: MenuItemConstructorOptions[][] = [];
  if (p.isEditable && p.misspelledWord !== "" && p.dictionarySuggestions.length > 0) {
    groups.push(p.dictionarySuggestions.slice(0, 5).map((w) => ({ label: w, click: () => a.replaceMisspelling(w) })));
  }
  if (p.isEditable) {
    groups.push([
      { role: "cut", enabled: p.editFlags.canCut },
      { role: "copy", enabled: p.editFlags.canCopy },
      { role: "paste", enabled: p.editFlags.canPaste },
    ]);
    groups.push([{ role: "selectAll", enabled: p.editFlags.canSelectAll }]);
  } else if (p.selectionText.trim() !== "") {
    groups.push([{ role: "copy" }]);
  }
  if (isWebLink(p.linkURL)) {
    const url = p.linkURL;
    groups.push([
      { label: "Copy link", click: () => a.copyText(url) },
      { label: "Open in browser", click: () => a.openExternal(url) },
    ]);
  }
  if (p.mediaType === "image") {
    groups.push([{ label: "Copy image", click: () => a.copyImage(p.x, p.y) }]);
  }
  return groups.flatMap((g, i) => (i === 0 ? g : [{ type: "separator" as const }, ...g]));
}

// A thread row's menu: Rename, Archive (off while an agent works; a native
// menu has no tooltip, so the label says why) and Delete.
export function threadMenuTemplate(archive: ArchiveOffer, pick: (c: ThreadChoice) => void): MenuItemConstructorOptions[] {
  const items: MenuItemConstructorOptions[] = [{ label: "Rename…", click: () => pick("rename") }];
  if (archive !== "none") {
    items.push({ label: archive === "on" ? "Archive" : "Archive (stop its agents first)", enabled: archive === "on", click: () => pick("archive") });
  }
  return [...items, { type: "separator" }, { label: "Delete…", click: () => pick("delete") }];
}
