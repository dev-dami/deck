import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { fakePng } from "../../test/png";
import {
  applyLauncherIcon,
  DESKTOP_ID,
  desktopValue,
  encodeIco,
  type LauncherDeps,
  linuxLaunchers,
  linuxLauncherText,
  type Shortcut,
  type ShortcutShell,
  setDesktopValue,
  syncShortcuts,
  windowsShortcuts,
} from "./launcherIcon";

const set = (...sizes: number[]) => sizes.map((size) => ({ size, png: fakePng(size, size, 40 + size) }));

// The entries of an ICO, read back as Windows reads its directory.
function readIco(ico: Uint8Array): { size: number; bytes: number; offset: number; planes: number; bits: number }[] {
  const v = new DataView(ico.buffer, ico.byteOffset, ico.byteLength);
  expect([v.getUint16(0, true), v.getUint16(2, true)]).toEqual([0, 1]);
  return Array.from({ length: v.getUint16(4, true) }, (_, n) => {
    const e = 6 + 16 * n;
    return {
      size: ico[e] === 0 ? 256 : (ico[e] as number),
      bytes: v.getUint32(e + 8, true),
      offset: v.getUint32(e + 12, true),
      planes: v.getUint16(e + 4, true),
      bits: v.getUint16(e + 6, true),
    };
  });
}

describe("encodeIco", () => {
  test("holds each image up to 256 as its PNG, smallest first", () => {
    const images = set(256, 16, 512, 48, 24, 32, 64, 128);
    const ico = encodeIco(images);
    const entries = readIco(ico);
    expect(entries.map((e) => e.size)).toEqual([16, 24, 32, 48, 64, 128, 256]);
    for (const e of entries) {
      expect([e.planes, e.bits]).toEqual([1, 32]);
      const png = images.find((i) => i.size === e.size)?.png as Uint8Array;
      expect(e.bytes).toBe(png.length);
      expect(ico.subarray(e.offset, e.offset + e.bytes)).toEqual(png);
      // The width and height bytes agree.
      expect(ico[6 + 16 * entries.indexOf(e) + 1]).toBe(e.size & 0xff);
    }
    // The images follow the directory with no gap, and fill the file.
    expect(entries[0]?.offset).toBe(6 + 16 * entries.length);
    const last = entries[entries.length - 1];
    expect((last?.offset ?? 0) + (last?.bytes ?? 0)).toBe(ico.length);
  });

  test("the build's icon.ico is the build's PNGs, as electron-builder takes it", () => {
    const dir = join(__dirname, "..", "..", "build");
    const sizes = [16, 24, 32, 48, 64, 128, 256];
    const images = sizes.map((size) => ({ size, png: new Uint8Array(readFileSync(join(dir, "icons", `${size}x${size}.png`))) }));
    expect(new Uint8Array(readFileSync(join(dir, "icon.ico")))).toEqual(encodeIco(images));
    const pkg = JSON.parse(readFileSync(join(__dirname, "..", "..", "package.json"), "utf8"));
    expect(pkg.build.win.icon).toBe("build/icon.ico");
    expect(pkg.build.nsis.shortcutName).toBe("Deck");
    expect(pkg.build.appId).toBe("dev.deck.desktop");
    expect(`${pkg.desktopName}`).toBe(DESKTOP_ID);
  });

  test("refuses a set with nothing an ICO can hold", () => {
    expect(() => encodeIco(set(512))).toThrow();
  });
});

