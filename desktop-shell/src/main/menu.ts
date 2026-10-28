import type { MenuItemConstructorOptions } from "electron";
import type { MenuCommand } from "../shared/bridge";

export interface MenuActions {
  // Sends a command to the window, which runs it as the palette would.
  send: (command: MenuCommand) => void;
  // One step in (1) or out (-1), or back to 100% (0).
  zoom: (step: -1 | 0 | 1) => void;
  openLogs: () => void;
  checkForUpdates: () => void;
}

// The window handles these keys itself (Shell.tsx), so the menu only shows
// them. Registered here too, they would run twice on Linux and Windows.
const shown = { registerAccelerator: false };

// The application menu: the standard roles plus Deck's commands. Reload and
// the developer tools are there only when the app is not packaged.
export function menuTemplate(o: { isMac: boolean; packaged: boolean; name: string }, a: MenuActions): MenuItemConstructorOptions[] {
  const cmd = (label: string, command: MenuCommand, accelerator?: string, own = false): MenuItemConstructorOptions => ({
    label,
    ...(accelerator === undefined ? {} : { accelerator }),
    ...(own ? shown : {}),
    click: () => a.send(command),
  });
  const sep: MenuItemConstructorOptions = { type: "separator" };
  const about = cmd(`About ${o.name}`, "about");
  const settings = cmd(o.isMac ? "Settings…" : "Settings", "settings", "CmdOrCtrl+,", true);

  const appMenu: MenuItemConstructorOptions[] = o.isMac
    ? [
        {
          label: o.name,
          submenu: [
            about,
            sep,
            settings,
            sep,
            { role: "services" },
            sep,
            { role: "hide" },
            { role: "hideOthers" },
            { role: "unhide" },
            sep,
            { role: "quit" },
          ],
        },
      ]
    : [];

  const file: MenuItemConstructorOptions = {
    label: "File",
    submenu: [
      cmd("New thread", "new-thread", "CmdOrCtrl+N", true),
      cmd("Open folder…", "open-folder", "CmdOrCtrl+O"),
      sep,
      ...(o.isMac ? [{ role: "close" } as const] : [settings, sep, { role: "quit" } as const]),
    ],
  };

  const edit: MenuItemConstructorOptions = {
    label: "Edit",
    submenu: [
      { role: "undo" },
      { role: "redo" },
      sep,
      { role: "cut" },
      { role: "copy" },
      { role: "paste" },
      ...(o.isMac ? [{ role: "pasteAndMatchStyle" } as const] : []),
      { role: "delete" },
      sep,
      { role: "selectAll" },
    ],
  };

  const view: MenuItemConstructorOptions = {
    label: "View",
    submenu: [
      cmd("Command palette", "palette", "CmdOrCtrl+K", true),
      sep,
      cmd("Inbox", "inbox"),
      cmd("Next room that needs you", "next-attention", "CmdOrCtrl+Alt+L", true),
      // The menu answers this key, so it works with a page of the browser focused.
      cmd("Browser address", "browser-address", "CmdOrCtrl+L"),
      {
        label: "Open room",
        submenu: ([1, 2, 3, 4, 5, 6, 7, 8, 9] as const).map((n) => cmd(`Room ${n}`, `room-${n}`, `CmdOrCtrl+${n}`, true)),
      },
      sep,
      cmd("Toggle sidebar", "toggle-sidebar", "CmdOrCtrl+B", true),
      cmd("Toggle side panel", "toggle-panel"),
      cmd("Toggle terminal", "toggle-terminal", "CmdOrCtrl+J", true),
      sep,
      { label: "Zoom in", accelerator: "CmdOrCtrl+=", click: () => a.zoom(1) },
      // The same with Shift, as the + sits above = on most keyboards.
      { label: "Zoom in", accelerator: "CmdOrCtrl+Plus", visible: false, click: () => a.zoom(1) },
      { label: "Zoom out", accelerator: "CmdOrCtrl+-", click: () => a.zoom(-1) },
      { label: "Actual size", accelerator: "CmdOrCtrl+0", click: () => a.zoom(0) },
      sep,
      { role: "togglefullscreen" },
      ...(o.packaged ? [] : [sep, { role: "reload" } as const, { role: "toggleDevTools" } as const]),
    ],
  };

  const help: MenuItemConstructorOptions = {
    role: "help",
    submenu: [
      { label: "Open logs folder", click: a.openLogs },
      { label: "Check for updates…", click: a.checkForUpdates },
      ...(o.isMac ? [] : [sep, about]),
    ],
  };

  return [...appMenu, file, edit, view, { role: "windowMenu" }, help];
}
