import type { MenuItemConstructorOptions } from "electron";
import { describe, expect, test } from "vitest";
import { type MenuActions, menuTemplate } from "../main/menu";
import { MENU_COMMANDS, type MenuCommand } from "../shared/bridge";
import { type Command, type CommandDeps, MENU_PALETTE, markMatch, type PaletteRoom, paletteCommands, searchPalette } from "./palette";

const noop = () => {};
const deps = (mac: boolean, terminal = true): CommandDeps => ({
  mac,
  terminal,
  newThread: noop,
  togglePanel: noop,
  toggleSidePanel: noop,
  toggleSidebar: noop,
  settings: noop,
  usage: noop,
  team: noop,
  inbox: noop,
  nextAttention: noop,
  room: noop,
  toggleTerminal: noop,
  theme: noop,
  addProject: noop,
  switchDeck: noop,
  welcome: noop,
});

// Each menu item that sends a command, with its key.
function menuCommands(isMac: boolean): { command: MenuCommand; accelerator: string | undefined }[] {
  let last: MenuCommand | null = null;
  const a: MenuActions = { send: (c) => (last = c), zoom: noop, openLogs: noop, checkForUpdates: noop };
  const walk = (t: MenuItemConstructorOptions[]): MenuItemConstructorOptions[] =>
    t.flatMap((i) => [i, ...(Array.isArray(i.submenu) ? walk(i.submenu) : [])]);
  const out: { command: MenuCommand; accelerator: string | undefined }[] = [];
  for (const item of walk(menuTemplate({ isMac, packaged: true, name: "Deck" }, a))) {
    last = null;
    (item.click as unknown as (() => void) | undefined)?.();
    const sent = last as MenuCommand | null;
    if (sent !== null) out.push({ command: sent, accelerator: typeof item.accelerator === "string" ? item.accelerator : undefined });
  }
  return out;
}

describe("the menu and the palette", () => {
  test("every menu command has a palette command, with its shortcut", () => {
    for (const mac of [true, false]) {
      const commands = paletteCommands(deps(mac));
      const items = menuCommands(mac);
      expect(new Set(items.map((i) => i.command))).toEqual(new Set(MENU_COMMANDS));
      for (const { command, accelerator } of items) {
        if (command === "palette") continue;
        const c = commands.find((x) => x.id === MENU_PALETTE[command]);
        expect(c, `menu command ${command}`).toBeDefined();
        if (accelerator !== undefined) expect(c?.hint, `shortcut of ${command}`).toBeTruthy();
      }
    }
  });

  test("the terminal is there only for a local project", () => {
    expect(paletteCommands(deps(false, false)).some((c) => c.id === "terminal")).toBe(false);
    expect(paletteCommands(deps(false, true)).find((c) => c.id === "terminal")?.hint).toBe("Ctrl J");
    expect(paletteCommands(deps(true, true)).find((c) => c.id === "new")?.hint).toBe("⌘N");
  });
});

const cmd = (id: string, label: string): Command => ({ id, label, run: noop });
const room = (over: Partial<PaletteRoom>): PaletteRoom => ({
  project: "p1",
  name: "deck",
  id: "r",
  title: "",
  updated: "2026-01-01T00:00:00Z",
  branches: [],
  texts: null,
  ...over,
});

describe("searchPalette", () => {
  const commands = [cmd("new", "New thread"), cmd("inbox", "Open inbox"), cmd("theme-dark", "Theme: dark")];
  const rooms = [
    room({ id: "a", title: "Fix the pairing lockout", updated: "2026-01-03T00:00:00Z", branches: ["deck/t3"] }),
    room({ id: "b", title: "Docs pass", project: "p2", name: "site", updated: "2026-01-02T00:00:00Z", texts: ["Done.\nThe README now lists --lan.", "Update the README"] }),
    room({ id: "c", title: "Long history", updated: "2026-01-01T00:00:00Z", texts: ["nothing here"] }),
  ];
  const ids = (items: ReturnType<typeof searchPalette>) =>
    items.map((i) => (i.type === "action" ? i.c.id : i.type === "room" ? `room:${i.room.id}` : `hit:${i.hit.message}`));

  test("an empty query shows the newest rooms, then the actions", () => {
    expect(ids(searchPalette("", commands, rooms))).toEqual(["room:a", "room:b", "room:c", "new", "inbox", "theme-dark"]);
  });

  test("finds rooms of every project by title and by branch", () => {
    expect(ids(searchPalette("docs", commands, rooms))).toEqual(["room:b"]);
    expect(ids(searchPalette("deck/t3", commands, rooms))).toEqual(["room:a"]);
    expect(ids(searchPalette("pairing fix", commands, rooms))).toEqual(["room:a"]);
  });

  test("finds loaded rooms by message text and shows the line", () => {
    const r = searchPalette("lists --lan", commands, rooms);
    expect(r).toEqual([{ type: "room", room: rooms[1], line: "The README now lists --lan." }]);
    // Too short to search messages.
    expect(ids(searchPalette("li", commands, rooms))).toEqual([]);
  });

  test("the messages a Deck found come after title matches and before loaded text", () => {
    const c = rooms[2]!;
    const hits = [{ room: c, message: "m7", snippet: "…the README of old…", at: 7 }];
    expect(ids(searchPalette("readme", commands, rooms, 12, hits))).toEqual(["hit:m7", "room:b"]);
    // A room the Deck found is not listed again by its loaded text.
    const inB = [{ room: rooms[1]!, message: "m2", snippet: "Update the README", at: 9 }];
    expect(ids(searchPalette("readme", commands, rooms, 12, inB))).toEqual(["hit:m2"]);
    // Below three letters the Decks are not asked, and old hits do not show.
    expect(ids(searchPalette("re", commands, rooms, 12, hits))).toEqual(["new"]);
  });

  test("a snippet splits around its match", () => {
    expect(markMatch("…keys the count by addr…", "COUNT")).toEqual(["…keys the ", "count", " by addr…"]);
    expect(markMatch("nothing", "count")).toBeNull();
  });

  test("a title match is listed once, without a line", () => {
    const r = searchPalette("docs pass", commands, [room({ id: "d", title: "Docs pass", texts: ["docs pass again"] })]);
    expect(r).toEqual([{ type: "room", room: expect.objectContaining({ id: "d" }), line: null }]);
  });

  test("> lists actions alone", () => {
    expect(ids(searchPalette(">", commands, rooms))).toEqual(["new", "inbox", "theme-dark"]);
    expect(ids(searchPalette("> the", commands, rooms))).toEqual(["theme-dark"]);
    expect(ids(searchPalette("thread", commands, rooms))).toEqual(["new"]);
  });

  test("caps the results", () => {
    const many = Array.from({ length: 20 }, (_, i) => room({ id: `m${i}`, title: `Room ${i}` }));
    expect(searchPalette("room", commands, many)).toHaveLength(8);
    expect(searchPalette("", commands, many, 6)).toHaveLength(6);
  });
});