describe("windowsShortcuts", () => {
  test("the desktop, Start menu and taskbar pin of a per-user install, and a per-machine one's", () => {
    const env = { APPDATA: "C:\\Users\\a\\AppData\\Roaming", PUBLIC: "C:\\Users\\Public", ProgramData: "C:\\ProgramData" };
    expect(windowsShortcuts(env, "C:\\Users\\a\\OneDrive\\Desktop")).toEqual([
      { path: "C:\\Users\\a\\OneDrive\\Desktop\\Deck.lnk", machine: false },
      { path: "C:\\Users\\a\\AppData\\Roaming\\Microsoft\\Windows\\Start Menu\\Programs\\Deck.lnk", machine: false },
      { path: "C:\\Users\\a\\AppData\\Roaming\\Microsoft\\Internet Explorer\\Quick Launch\\User Pinned\\TaskBar\\Deck.lnk", machine: false },
      { path: "C:\\Users\\Public\\Desktop\\Deck.lnk", machine: true },
      { path: "C:\\ProgramData\\Microsoft\\Windows\\Start Menu\\Programs\\Deck.lnk", machine: true },
    ]);
    expect(windowsShortcuts({}, "D:\\Desk")).toEqual([{ path: "D:\\Desk\\Deck.lnk", machine: false }]);
  });
});

const EXE = "C:\\Users\\a\\AppData\\Local\\Programs\\Deck\\Deck.exe";

// Shortcuts in memory: path → its target and icon. A path in locked
// cannot be written, as an administrator's would not be.
function fakeShell(links: Record<string, { target: string; icon?: string; iconIndex?: number }>, locked: string[] = []) {
  const writes: [string, string][] = [];
  const sh: ShortcutShell = {
    read: (p) => {
      const l = links[p];
      if (l === undefined) throw new Error("no such shortcut");
      return { ...l };
    },
    write: (p, icon) => {
      if (locked.includes(p)) return false;
      writes.push([p, icon]);
      links[p] = { ...(links[p] as { target: string }), icon, iconIndex: 0 };
      return true;
    },
  };
  return { sh, writes, exists: (p: string) => p in links };
}

describe("syncShortcuts", () => {
  const desk: Shortcut = { path: "desk\\Deck.lnk", machine: false };
  const start: Shortcut = { path: "start\\Deck.lnk", machine: false };
  const pub: Shortcut = { path: "public\\Deck.lnk", machine: true };
  const ico = "C:\\Users\\a\\AppData\\Roaming\\Deck\\icons\\app-1.ico";

  test("points the installer's shortcuts at the icon, once", () => {
    const f = fakeShell({ [desk.path]: { target: EXE, icon: EXE }, [start.path]: { target: EXE.toUpperCase() } });
    expect(syncShortcuts([desk, start, pub], EXE, ico, f.sh, f.exists)).toEqual({ state: "done", changed: true });
    expect(f.writes).toEqual([
      [desk.path, ico],
      [start.path, ico],
    ]);
    // Already there: nothing is written, and the cache needs no nudge.
    expect(syncShortcuts([desk, start, pub], EXE, ico, f.sh, f.exists)).toEqual({ state: "done", changed: false });
    expect(f.writes).toHaveLength(2);
    // The standard icon is the exe's own again.
    expect(syncShortcuts([desk, start], EXE, EXE, f.sh, f.exists)).toEqual({ state: "done", changed: true });
    expect(f.writes.slice(2)).toEqual([
      [desk.path, EXE],
      [start.path, EXE],
    ]);
  });

  test("leaves a shortcut of the same name to something else, and one it cannot read", () => {
    const f = fakeShell({ [desk.path]: { target: "C:\\Other\\Deck.exe", icon: "C:\\Other\\Deck.exe" } });
    const broken = { ...f.sh, read: () => ({ target: EXE }) };
    expect(syncShortcuts([desk], EXE, ico, f.sh, f.exists)).toEqual({ state: "none", changed: false });
    expect(f.writes).toEqual([]);
    const unreadable: ShortcutShell = {
      ...broken,
      read: () => {
        throw new Error("bad link");
      },
    };
    expect(syncShortcuts([desk], EXE, ico, unreadable, () => true)).toEqual({ state: "none", changed: false });
  });

  test("a per-machine install's shortcuts need an administrator: denied, the user's still change", () => {
    const f = fakeShell({ [pub.path]: { target: EXE }, [start.path]: { target: EXE } }, [pub.path]);
    expect(syncShortcuts([start, pub], EXE, ico, f.sh, f.exists)).toEqual({ state: "denied", changed: true });
    expect(f.writes).toEqual([[start.path, ico]]);
    // A user's own that cannot be written, or a write that throws, failed.
    const g = fakeShell({ [desk.path]: { target: EXE } }, [desk.path]);
    expect(syncShortcuts([desk], EXE, ico, g.sh, g.exists).state).toBe("failed");
    const throws: ShortcutShell = {
      ...g.sh,
      write: () => {
        throw new Error("EPERM");
      },
    };
    expect(syncShortcuts([desk], EXE, ico, throws, g.exists).state).toBe("failed");
  });
});

