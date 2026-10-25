import type { MenuItemConstructorOptions } from "electron";
import { describe, expect, test } from "vitest";
import type { MenuCommand } from "../shared/bridge";
import { type MenuActions, menuTemplate } from "./menu";

function record() {
  const sent: MenuCommand[] = [];
  const calls: string[] = [];
  const a: MenuActions = {
    send: (c) => sent.push(c),
    zoom: (s) => calls.push(`zoom ${s}`),
    openLogs: () => calls.push("logs"),
    checkForUpdates: () => calls.push("updates"),
  };
  return { a, sent, calls };
}

// Every item of the menu, depth first.
function items(t: MenuItemConstructorOptions[]): MenuItemConstructorOptions[] {
  return t.flatMap((i) => [i, ...(Array.isArray(i.submenu) ? items(i.submenu) : [])]);
}

const byLabel = (t: MenuItemConstructorOptions[], label: string) => items(t).find((i) => i.label === label);
const click = (i: MenuItemConstructorOptions | undefined) =>
  (i?.click as unknown as (() => void) | undefined)?.();

describe("menuTemplate", () => {
  test("has the standard menus, with the app menu on a Mac only", () => {
    const { a } = record();
    const mac = menuTemplate({ isMac: true, packaged: true, name: "Deck" }, a);
    const other = menuTemplate({ isMac: false, packaged: true, name: "Deck" }, a);
    expect(mac.map((m) => m.label ?? m.role)).toEqual(["Deck", "File", "Edit", "View", "windowMenu", "help"]);
    expect(other.map((m) => m.label ?? m.role)).toEqual(["File", "Edit", "View", "windowMenu", "help"]);
    for (const t of [mac, other]) {
      const roles = items(t).map((i) => i.role);
      for (const r of ["undo", "redo", "cut", "copy", "paste", "selectAll"]) expect(roles).toContain(r);
    }
    expect(items(mac).map((i) => i.role)).toContain("quit");
    expect(items(other).map((i) => i.role)).toContain("quit");
  });

  test("reload and the developer tools only when not packaged", () => {
    const { a } = record();
    const roles = (packaged: boolean) =>
      items(menuTemplate({ isMac: false, packaged, name: "Deck" }, a)).map((i) => i.role);
    expect(roles(true)).not.toContain("reload");
    expect(roles(true)).not.toContain("toggleDevTools");
    expect(roles(false)).toContain("reload");
    expect(roles(false)).toContain("toggleDevTools");
  });

  test("each Deck command sends its id, with its shortcut", () => {
    const { a, sent } = record();
    const t = menuTemplate({ isMac: false, packaged: true, name: "Deck" }, a);
    const want: [string, MenuCommand, string | undefined][] = [
      ["New thread", "new-thread", "CmdOrCtrl+N"],
      ["Open folder…", "open-folder", "CmdOrCtrl+O"],
      ["Settings", "settings", "CmdOrCtrl+,"],
      ["Toggle sidebar", "toggle-sidebar", "CmdOrCtrl+B"],
      ["Toggle side panel", "toggle-panel", undefined],
      ["Toggle terminal", "toggle-terminal", "CmdOrCtrl+J"],
      ["Command palette", "palette", "CmdOrCtrl+K"],
      ["About Deck", "about", undefined],
      ["Inbox", "inbox", undefined],
      ["Next room that needs you", "next-attention", "CmdOrCtrl+Alt+L"],
      ["Room 1", "room-1", "CmdOrCtrl+1"],
      ["Room 9", "room-9", "CmdOrCtrl+9"],
    ];
    for (const [label, command, accelerator] of want) {
      const item = byLabel(t, label);
      expect(item?.accelerator).toBe(accelerator);
      sent.length = 0;
      click(item);
      expect(sent).toEqual([command]);
    }
  });

  test("keys the window handles are shown but not registered", () => {
    const { a } = record();
    const t = menuTemplate({ isMac: false, packaged: true, name: "Deck" }, a);
    for (const label of ["New thread", "Settings", "Toggle sidebar", "Toggle terminal", "Command palette", "Next room that needs you", "Room 3"]) {
      expect(byLabel(t, label)?.registerAccelerator).toBe(false);
    }
    // Only the menu answers Open folder and the zoom keys.
    expect(byLabel(t, "Open folder…")?.registerAccelerator).toBeUndefined();
    expect(byLabel(t, "Zoom out")?.registerAccelerator).toBeUndefined();
  });

  test("zoom, logs and updates call main", () => {
    const { a, calls } = record();
    const t = menuTemplate({ isMac: true, packaged: true, name: "Deck" }, a);
    for (const label of ["Zoom in", "Zoom out", "Actual size", "Open logs folder", "Check for updates…"]) click(byLabel(t, label));
    expect(calls).toEqual(["zoom 1", "zoom -1", "zoom 0", "logs", "updates"]);
  });
});
