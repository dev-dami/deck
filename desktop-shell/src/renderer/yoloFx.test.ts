import { expect, test } from "vitest";
import {
  arrival,
  bandColour,
  DURATION,
  done,
  easeOut,
  envelope,
  frontRadius,
  hueAt,
  kick,
  playsOn,
  regionShift,
  REGION_PUSH_PX,
  SETTLE_AT,
  smooth,
  TIER_FRONT,
  TIER_GLOW,
  TIER_MEDIUM,
  TIER_NONE,
  TIER_STRONG,
  TRAVEL,
  tier,
  tierEnds,
  WORD,
  WORD_COLS,
  WORD_OFF,
  WORD_ON,
  wordColour,
  wordShowing,
} from "./yoloFx";

// After internal/ui/yolo/rainbow_test.go: the effect is done at Duration,
// leaves the window alone before it starts and after it ends, and its front
// reaches the corners at travel.

test("the effect is done at DURATION and not before", () => {
  expect(done(0)).toBe(false);
  expect(done(DURATION - 1)).toBe(false);
  expect(done(DURATION)).toBe(true);
  expect(done(-1)).toBe(true);
  expect(envelope(DURATION)).toBe(0);
  expect(envelope(SETTLE_AT)).toBe(1);
  expect(envelope((SETTLE_AT + DURATION) / 2)).toBeCloseTo(0.5);
});

test("a region is untouched before the start and after the end", () => {
  for (const t of [-100, -1, DURATION, DURATION + 500]) {
    const s = regionShift(t, 680, 430, 100, 100);
    expect(s.dx).toBe(0);
    expect(s.dy).toBe(0);
  }
  // At the start the front has not reached a region away from the centre.
  expect(regionShift(0, 680, 430, 100, 100)).toMatchObject({ dx: 0, dy: 0 });
});

test("the front reaches the corners at TRAVEL", () => {
  const reach = Math.hypot(680, 430);
  expect(frontRadius(0, reach)).toBe(0);
  expect(frontRadius(TRAVEL, reach)).toBeCloseTo(reach);
  expect(frontRadius(TRAVEL + 300, reach)).toBeCloseTo(reach);
  expect(frontRadius(TRAVEL / 2, reach)).toBeGreaterThan(reach / 2);
  // arrival inverts it.
  for (const t of [50, 300, 700]) expect(arrival(frontRadius(t, reach), reach)).toBeCloseTo(t);
  expect(arrival(reach, reach)).toBeCloseTo(TRAVEL);
});

test("the easings run from 0 to 1", () => {
  expect(easeOut(0)).toBe(0);
  expect(easeOut(1)).toBe(1);
  expect(easeOut(2)).toBe(1);
  expect(smooth(-1)).toBe(0);
  expect(smooth(0.5)).toBe(0.5);
});

test("the spring kicks outwards, swings back and dies away", () => {
  expect(kick(-0.1)).toBe(0);
  expect(kick(0)).toBe(0);
  expect(kick(0.07)).toBeGreaterThan(2);
  expect(kick(0.25)).toBeLessThan(0);
  expect(Math.abs(kick(1.2))).toBeLessThan(0.01);
});

test("a region swings outwards from the centre, about REGION_PUSH_PX at the peak", () => {
  const cx = 680;
  const cy = 430;
  let peak = 0;
  for (let t = 0; t < DURATION; t += 4) {
    const s = regionShift(t, cx, cy, 100, cy);
    // Outwards is left of the centre here.
    if (s.dx < 0) peak = Math.max(peak, -s.dx);
    expect(Math.abs(s.dy)).toBeLessThan(1e-9);
  }
  expect(peak).toBeGreaterThan(REGION_PUSH_PX * 0.9);
  expect(peak).toBeLessThanOrEqual(REGION_PUSH_PX + 1e-9);
});

test("the band's tiers run from the crest back to the window's colours", () => {
  expect(tier(-0.01, 1)).toBe(TIER_NONE);
  expect(tier(0.01, 1)).toBe(TIER_FRONT);
  expect(tier(0.05, 1)).toBe(TIER_STRONG);
  expect(tier(0.15, 1)).toBe(TIER_MEDIUM);
  expect(tier(0.25, 1)).toBe(TIER_GLOW);
  expect(tier(0.5, 1)).toBe(TIER_NONE);
  // Settling, the crest goes first.
  expect(tier(0.01, 0.4)).toBe(TIER_MEDIUM);
  expect(tierEnds(1).map(([t]) => t)).toEqual([TIER_FRONT, TIER_STRONG, TIER_MEDIUM, TIER_GLOW]);
  expect(tierEnds(0.2).map(([t]) => t)).toEqual([TIER_GLOW]);
  for (const [t, end] of tierEnds(1)) expect(tier(end - 1e-6, 1)).toBe(t);
});

test("the hue rises outwards and runs outwards over time", () => {
  expect(hueAt(0, 0)).toBe(0);
  expect(hueAt(10, 0)).toBe(90);
  expect(hueAt(0, 0.1)).toBeCloseTo(308);
  for (const r of [0, 3, 50, 400]) for (const t of [0, 0.4, 1.4]) expect(hueAt(r, t)).toBeGreaterThanOrEqual(0);
  expect(bandColour(TIER_FRONT, 30, true)).toMatch(/^hsl\(30\.0 100% 92% \//);
  expect(bandColour(TIER_FRONT, 30, false)).toMatch(/^hsl\(30\.0 35% 9% \//);
});

test("YOLO is a 5-row block word that pops in and goes", () => {
  expect(WORD).toHaveLength(5);
  for (const row of WORD) expect(row).toHaveLength(WORD_COLS);
  const art = WORD.map((r) => r.map((on) => (on ? "#" : ".")).join(""));
  expect(art[0]).toBe("#...#...###...#.......###.");
  expect(art[4]).toBe("..#.....###...#####...###.");
  expect(wordShowing(WORD_ON - 1)).toBe(false);
  expect(wordShowing(WORD_ON)).toBe(true);
  expect(wordShowing(WORD_OFF)).toBe(false);
  expect(wordColour(WORD_ON, true)).toBe("rgb(255 255 255)");
  expect(wordColour(WORD_ON + 1000, true)).toBe("rgb(244 247 246)");
  expect(wordColour(WORD_ON, false)).toBe("rgb(0 0 0)");
});

test("only a switch of the open room to yolo plays", () => {
  const at = (room: string, mode: "plan" | "ask" | "yolo" | null) => ({ room, mode });
  expect(playsOn(at("a", "ask"), at("a", "yolo"))).toBe(true);
  expect(playsOn(at("a", "plan"), at("a", "yolo"))).toBe(true);
  // Opening a room already in yolo, the app's start, a room still loading.
  expect(playsOn(at("a", "ask"), at("b", "yolo"))).toBe(false);
  expect(playsOn(null, at("a", "yolo"))).toBe(false);
  expect(playsOn(at("a", null), at("a", "yolo"))).toBe(false);
  expect(playsOn(at("a", "yolo"), at("a", "yolo"))).toBe(false);
  expect(playsOn(at("a", "yolo"), at("a", "ask"))).toBe(false);
  expect(playsOn(at("", "ask"), at("", "yolo"))).toBe(false);
});
