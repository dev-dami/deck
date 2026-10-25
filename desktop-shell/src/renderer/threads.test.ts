import type { Session } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { splitSettled } from "./threads";
import { formatDuration, splitWork } from "./work";

const now = Date.parse("2026-10-19T12:00:00Z");
const s = (id: string, updated: string, extra: Partial<Session> = {}): Session => ({
  id,
  title: id,
  updated_at: updated,
  busy: false,
  pending: 0,
  mode: "ask",
  ...extra,
});

describe("settled threads", () => {
  test("old quiet threads settle; open, busy, waiting or unread ones stay", () => {
    const old = "2026-10-01T00:00:00Z";
    const out = splitSettled(
      [
        s("fresh", "2026-10-19T11:00:00Z"),
        s("old", old),
        s("busy", old, { busy: true }),
        s("waits", old, { pending: 2 }),
        s("news", old),
        s("open", old),
      ],
      now,
      "open",
      { news: true },
    );
    expect(out.live.map((x) => x.id)).toEqual(["fresh", "busy", "waits", "news", "open"]);
    expect(out.settled.map((x) => x.id)).toEqual(["old"]);
  });
});

describe("work fold", () => {
  test("the text after the last tool stays; the rest folds", () => {
    const tool = { k: "tool", call: null, result: null } as const;
    const text = (t: string) => ({ k: "text", text: t, truncated: false }) as const;
    const r = splitWork([text("looking"), tool, text("found it"), text("done")]);
    expect(r.work).toHaveLength(2);
    expect(r.answer.map((c) => (c.k === "text" ? c.text : ""))).toEqual(["found it", "done"]);
    expect(splitWork([text("hi")]).work).toEqual([]);
  });

  test("durations read like T3's", () => {
    expect(formatDuration(45_000)).toBe("45s");
    expect(formatDuration(223_000)).toBe("3m 43s");
    expect(formatDuration(3_720_000)).toBe("1h 2m");
  });
});
