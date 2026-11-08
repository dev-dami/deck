import { describeError } from "@web/controller";
import { Duration, Effect, Schedule } from "effect";
import type { DeckBridge, RpcMethod } from "../shared/bridge";
import { type BridgeError, bridgeError, GRPC, RpcError } from "../shared/effect";

// One unary call to a local Deck through main, as an Effect: the Deck's
// answer, or why not. RpcError is the Deck's own refusal (its gRPC code
// and words); BridgeError is the bridge failing, which main does only for
// its own reasons (bad arguments, a closed window). main's gRPC deadline
// is the call's timeout.
export function rpcEffect(
  bridge: Pick<DeckBridge, "rpcCall">,
  deckId: string,
  method: RpcMethod,
  request: Record<string, unknown>,
): Effect.Effect<unknown, RpcError | BridgeError> {
  return Effect.tryPromise({ try: () => bridge.rpcCall(deckId, method, request), catch: bridgeError }).pipe(
    Effect.flatMap((r) => (r.ok ? Effect.succeed(r.value) : Effect.fail(new RpcError({ code: r.code, message: r.message })))),
  );
}

const BACKOFF_MAX_MS = 10_000;

// The waits before the event stream is asked for again after it ended:
// 500 ms, doubling each time, at most BACKOFF_MAX_MS (the shorter of the
// two schedules wins). A Ready starts it over.
export const RESUBSCRIBE: Schedule.Schedule<unknown> = Schedule.exponential(Duration.millis(500)).pipe(
  Schedule.union(Schedule.spaced(Duration.millis(BACKOFF_MAX_MS))),
);

// What the window says of a failed call; refused when the Deck gave no words.
export function callMessage(e: RpcError | BridgeError, refused: string): string {
  if (e._tag === "BridgeError") return describeError(e.cause);
  return e.code === GRPC.UNAVAILABLE ? "Deck is not reachable." : e.message || refused;
}
