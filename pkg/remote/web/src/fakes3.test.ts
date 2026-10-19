import { describe, expect, test } from "bun:test";
import { FakeComputer3, fakeComputerRecord } from "./fakes3";

describe("fake computer v3 (B12)", () => {
  test("inbox grouping and approval binding", () => {
    const fake = new FakeComputer3(fakeComputerRecord());
    fake.setInbox([
      {
        pj: "p1",
        session_id: "s1",
        title: "Fix bug",
        state: "needs_you",
        approvals: 1,
        last_activity: "2026-10-14T01:00:00Z",
        summary: "needs approval",
        unread: true,
      },
      {
        pj: "p1",
        session_id: "s2",
        title: "Running job",
        state: "running",
        approvals: 0,
        last_activity: "2026-10-14T01:01:00Z",
        summary: "working",
        unread: false,
      },
    ]);
    expect(fake.needsYou().map((i) => i.session_id)).toEqual(["s1"]);
    fake.advertiseApproval("ap1", "digest1");
    expect(fake.answerApproval("ap1", "digest1")).toBe("applied");
    expect(fake.answerApproval("ap1", "other")).toBe("dropped");
    fake.withdrawApproval("ap1");
    expect(fake.answerApproval("ap1", "digest1")).toBe("dropped");
  });
});
