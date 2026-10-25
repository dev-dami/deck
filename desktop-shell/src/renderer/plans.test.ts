import { describe, expect, test } from "vitest";
import { approveMode, detectPlan, findPlan, nextModeMemory, planText } from "./plans";

const CLAUDE_PLAN = `I read the limiter and its tests. The count is global, so one bad phone locks out every other.

## Plan

1. Key the failure count by address in \`pkg/remote/auth/limit.go\`.
2. Prune addresses that have been quiet for an hour.
3. Add \`TestLimiterIsPerAddress\` with twenty failures from one address.
4. Run \`go test ./pkg/remote/...\`.

I will not touch the pairing flow.`;

const CODEX_PLAN = `**Proposed plan:**
1) Read \`internal/deck/session/leases.go\`
   - note where overlap notes are written
2) Carry \`Via\` through \`pkg/rpc/convert.go\`

3) Regenerate the protos with \`buf generate\``;

describe("a plan in a reply", () => {
  test("a claude reply with a heading, prose around it and four steps", () => {
    const p = findPlan(CLAUDE_PLAN);
    expect(p).not.toBeNull();
    expect(p?.heading).toBe("Plan");
    expect(p?.steps).toHaveLength(4);
    expect(p?.steps[0]).toBe("Key the failure count by address in `pkg/remote/auth/limit.go`.");
    expect(p?.before).toBe("I read the limiter and its tests. The count is global, so one bad phone locks out every other.");
    expect(p?.after).toBe("I will not touch the pairing flow.");
  });

  test("a bold label, parenthesis numbers, a sub-bullet and a blank line between steps", () => {
    const p = findPlan(CODEX_PLAN);
    expect(p?.heading).toBe("Proposed plan");
    expect(p?.steps).toEqual([
      "Read `internal/deck/session/leases.go`\n- note where overlap notes are written",
      "Carry `Via` through `pkg/rpc/convert.go`",
      "Regenerate the protos with `buf generate`",
    ]);
    expect(p?.before).toBe("");
  });

  test("a list with no heading keeps the line above it as prose", () => {
    const p = findPlan("I will do this in order.\n1. One\n2. Two\n3. Three");
    expect(p?.heading).toBeNull();
    expect(p?.before).toBe("I will do this in order.");
  });

  test("two steps are not a plan", () => {
    expect(findPlan("## Plan\n\n1. Read the code\n2. Fix it")).toBeNull();
  });

  test("a list in a code fence is not a plan", () => {
    expect(findPlan("Run this:\n\n```\n1. one\n2. two\n3. three\n```")).toBeNull();
  });

  test("a list that starts past 1 is not a plan", () => {
    expect(findPlan("4. four\n5. five\n6. six")).toBeNull();
  });

  test("a short list first, then a plan", () => {
    const p = findPlan("Two notes:\n1. a\n2. b\n\nThen:\n1. x\n2. y\n3. z");
    expect(p?.heading).toBe("Then");
    expect(p?.steps).toEqual(["x", "y", "z"]);
    expect(p?.before).toBe("Two notes:\n1. a\n2. b");
  });

  test("a numbered list in an ordinary answer in ask mode is not a plan", () => {
    const answer = `The limiter fails for three reasons:

1. It counts failures globally.
2. It never prunes old entries.
3. Its window is a day long.`;
    expect(detectPlan(answer, { mode: "ask" })).toBeNull();
    expect(detectPlan(answer, { mode: "yolo" })).toBeNull();
    expect(detectPlan(answer, { mode: null })).toBeNull();
  });

  test("plan mode, or a reply the engine marks, makes a plan", () => {
    expect(detectPlan(CLAUDE_PLAN, { mode: "plan" })?.steps).toHaveLength(4);
    expect(detectPlan(CLAUDE_PLAN, { mode: "ask", marked: true })?.steps).toHaveLength(4);
  });

  test("plain prose in plan mode is not a plan", () => {
    expect(detectPlan("I need to know which branch you want before I plan.", { mode: "plan" })).toBeNull();
  });
});

test("planText numbers the steps for the composer", () => {
  expect(planText(["Read it", "Fix it\n- carefully", "Test it"])).toBe("1. Read it\n2. Fix it\n   - carefully\n3. Test it");
});

describe("the mode after a plan", () => {
  test("approve goes back to yolo only from yolo", () => {
    expect(approveMode("yolo")).toBe("yolo");
    expect(approveMode("ask")).toBe("ask");
    expect(approveMode(null)).toBe("ask");
  });

  test("the memory keeps the mode before plan", () => {
    let m = nextModeMemory(undefined, "yolo");
    expect(m.before).toBeNull();
    m = nextModeMemory(m, "plan");
    expect(m).toEqual({ mode: "plan", before: "yolo" });
    expect(nextModeMemory(m, "plan")).toBe(m);
    m = nextModeMemory(m, "ask");
    expect(m).toEqual({ mode: "ask", before: null });
    expect(nextModeMemory(nextModeMemory(undefined, "plan"), "plan").before).toBeNull();
  });
});