const SYSTEM = `[Desktop Entry]
Name=Deck
Exec=/opt/Deck/deck-desktop %U
Terminal=false
Type=Application
Icon=deck-desktop
StartupWMClass=Deck

[Desktop Action new]
Name=New window
Icon=window-new
`;

describe("desktop entries", () => {
  test("read and set a key of the [Desktop Entry] group only", () => {
    expect(desktopValue(SYSTEM, "Icon")).toBe("deck-desktop");
    const changed = setDesktopValue(SYSTEM, "Icon", "/home/a/icon.png");
    expect(desktopValue(changed, "Icon")).toBe("/home/a/icon.png");
    expect(changed).toContain("Icon=window-new");
    expect(changed.split("\n")).toHaveLength(SYSTEM.split("\n").length);
    const marked = setDesktopValue(SYSTEM, "X-Mark", "true");
    expect(marked.split("\n").slice(0, 2)).toEqual(["[Desktop Entry]", "X-Mark=true"]);
    expect(setDesktopValue(marked, "X-Mark", null)).toBe(SYSTEM);
  });

  test("the launcher's text for each case", () => {
    const png = "/home/a/.config/Deck/icons/app-1.png";
    // Installed by a package: a copy under the user's applications.
    const copy = linuxLauncherText(null, SYSTEM, png) as string;
    expect(desktopValue(copy, "Icon")).toBe(png);
    expect(desktopValue(copy, "Exec")).toBe("/opt/Deck/deck-desktop %U");
    // Classic removes the copy; nothing to remove leaves nothing.
    expect(linuxLauncherText(copy, SYSTEM, null)).toBeNull();
    expect(linuxLauncherText(null, SYSTEM, null)).toBeUndefined();
    // The copy is made again from a newer package.
    const newer = SYSTEM.replace("/opt/Deck", "/opt/Deck2");
    expect(desktopValue(linuxLauncherText(copy, newer, png) as string, "Exec")).toBe("/opt/Deck2/deck-desktop %U");
    // The user's own launcher keeps its old icon to go back to.
    const own = SYSTEM.replace("Icon=deck-desktop", "Icon=/home/a/mine.svg");
    const changed = linuxLauncherText(own, null, png) as string;
    expect(desktopValue(changed, "Icon")).toBe(png);
    const again = linuxLauncherText(changed, null, `${png}2`) as string;
    expect(linuxLauncherText(again, null, null)).toBe(own);
    expect(linuxLauncherText(own, null, null)).toBeUndefined();
    // No launcher at all.
    expect(linuxLauncherText(null, null, png)).toBeUndefined();
  });

  test("where the launchers live", () => {
    expect(linuxLaunchers({}, "/home/a")).toEqual({
      user: `/home/a/.local/share/applications/${DESKTOP_ID}`,
      system: [`/usr/local/share/applications/${DESKTOP_ID}`, `/usr/share/applications/${DESKTOP_ID}`],
    });
    expect(linuxLaunchers({ XDG_DATA_HOME: "/d", XDG_DATA_DIRS: "/x:" }, "/home/a")).toEqual({
      user: `/d/applications/${DESKTOP_ID}`,
      system: [`/x/applications/${DESKTOP_ID}`],
    });
  });
});

