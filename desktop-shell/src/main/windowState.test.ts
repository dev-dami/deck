import { EventEmitter } from "node:events";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, test, vi } from "vitest";
import {
  DEFAULT_WINDOW,
  nextZoom,
  parseWindowState,
  placeWindow,
  type Rect,
  trackWindow,
  WindowStateFile,
} from "./windowState";

const laptop: Rect = { x: 0, y: 0, width: 1440, height: 875 };
// A second display to the right of the laptop.
const monitor: Rect = { x: 1440, y: 0, width: 2560, height: 1415 };

describe("parseWindowState", () => {
  test("keeps what is valid and defaults the rest", () => {
    expect(parseWindowState(null)).toEqual(DEFAULT_WINDOW);
    expect(parseWindowState("x")).toEqual(DEFAULT_WINDOW);
    expect(parseWindowState({ width: 1000, height: 700, x: 5, y: 6, maximized: true, zoom: 1 })).toEqual({
      width: 1000,
      height: 700,
      x: 5,
      y: 6,
      maximized: true,
      zoom: 1,
    });
    expect(parseWindowState({ width: -1, height: "700", x: 5, maximized: "yes", zoom: 99 })).toEqual({
      ...DEFAULT_WINDOW,
      zoom: 5,
    });
  });
});

describe("placeWindow", () => {
  test("no saved place: the saved size, fitted to the first display", () => {
    expect(placeWindow(DEFAULT_WINDOW, [laptop])).toEqual({ width: 1320, height: 840 });
    expect(placeWindow({ ...DEFAULT_WINDOW, width: 3000, height: 2000 }, [laptop])).toEqual({ width: 1440, height: 875 });
  });

  test("a window on the second display stays there", () => {
    const s = { ...DEFAULT_WINDOW, x: 2000, y: 100, width: 1600, height: 1000 };
    expect(placeWindow(s, [laptop, monitor])).toEqual({ x: 2000, y: 100, width: 1600, height: 1000 });
  });

  test("a window that hangs off an edge moves back inside", () => {
    const s = { ...DEFAULT_WINDOW, x: 1000, y: -50, width: 1320, height: 840 };
    expect(placeWindow(s, [laptop])).toEqual({ x: 120, y: 0, width: 1320, height: 840 });
  });

  test("a window on a display that is gone opens on the first one, centred", () => {
    const s = { ...DEFAULT_WINDOW, x: 2000, y: 100, width: 1600, height: 1000 };
    expect(placeWindow(s, [laptop])).toEqual({ width: 1440, height: 875 });
  });

  test("never smaller than the minimum where the display allows it", () => {
    const s = { ...DEFAULT_WINDOW, x: 0, y: 0, width: 200, height: 200 };
    expect(placeWindow(s, [laptop])).toEqual({ x: 0, y: 0, width: 960, height: 560 });
  });
});

describe("nextZoom", () => {
  test("steps by half a level, within bounds, and resets", () => {
    expect(nextZoom(0, 1)).toBe(0.5);
    expect(nextZoom(0.5, -1)).toBe(0);
    expect(nextZoom(2, 0)).toBe(0);
    expect(nextZoom(5, 1)).toBe(5);
    expect(nextZoom(-3, -1)).toBe(-3);
  });
});

describe("WindowStateFile", () => {
  test("writes window.json and reads it back", () => {
    const file = join(mkdtempSync(join(tmpdir(), "deck-window-")), "window.json");
    const a = new WindowStateFile(file);
    expect(a.state).toEqual(DEFAULT_WINDOW);
    a.set({ x: 10, y: 20, width: 1000, height: 700, maximized: true });
    a.set({ zoom: 1 });
    expect(new WindowStateFile(file).state).toEqual({ x: 10, y: 20, width: 1000, height: 700, maximized: true, zoom: 1 });
  });

  test("a broken file is the default state", () => {
    const file = join(mkdtempSync(join(tmpdir(), "deck-window-")), "window.json");
    writeFileSync(file, "{not json");
    expect(new WindowStateFile(file).state).toEqual(DEFAULT_WINDOW);
  });
});

class FakeWindow extends EventEmitter {
  bounds: Rect = { x: 0, y: 0, width: 1320, height: 840 };
  maximized = false;
  minimized = false;
  getNormalBounds = () => this.bounds;
  isMaximized = () => this.maximized;
  isMinimized = () => this.minimized;
}

describe("trackWindow", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  test("saves once the window is still, and at once on close", () => {
    vi.useFakeTimers();
    const file = join(mkdtempSync(join(tmpdir(), "deck-window-")), "window.json");
    const store = new WindowStateFile(file);
    const w = new FakeWindow();
    trackWindow(w, store, 500);
    const saved = () => JSON.parse(readFileSync(file, "utf8")) as Record<string, unknown>;

    w.bounds = { x: 50, y: 60, width: 1000, height: 700 };
    w.emit("move");
    w.emit("resize");
    vi.advanceTimersByTime(400);
    expect(() => saved()).toThrow();
    vi.advanceTimersByTime(200);
    expect(saved()).toMatchObject({ x: 50, y: 60, width: 1000, height: 700, maximized: false });

    // A minimized window keeps the place it had.
    w.minimized = true;
    w.bounds = { x: -32000, y: -32000, width: 160, height: 28 };
    w.emit("move");
    vi.advanceTimersByTime(600);
    expect(saved()).toMatchObject({ x: 50, y: 60 });

    w.minimized = false;
    w.maximized = true;
    w.bounds = { x: 70, y: 80, width: 1100, height: 750 };
    w.emit("maximize");
    w.emit("close");
    expect(saved()).toMatchObject({ x: 70, y: 80, width: 1100, height: 750, maximized: true });
  });
});
