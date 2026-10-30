import { describe, expect, test } from "bun:test";
import { briefOf, isTop, nest } from "./subthreads";

interface T {
  id: string;
  parent: string;
  at: number;
}
const t = (id: string, parent = "", at = 0): T => ({ id, parent, at });
const id = (x: T) => x.id;
const parent = (x: T) => x.parent;
const newer = (a: T, b: T) => b.at - a.at;

describe("sub-threads in the list", () => {
  test("each parent's sub-threads follow it, newest first, a level deeper", () => {
    const all = [t("a"), t("b"), t("a1", "a", 1), t("a2", "a", 2), t("a2x", "a2", 3)];
    const ids = new Set(all.map(id));
    const tops = all.filter((x) => isTop(x, ids, parent));
    expect(nest(tops, all, id, parent, newer).map((n) => `${n.depth}:${n.item.id}`)).toEqual([
      "0:a",
      "1:a2",
      "2:a2x",
      "1:a1",
      "0:b",
    ]);
  });

  test("a sub-thread whose parent is not listed stands on its own", () => {
    const all = [t("a1", "gone"), t("b")];
    const ids = new Set(all.map(id));
    expect(all.filter((x) => isTop(x, ids, parent)).map(id)).toEqual(["a1", "b"]);
  });

  test("a cycle cannot loop", () => {
    const all = [t("a", "b"), t("b", "a")];
    expect(nest(all.slice(0, 1), all, id, parent, newer).map((n) => n.item.id)).toEqual(["a", "b"]);
  });
});

describe("a parent agent's brief", () => {
  const msg = (text: string, agent?: string) => ({
    role: "user",
    ...(agent === undefined ? {} : { agent }),
    parts: [{ type: "text", text }],
  });

  test("names its agent and thread, without Deck's line", () => {
    const b = briefOf(
      msg(
        '[deck] claude, the agent of the thread "Fix the \\"pairing\\" lockout", opened this sub-thread for you. Your final reply to this goes back to it; the user may also talk to you here.\n\nIs keying by address enough?',
        "claude",
      ),
    );
    expect(b).toEqual({
      from: "claude",
      thread: 'Fix the "pairing" lockout',
      body: "Is keying by address enough?",
      first: true,
    });
  });

  test("a follow-up is the agent's too", () => {
    expect(
      briefOf(
        msg(
          "[deck] From claude, the agent that opened this sub-thread:\n\nShorter, please.",
          "claude",
        ),
      ),
    ).toEqual({
      from: "claude",
      thread: "",
      body: "Shorter, please.",
      first: false,
    });
  });

  test("the person's own message is none", () => {
    expect(briefOf(msg("[deck] looks like a brief"))).toBeNull();
    expect(briefOf({ role: "assistant", agent: "claude", parts: [] })).toBeNull();
  });
});