describe("applyLauncherIcon", () => {
  const images = set(16, 24, 32, 48, 64, 128, 256);

  function deps(platform: NodeJS.Platform, over: Partial<LauncherDeps> = {}) {
    const root = mkdtempSync(join(tmpdir(), "deck-launcher-"));
    const refreshed: number[] = [];
    const desk = join(root, "Desktop", "Deck.lnk");
    const f = fakeShell({ [desk]: { target: EXE } });
    mkdirSync(join(root, "Desktop"));
    writeFileSync(desk, "lnk");
    const d: LauncherDeps = {
      platform,
      packaged: true,
      dir: join(root, "userData", "icons"),
      exe: EXE,
      windows: { links: [{ path: desk, machine: false }], shell: f.sh, refresh: () => refreshed.push(1) },
      linux: { user: join(root, "home", "applications", DESKTOP_ID), system: [join(root, "usr", DESKTOP_ID)] },
      ...over,
    };
    return { d, root, f, refreshed, desk };
  }

  test("Windows: writes an .ico, points the shortcuts at it, and back at the exe for the standard icon", () => {
    const { d, f, refreshed, desk } = deps("win32");
    const r = applyLauncherIcon(images, false, d);
    expect(r.state).toBe("done");
    expect(r.file).toMatch(/app-[0-9a-f]{16}\.ico$/);
    expect(readIco(new Uint8Array(readFileSync(r.file as string))).map((e) => e.size)).toEqual([16, 24, 32, 48, 64, 128, 256]);
    expect(f.writes).toEqual([[desk, r.file]]);
    expect(refreshed).toHaveLength(1);
    // The same icon again: nothing written, no nudge.
    applyLauncherIcon(images, false, d);
    expect(f.writes).toHaveLength(1);
    expect(refreshed).toHaveLength(1);
    // Another icon: another file, and the old one goes.
    const other = applyLauncherIcon(set(16, 32, 256), false, d);
    expect(other.file).not.toBe(r.file);
    expect(existsSync(r.file as string)).toBe(false);
    // The standard icon: the exe's own, and no icon file is left.
    expect(applyLauncherIcon(images, true, d)).toEqual({ state: "done", file: null });
    expect(f.writes[f.writes.length - 1]).toEqual([desk, EXE]);
    expect(readdirSync(d.dir)).toEqual([]);
  });

  test("Windows: a shortcut it may not change keeps the icon file it may still need", () => {
    const { d, desk } = deps("win32");
    const locked = fakeShell({ [desk]: { target: EXE } }, [desk]);
    const r = applyLauncherIcon(images, false, { ...d, windows: { ...d.windows, links: [{ path: desk, machine: true }], shell: locked.sh } });
    expect(r.state).toBe("denied");
    expect(existsSync(r.file as string)).toBe(true);
  });

  test("Linux: shadows the package's launcher with the icon, and removes the copy for the standard one", () => {
    const { d, root } = deps("linux");
    mkdirSync(join(root, "usr"));
    writeFileSync(d.linux.system[0] as string, SYSTEM);
    const r = applyLauncherIcon(images, false, d);
    expect(r.state).toBe("done");
    expect(r.file).toMatch(/app-[0-9a-f]{16}\.png$/);
    // The 256px image.
    expect(new Uint8Array(readFileSync(r.file as string))).toEqual(images[6]?.png);
    expect(desktopValue(readFileSync(d.linux.user, "utf8"), "Icon")).toBe(r.file);
    expect(applyLauncherIcon(images, true, d)).toEqual({ state: "done", file: null });
    expect(existsSync(d.linux.user)).toBe(false);
    expect(readFileSync(d.linux.system[0] as string, "utf8")).toBe(SYSTEM);
    expect(readdirSync(d.dir)).toEqual([]);
  });

  test("Linux without a launcher, a Mac, and a development build change nothing", () => {
    const linux = deps("linux");
    expect(applyLauncherIcon(images, false, linux.d)).toEqual({ state: "none", file: null });
    expect(existsSync(linux.d.dir)).toBe(false);
    const mac = deps("darwin");
    expect(applyLauncherIcon(images, false, mac.d)).toEqual({ state: "none", file: null });
    const dev = deps("win32", { packaged: false });
    expect(applyLauncherIcon(images, false, dev.d)).toEqual({ state: "none", file: null });
    expect(dev.f.writes).toEqual([]);
    expect(existsSync(dev.d.dir)).toBe(false);
  });
});
