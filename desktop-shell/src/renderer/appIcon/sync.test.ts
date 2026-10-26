import { expect, test } from "vitest";
import { iconSync } from "./sync";

// A show that waits until the test lets it finish.
function harness() {
  let want: string | null = null;
  const shown: string[] = [];
  const errors: unknown[] = [];
  const pending: { id: string; done: () => void; fail: (e: Error) => void }[] = [];
  const update = iconSync({
    want: () => want,
    show: (id) =>
      new Promise<void>((done, fail) => {
        pending.push({ id, done, fail });
      }).then(() => void shown.push(id)),
    onError: (e) => errors.push(e),
  });
  const settle = () => new Promise((r) => setTimeout(r, 0));
  return {
    update,
    set: (id: string | null) => {
      want = id;
      update();
    },
    pending,
    shown,
    errors,
    settle,
  };
}

test("shows nothing before the theme is known, then the wanted icon once", async () => {
  const h = harness();
  h.update();
  expect(h.pending).toHaveLength(0);
  h.set("glass");
  h.update();
  h.set("glass");
  expect(h.pending.map((p) => p.id)).toEqual(["glass"]);
  h.pending[0]?.done();
  await h.settle();
  h.update();
  expect(h.shown).toEqual(["glass"]);
  expect(h.pending).toHaveLength(1);
});

test("changes while an icon is drawn wait, and only the latest is drawn next", async () => {
  const h = harness();
  h.set("glass");
  h.set("nord");
  h.set("dracula");
  h.set("riso");
  expect(h.pending.map((p) => p.id)).toEqual(["glass"]);
  h.pending[0]?.done();
  await h.settle();
  expect(h.pending.map((p) => p.id)).toEqual(["glass", "riso"]);
  h.pending[1]?.done();
  await h.settle();
  expect(h.shown).toEqual(["glass", "riso"]);
  // Back to one already shown since: drawn again.
  h.set("glass");
  expect(h.pending.map((p) => p.id)).toEqual(["glass", "riso", "glass"]);
});

test("a failed icon is reported and not retried until the choice changes", async () => {
  const h = harness();
  h.set("stitch");
  h.pending[0]?.fail(new Error("no 2d context"));
  await h.settle();
  expect(h.errors).toEqual([new Error("no 2d context")]);
  h.update();
  expect(h.pending).toHaveLength(1);
  h.set("classic");
  expect(h.pending.map((p) => p.id)).toEqual(["stitch", "classic"]);
});
