import { Duration, Effect, Either, Schedule, TestClock, TestContext } from "effect";
import { describe, expect, test } from "vitest";
import type { RpcResult } from "../shared/bridge";
import { BridgeError, RpcError } from "../shared/effect";
import { callMessage, RESUBSCRIBE, rpcEffect } from "./rpcEffect";

const bridge = (answer: () => Promise<RpcResult>) => ({ rpcCall: answer });

describe("rpcEffect", () => {
  test("a Deck's answer is the value", async () => {
    const out = await Effect.runPromise(rpcEffect(bridge(async () => ({ ok: true, value: { n: 1 } })), "local-1", "Info", {}));
    expect(out).toEqual({ n: 1 });
  });

  test("a Deck's refusal is an RpcError, the bridge's own failure a BridgeError", async () => {
    const refused = await Effect.runPromise(
      Effect.either(rpcEffect(bridge(async () => ({ ok: false, code: 9, message: "not now" })), "local-1", "Send", {})),
    );
    expect(Either.isLeft(refused) && refused.left).toBeInstanceOf(RpcError);
    expect(Either.isLeft(refused) && refused.left).toMatchObject({ code: 9, message: "not now" });
    const broken = await Effect.runPromise(
      Effect.either(
        rpcEffect(
          bridge(() => Promise.reject(new Error("bad arguments for rpcCall"))),
          "local-1",
          "Send",
          {},
        ),
      ),
    );
    expect(Either.isLeft(broken) && broken.left).toBeInstanceOf(BridgeError);
    expect(Either.isLeft(broken) && broken.left.message).toBe("bad arguments for rpcCall");
  });

  test("the window's words for each failure", () => {
    expect(callMessage(new RpcError({ code: 14, message: "connect ENOENT" }), "x")).toBe("Deck is not reachable.");
    expect(callMessage(new RpcError({ code: 9, message: "busy" }), "x")).toBe("busy");
    expect(callMessage(new RpcError({ code: 9, message: "" }), "Deck refused Send.")).toBe("Deck refused Send.");
  });
});

describe("RESUBSCRIBE", () => {
  test("waits 500 ms, doubling, and never more than 10 s", async () => {
    const delays = await Effect.runPromise(
      Effect.gen(function* () {
        const driver = yield* Schedule.driver(RESUBSCRIBE);
        const out: number[] = [];
        for (let i = 0; i < 7; i++) {
          // next() sleeps on the clock; step the TestClock until it wakes.
          const step = yield* Effect.fork(driver.next(undefined));
          let waited = 0;
          while ((yield* step.poll)._tag === "None") {
            yield* TestClock.adjust(Duration.millis(250));
            waited += 250;
          }
          out.push(waited);
        }
        return out;
      }).pipe(Effect.provide(TestContext.TestContext)),
    );
    expect(delays).toEqual([500, 1000, 2000, 4000, 8000, 10_000, 10_000]);
  });
});
