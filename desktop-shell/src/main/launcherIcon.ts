import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, win32 } from "node:path";
import type { AppIconImage, LauncherState } from "../shared/bridge";

// The installed app's own icon: the shortcuts the Windows installer made
// (desktop, Start menu, a taskbar pin) and the .desktop launcher a Linux
// package put in the menu. The running app's icon is main.ts's showIcon;
// this makes the places Deck starts from wear the same one, and puts the
// standard icon back when the classic one is picked. A Mac's bundle is
// signed and keeps its icon.

// An ICO of the images, each stored as the PNG it is (Windows reads PNG
// entries since Vista), smallest first. An ICO holds sizes up to 256.
export function encodeIco(images: AppIconImage[]): Uint8Array {
  const list = images.filter((i) => i.size >= 1 && i.size <= 256).sort((a, b) => a.size - b.size);
  if (list.length === 0) throw new Error("no image an ICO can hold");
  const head = 6 + 16 * list.length;
  const out = new Uint8Array(head + list.reduce((n, i) => n + i.png.length, 0));
  const v = new DataView(out.buffer);
  // Reserved, type 1 (icon), count.
  v.setUint16(0, 0, true);
  v.setUint16(2, 1, true);
  v.setUint16(4, list.length, true);
  let offset = head;
  list.forEach((i, n) => {
    const e = 6 + 16 * n;
    // Width and height, 0 meaning 256; no palette; one plane of 32 bits.
    out[e] = i.size & 0xff;
    out[e + 1] = i.size & 0xff;
    v.setUint16(e + 4, 1, true);
    v.setUint16(e + 6, 32, true);
    v.setUint32(e + 8, i.png.length, true);
    v.setUint32(e + 12, offset, true);
    out.set(i.png, offset);
    offset += i.png.length;
  });
  return out;
}

// A shortcut the installer may have made, and whether it is machine-wide
// (a per-machine install's, which only an administrator can change).
export interface Shortcut {
  path: string;
  machine: boolean;
}

// Where electron-builder's NSIS installer puts Deck.lnk (nsis.shortcutName
// in package.json): the desktop and the Start menu's Programs, the user's
// for a per-user install and everyone's for a per-machine one, and the
// taskbar pin Windows makes when the person pins Deck.
export function windowsShortcuts(env: Record<string, string | undefined>, desktop: string, name = "Deck"): Shortcut[] {
  const lnk = `${name}.lnk`;
  const out: Shortcut[] = [{ path: win32.join(desktop, lnk), machine: false }];
  const appData = env["APPDATA"];
  if (appData) {
    out.push({ path: win32.join(appData, "Microsoft", "Windows", "Start Menu", "Programs", lnk), machine: false });
    out.push({ path: win32.join(appData, "Microsoft", "Internet Explorer", "Quick Launch", "User Pinned", "TaskBar", lnk), machine: false });
  }
  const pub = env["PUBLIC"];
  if (pub) out.push({ path: win32.join(pub, "Desktop", lnk), machine: true });
  const programData = env["ProgramData"] ?? env["PROGRAMDATA"];
  if (programData) out.push({ path: win32.join(programData, "Microsoft", "Windows", "Start Menu", "Programs", lnk), machine: true });
  return out;
}

// Electron's shell.readShortcutLink and writeShortcutLink, as this needs
// them; the tests stub them.
export interface ShortcutShell {
  // The link's target and icon; throws when it cannot be read.
  read: (path: string) => { target: string; icon?: string; iconIndex?: number };
  // Points the link's icon at icon (an .ico, or the exe for its own);
  // false when it could not be written.
  write: (path: string, icon: string) => boolean;
}

const samePath = (a: string, b: string): boolean => a.toLowerCase() === b.toLowerCase();

