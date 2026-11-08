import { expect, test } from "vitest";
import { defaultPrefs, merge } from "./prefs";
import { yoloMove } from "./yolo";

test("enters yolo from any other mode when it is allowed", () => {
  expect(yoloMove("ask", null, true)).toEqual({ to: "yolo" });
  expect(yoloMove("plan", null, true)).toEqual({ to: "yolo" });
  expect(yoloMove(null, null, true)).toEqual({ to: "yolo" });
});

test("refuses to enter yolo when the Deck does not allow it", () => {
  expect(yoloMove("ask", null, false)).toMatchObject({ refused: expect.stringContaining("not allowed") });
});

test("leaves yolo for the mode the thread was in, else Ask", () => {
  expect(yoloMove("yolo", "plan", true)).toEqual({ to: "plan" });
  expect(yoloMove("yolo", null, true)).toEqual({ to: "ask" });
  expect(yoloMove("yolo", "yolo", true)).toEqual({ to: "ask" });
});

test("leaving yolo is always possible, even if the grant was withdrawn", () => {
  expect(yoloMove("yolo", "ask", false)).toEqual({ to: "ask" });
});

test("Yolo does not ask first unless the person turns that on", () => {
  // New threads start in Yolo, so switching back to it is the normal state.
  expect(defaultPrefs.confirmYolo).toBe(false);
  expect(merge({}).confirmYolo).toBe(false);
  // The setting is kept: asking first stays possible.
  expect(merge({ confirmYolo: true }).confirmYolo).toBe(true);
});
