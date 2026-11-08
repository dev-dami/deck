import { Effect, Schedule } from "effect";
import { describe, expect, test } from "vitest";
import { Backoff, pollLoop, RpcError, runFork, runPromise, interrupt } from "./effect";

class Odd extends Error {
  readonly code = "odd";
}

const later = (ms: number) => new Promise((r) => setTimeout(r, ms));

describe("runPromise", () => {
  test("resolves with the value", async () => {
    await expect(runPromise(Effect.succeed(3))).resolves.toBe(3);
  });

  test("rejects with the error the effect failed with, not a wrapper", async () => {
    const err = await runPromise(Effect.fail(new Odd("no"))).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Odd);
    expect((err as Odd).code).toBe("odd");
    const rpc = await runPromise(Effect.fail(new RpcError({ code: 14, message: "gone" }))).catch((e: unknown) => e);
    expect(rpc).toBeInstanceOf(RpcError);
    expect(rpc).toMatchObject({ _tag: "RpcError", code: 14, message: "gone" });
  });

  test("a thrown defect rejects with what was thrown", async () => {
    const boom = new Error("boom");
    await expect(
      runPromise(
        Effect.sync(() => {
          throw boom;
        }),
      ),
    ).rejects.toBe(boom);
  });
});

describe("pollLoop", () => {
  test("polls at once, then after each delay, until interrupted", async () => {
    let n = 0;
    const fiber = runFork(pollLoop(Effect.sync(() => (n++, 10))));
    // The first poll runs before runFork returns.
    expect(n).toBe(1);
    await later(55);
    interrupt(fiber);
    const seen = n;
    expect(seen).toBeGreaterThanOrEqual(3);
    await later(40);
    expect(n).toBe(seen);
  });
});

describe("Backoff", () => {
  test("waits longer each time, starts over on reset, and cancel drops a waiting retry", async () => {
    const b = new Backoff(Schedule.exponential(10));
    const at: number[] = [];
    const t0 = Date.now();
    const go = () =>
      new Promise<void>((done) =>
        b.after(() => {
          at.push(Date.now() - t0);
          done();
        }),
      );
    await go(); // ~10 ms
    await go(); // ~20 ms more
    await go(); // ~40 ms more
    expect(at[1]! - at[0]!).toBeGreaterThanOrEqual(15);
    expect(at[2]! - at[1]!).toBeGreaterThanOrEqual(35);
    b.reset();
    const before = Date.now();
    await go();
    expect(Date.now() - before).toBeLessThan(35);
    let ran = false;
    b.after(() => {
      ran = true;
    });
    b.cancel();
    await later(60);
    expect(ran).toBe(false);
  });

  test("a schedule with no delay retries at once, and one that ran out retries no more", () => {
    const now = new Backoff(Schedule.forever);
    let n = 0;
    now.after(() => n++);
    expect(n).toBe(1);
    const once = new Backoff(Schedule.recurs(1));
    once.after(() => n++);
    once.after(() => n++);
    expect(n).toBe(2);
  });

  test("a new retry replaces the one waiting", async () => {
    const b = new Backoff(Schedule.spaced(20));
    const calls: string[] = [];
    b.after(() => calls.push("first"));
    b.after(() => calls.push("second"));
    await later(60);
    expect(calls).toEqual(["second"]);
  });
});