// Points each shortcut that starts this exe at icon. Shortcuts to anything
// else, and ones already there, are left alone. changed: whether one was
// written, so the shell's icon cache needs a nudge.
export function syncShortcuts(
  links: Shortcut[],
  exe: string,
  icon: string,
  sh: ShortcutShell,
  exists: (p: string) => boolean,
): { state: LauncherState; changed: boolean } {
  let found = false;
  let changed = false;
  let denied = false;
  let failed = false;
  for (const l of links) {
    if (!exists(l.path)) continue;
    let cur: { target: string; icon?: string; iconIndex?: number };
    try {
      cur = sh.read(l.path);
    } catch {
      continue;
    }
    if (!samePath(cur.target, exe)) continue;
    found = true;
    // A shortcut with no icon of its own shows its target's.
    const curIcon = cur.icon ? cur.icon : exe;
    if (samePath(curIcon, icon) && (cur.iconIndex ?? 0) === 0) continue;
    let ok = false;
    try {
      ok = sh.write(l.path, icon);
    } catch {
      ok = false;
    }
    if (ok) changed = true;
    else if (l.machine) denied = true;
    else failed = true;
  }
  const state: LauncherState = !found ? "none" : denied ? "denied" : failed ? "failed" : "done";
  return { state, changed };
}

// The desktop file id package.json's desktopName gives the Linux launcher.
export const DESKTOP_ID = "deck-desktop.desktop";
// Marks a copy of the system's launcher Deck made under the user's
// applications, which shadows it and goes when the standard icon is back.
const COPY_MARK = "X-Deck-Icon-Copy";
// Marks a launcher of the user's whose Icon= Deck changed, with the one it
// had.
const ORIGINAL_MARK = "X-Deck-Original-Icon";

// The [Desktop Entry] group's value of key, or null.
export function desktopValue(text: string, key: string): string | null {
  let main = false;
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (t.startsWith("[")) main = t === "[Desktop Entry]";
    else if (main && t.startsWith(`${key}=`)) return t.slice(key.length + 1);
  }
  return null;
}

// text with the [Desktop Entry] group's key set to value (added under the
// group's header when missing), or removed when value is null. Other
// groups, such as a [Desktop Action]'s own Icon=, keep theirs.
export function setDesktopValue(text: string, key: string, value: string | null): string {
  const out: string[] = [];
  let main = false;
  let done = value === null;
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (t.startsWith("[")) main = t === "[Desktop Entry]";
    else if (main && t.startsWith(`${key}=`)) {
      if (!done) out.push(`${key}=${value}`);
      done = true;
      continue;
    }
    out.push(line);
  }
  if (!done) out.splice(out.findIndex((l) => l.trim() === "[Desktop Entry]") + 1, 0, `${key}=${value}`);
  return out.join("\n");
}

// Where a Linux launcher for Deck lives: the user's applications (which
// shadow the system's) and the system's, from the XDG base directories.
export function linuxLaunchers(env: Record<string, string | undefined>, home: string): { user: string; system: string[] } {
  const dataHome = env["XDG_DATA_HOME"] || join(home, ".local", "share");
  const dirs = (env["XDG_DATA_DIRS"] || "/usr/local/share:/usr/share").split(":").filter((d) => d !== "");
  return { user: join(dataHome, "applications", DESKTOP_ID), system: dirs.map((d) => join(d, "applications", DESKTOP_ID)) };
}

// What the user's launcher should hold for icon (a PNG's path, or null for
// the standard icon), or null to remove it; undefined leaves it as it is.
// user is its text now (null when there is none), system the installed
// launcher's (null when Deck was not installed by a package).
export function linuxLauncherText(user: string | null, system: string | null, icon: string | null): string | null | undefined {
  if (user !== null && desktopValue(user, COPY_MARK) === "true") {
    // Deck's copy: back to the standard icon removes it; otherwise it is
    // made again from the system's, which a newer package may have changed.
    if (icon === null) return null;
    const base = system ?? user;
    return setDesktopValue(setDesktopValue(base, COPY_MARK, "true"), "Icon", icon);
  }
  if (user !== null) {
    // The user's own launcher: its icon changes, and the old one is kept.
    const original = desktopValue(user, ORIGINAL_MARK);
    if (icon === null) {
      if (original === null) return undefined;
      return setDesktopValue(setDesktopValue(user, "Icon", original), ORIGINAL_MARK, null);
    }
    const marked = original === null ? setDesktopValue(user, ORIGINAL_MARK, desktopValue(user, "Icon") ?? "") : user;
    return setDesktopValue(marked, "Icon", icon);
  }
  if (system === null || icon === null) return undefined;
  return setDesktopValue(setDesktopValue(system, COPY_MARK, "true"), "Icon", icon);
}

