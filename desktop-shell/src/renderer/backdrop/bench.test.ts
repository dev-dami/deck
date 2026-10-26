import { test } from "vitest";
import { atkinson, atkinsonPalette, autoLevels, chartTable, hex, levels, screen } from "./core";

test("bench", () => {
  const w = 720;
  const h = 450;
  const v = new Float32Array(w * h).map((_, i) => (i % 97) / 97);
  let t = performance.now();
  autoLevels(v);
  console.log("auto", performance.now() - t);
  t = performance.now();
  levels(v, 0.1, 0.9, 1.1);
  console.log("levels", performance.now() - t);
  for (let k = 0; k < 3; k++) {
    t = performance.now();
    atkinson(v, w, h);
    console.log("atkinson", performance.now() - t);
  }
  t = performance.now();
  const rgb = new Float32Array(w * h * 3).map((_, i) => (i % 255));
  atkinsonPalette(rgb, w, h, ["#101119", "#1a1b26", "#7aa2f7", "#bb9af7", "#f7768e", "#e0af68", "#c0caf5"].map(hex));
  console.log("palette", performance.now() - t);
  t = performance.now();
  screen(2880, 1800, 10, 15);
  console.log("screen", performance.now() - t);
  t = performance.now();
  chartTable(144, 90);
  console.log("chart", performance.now() - t);
});
