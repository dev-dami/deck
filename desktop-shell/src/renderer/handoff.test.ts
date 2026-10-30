import { describe, expect, test } from "vitest";
import { foldsBody, handoffOf } from "./handoff";

describe("handoffOf", () => {
  test("reads the recipients and the message", () => {
    expect(handoffOf("→ $codex $claude-2\n\nreview the limiter")).toEqual({ to: ["codex", "claude-2"], body: "review the limiter", task: null });
    expect(handoffOf("→ @charles\n\nhi")?.to).toEqual(["charles"]);
  });

  test("reads a task's report", () => {
    const h = handoffOf('→ $claude\n\ntask t3 "Prune idle buckets" done: added prune() and a test\nsecond line');
    expect(h?.task).toEqual({ id: "t3", title: "Prune idle buckets", state: "done", result: "added prune() and a test\nsecond line" });
    expect(handoffOf('→ $claude\n\ntask t5 "Fix \\"it\\"" failed')?.task).toMatchObject({ id: "t5", state: "failed", result: "" });
  });

  test("anything else is prose", () => {
    expect(handoffOf("The limiter → fixed")).toBeNull();
    expect(handoffOf("→ nobody\n\ntext")).toBeNull();
    expect(handoffOf("")).toBeNull();
  });

  test("folds long bodies only", () => {
    expect(foldsBody("short")).toBe(false);
    expect(foldsBody("x".repeat(500))).toBe(true);
    expect(foldsBody("a\nb\nc\nd\ne\nf\ng")).toBe(true);
  });
});