export interface LauncherDeps {
  platform: NodeJS.Platform;
  // Only an installed app changes the launchers: a checkout's would point
  // them at its development build.
  packaged: boolean;
  // Where the icon files go (under userData).
  dir: string;
  exe: string;
  windows: { links: Shortcut[]; shell: ShortcutShell; refresh: () => void };
  linux: { user: string; system: string[] };
}

const readOrNull = (p: string): string | null => {
  try {
    return readFileSync(p, "utf8");
  } catch {
    return null;
  }
};

// Writes bytes to dir/app-<hash>.<ext> unless there; a new name for each
// icon, so the shell's cache, keyed by path, cannot show the old one.
function iconFile(dir: string, ext: string, bytes: Uint8Array): string {
  const file = join(dir, `app-${createHash("sha256").update(bytes).digest("hex").slice(0, 16)}.${ext}`);
  if (!existsSync(file)) {
    mkdirSync(dir, { recursive: true, mode: 0o700 });
    writeFileSync(`${file}.tmp`, bytes);
    renameSync(`${file}.tmp`, file);
  }
  return file;
}

// Removes the icon files in dir but keep (null keeps none).
function prune(dir: string, keep: string | null): void {
  let names: string[];
  try {
    names = readdirSync(dir);
  } catch {
    return;
  }
  for (const n of names) {
    const p = join(dir, n);
    if (/^app-[0-9a-f]+\.(ico|png)$/.test(n) && p !== keep) rmSync(p, { force: true });
  }
}

// Gives the installed app's launchers the icon (standard: the one Deck
// ships with, which the exe and the package carry). file: the icon file
// written, for the window to show.
export function applyLauncherIcon(images: AppIconImage[], standard: boolean, d: LauncherDeps): { state: LauncherState; file: string | null } {
  if (!d.packaged) return { state: "none", file: null };
  if (d.platform === "win32") {
    const file = standard ? null : iconFile(d.dir, "ico", encodeIco(images));
    const r = syncShortcuts(d.windows.links, d.exe, file ?? d.exe, d.windows.shell, existsSync);
    if (r.changed) d.windows.refresh();
    if (r.state === "done" || r.state === "none") prune(d.dir, file);
    return { state: r.state, file };
  }
  if (d.platform === "linux") {
    const user = readOrNull(d.linux.user);
    const system = d.linux.system.map(readOrNull).find((t) => t !== null) ?? null;
    // An AppImage nobody integrated has no launcher to change.
    if (user === null && system === null) return { state: "none", file: null };
    const largest = [...images].filter((i) => i.size <= 256).sort((a, b) => b.size - a.size)[0];
    const file = standard || largest === undefined ? null : iconFile(d.dir, "png", largest.png);
    const next = linuxLauncherText(user, system, file);
    try {
      if (next === null) rmSync(d.linux.user, { force: true });
      else if (next !== undefined && next !== user) {
        mkdirSync(dirname(d.linux.user), { recursive: true });
        writeFileSync(`${d.linux.user}.tmp`, next);
        renameSync(`${d.linux.user}.tmp`, d.linux.user);
      }
    } catch {
      return { state: "failed", file };
    }
    prune(d.dir, file);
    return { state: "done", file };
  }
  return { state: "none", file: null };
}
