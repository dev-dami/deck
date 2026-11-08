import { describe, expect, test } from "vitest";
import { defaultPrefs, merge } from "./prefs";
import {
  autoStarts,
  closeTour,
  firstStep,
  getTour,
  placeCard,
  setTourStep,
  shows,
  spotlight,
  startTour,
  stepFrom,
  stepsFor,
  stepText,
  TOUR_STEPS,
  type TourStep,
} from "./tour";

const step = (id: string, target: string | null): TourStep => ({ id, screen: "shell", target, title: id, body: "" });
const steps = [step("intro", null), step("a", ".a"), step("b", ".b"), step("c", ".c")];
const only = (...on: string[]) => (s: string) => on.includes(s);

describe("the tour's steps", () => {
  test("each screen has its steps, an intro without a target first", () => {
    for (const screen of ["welcome", "shell"] as const) {
      const list = stepsFor(screen);
      expect(list.length).toBeGreaterThan(2);
      expect(list[0]?.target).toBeNull();
      expect(list.every((s) => s.screen === screen)).toBe(true);
    }
    expect(new Set(TOUR_STEPS.map((s) => s.id)).size).toBe(TOUR_STEPS.length);
  });

  test("the shell's steps point at the room's own hooks", () => {
    const targets = stepsFor("shell").map((s) => s.target);
    for (const t of ['nav[aria-label="Threads"]', ".composer", ".mode-picker", "section.home", ".panel-toggles"]) expect(targets).toContain(t);
  });

  test("{mod} reads as the platform's key", () => {
    const palette = TOUR_STEPS.find((s) => s.id === "palette") as TourStep;
    expect(stepText(palette, "⌘").title).toBe("Everything in ⌘K");
    expect(stepText(palette, "Ctrl ").title).toBe("Everything in Ctrl K");
    expect(TOUR_STEPS.every((s) => !stepText(s, "⌘").body.includes("{mod}"))).toBe(true);
  });

  test("a step whose target is missing is passed over, both ways", () => {
    expect(stepFrom(steps, 0, 1, only(".a", ".c"))).toBe(1);
    expect(stepFrom(steps, 1, 1, only(".a", ".c"))).toBe(3);
    expect(stepFrom(steps, 3, -1, only(".a", ".c"))).toBe(1);
    expect(stepFrom(steps, 1, -1, only())).toBe(0);
    // Past the last step that shows, there is none.
    expect(stepFrom(steps, 1, 1, only(".a"))).toBeNull();
    expect(stepFrom(steps, 0, -1, only(".a"))).toBeNull();
    expect(firstStep([step("x", ".x"), step("y", ".y")], only(".y"))).toBe(1);
    expect(firstStep([step("x", ".x")], only())).toBeNull();
  });

  test("a step that shows always is kept without its target", () => {
    const kept = [step("intro", null), { ...step("a", ".a"), always: true }, step("b", ".b")];
    expect(shows(kept[1] as TourStep, only())).toBe(true);
    expect(shows(kept[2] as TourStep, only())).toBe(false);
    expect(stepFrom(kept, 0, 1, only())).toBe(1);
    expect(stepFrom(kept, 1, 1, only())).toBeNull();
    // The mode dial is only in a thread; what it does is worth a step anyway.
    expect(TOUR_STEPS.find((s) => s.id === "mode")?.always).toBe(true);
  });
});

describe("where the card sits", () => {
  const view = { w: 1200, h: 800 };
  const card = { w: 320, h: 160 };

  test("below the target, lined up with its middle", () => {
    expect(placeCard({ x: 500, y: 100, w: 200, h: 40 }, card, view)).toEqual({ x: 440, y: 154, side: "below" });
  });

  test("above a target at the foot of the window", () => {
    expect(placeCard({ x: 300, y: 700, w: 600, h: 80 }, card, view)).toEqual({ x: 440, y: 526, side: "above" });
  });

  test("beside a target as tall as the window", () => {
    expect(placeCard({ x: 8, y: 8, w: 256, h: 784 }, card, view)).toEqual({ x: 278, y: 320, side: "right" });
    expect(placeCard({ x: 936, y: 8, w: 256, h: 784 }, card, view).side).toBe("left");
  });

  test("in the middle without a target, or with one that fills the window", () => {
    expect(placeCard(null, card, view)).toEqual({ x: 440, y: 320, side: "center" });
    expect(placeCard({ x: 0, y: 0, w: 1200, h: 800 }, card, view).side).toBe("center");
  });

  test("never past the window's edges", () => {
    const at = placeCard({ x: 1150, y: 20, w: 40, h: 30 }, card, view);
    expect(at.x).toBe(1200 - 320 - 12);
    expect(placeCard({ x: 0, y: 20, w: 40, h: 30 }, card, view).x).toBe(12);
    // A window narrower than the card keeps the card's leading edge in it.
    expect(placeCard(null, card, { w: 200, h: 800 }).x).toBe(12);
  });

  test("the spotlight grows the target and stays in the window", () => {
    expect(spotlight({ x: 100, y: 100, w: 50, h: 20 }, view)).toEqual({ x: 94, y: 94, w: 62, h: 32 });
    expect(spotlight({ x: 2, y: 0, w: 1198, h: 800 }, view)).toEqual({ x: 0, y: 0, w: 1200, h: 800 });
  });
});

describe("starting and ending", () => {
  test("it starts by itself only on a real first run", () => {
    expect(autoStarts({ pref: "new", webdriver: false, automated: false })).toBe(true);
    expect(autoStarts({ pref: "done", webdriver: false, automated: false })).toBe(false);
    expect(autoStarts({ pref: "new", webdriver: true, automated: false })).toBe(false);
    expect(autoStarts({ pref: "new", webdriver: false, automated: true })).toBe(false);
  });

  test("the store opens on the first step, steps and closes", () => {
    startTour("shell");
    expect(getTour()).toEqual({ open: true, screen: "shell", index: 0, auto: false });
    setTourStep(3);
    expect(getTour().index).toBe(3);
    closeTour();
    expect(getTour().open).toBe(false);
    // A closed tour does not step.
    setTourStep(5);
    expect(getTour().index).toBe(3);
    startTour("welcome", true);
    expect(getTour()).toEqual({ open: true, screen: "welcome", index: 0, auto: true });
    closeTour();
  });

  test("a new window's tour is new; prefs saved before the tour leave it done", () => {
    expect(defaultPrefs.tour).toBe("new");
    expect(merge({ tour: "new" }).tour).toBe("new");
    expect(merge({ tour: "done" }).tour).toBe("done");
    expect(merge({}).tour).toBe("done");
    expect(merge({ tour: "maybe" }).tour).toBe("done");
  });
});
