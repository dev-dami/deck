# Async work with Effect

The desktop shell writes its async work as [Effect](https://effect.website)
programs (the `effect` package, 3.x): starting and stopping the engine,
gRPC calls, the page control's CDP ops, probes, pairing, and the renderer's
RPC calls, retries and polls. This page says how that code is laid out and
how to add to it.

## The shape

Each module keeps the Promise API it always had. Callers, the IPC bridge,
React and the tests see the same functions; behind each one is an Effect,
run at the edge:

```ts
// The Effect: typed failure, composable.
export function unaryEffect(client, method, request, timeoutMs): Effect.Effect<unknown, RpcError> { ... }

// The edge: the Promise callers had before.
export function unary(client, method, request, timeoutMs): Promise<RpcResult> {
  return runPromise(Effect.match(unaryEffect(client, method, request, timeoutMs), { onFailure, onSuccess }));
}
```

Inside a module, compose the Effect versions (`yield*` them in
`Effect.gen`); only convert to a Promise where a caller needs one.

## The runtime helpers: `src/shared/effect.ts`

Main and the renderer share one small module. No layers or services:
everything runs on the default runtime.

| Helper | What it is for |
| --- | --- |
| `runPromise(effect)` | Runs an effect and settles like an async function. A failure rejects with **the error itself** (Effect's own `runPromise` wraps it in a `FiberFailure`), so `instanceof PageError`, `.code` and `toThrow(/…/)` keep working. |
| `runFork(effect)` / `interrupt(fiber)` | Background work with a handle: a poll, a retry, a deadline. `interrupt` stops it and runs its finalizers. Use this instead of `setTimeout` ids and `stopped` flags. |
| `pollLoop(step)` | Runs `step`, sleeps the delay it returns, forever. The limits, crew and work polls are this. |
| `Backoff` | Retries driven by events rather than a failing effect (the event stream "ended"): `after(f)`, `reset()`, `cancel()` over a `Schedule`. |
| `listenNow(register)` | Listens to an emitter in the current tick and hands back a `Deferred`. See "Emitters" below. |
| `unrefSleep(d)` | A sleep whose timer does not keep Node alive, for "kill it if it has not exited in 5 s" fallbacks. It ignores `TestClock`; prefer `Effect.sleep`. |
| `RpcError`, `BridgeError`, `GRPC` | The shared error types and status codes (below). |

## Error types

Errors are `Data.TaggedError` classes, so they are real `Error`s with a
`_tag` to match on (`Effect.catchTag`, `e._tag === "…"`).

| Error | Where | Meaning |
| --- | --- | --- |
| `RpcError { code, message }` | shared | A gRPC call failed: its status and the engine's words. Main's `unaryEffect` fails with it; the renderer's `rpcEffect` turns `{ ok: false }` back into it. |
| `BridgeError { message, cause }` | shared | The IPC bridge rejected (bad arguments, main threw), as opposed to the Deck refusing. |
| `StartRefused`, `HandshakeFailed { reason }`, `StartFailed { start }` | `main/decks.ts` | Why a Deck did not start: nothing spawned; no handshake (`spawn`, `exit` or `timeout`); failed after spawning, with the stderr tail for the window. |
| `PageError { code }`, `RefError` | `main/pageControl.ts` | An op the agent asked for cannot be done; `code` is what it is told. `RefError` means "take a snapshot". |
| `PinError`, `ConnectTimeout`, `SocketError` | `main/pin.ts` | A pinned connection failed. `connectPinned` still rejects with Node's own error for `SocketError`, so `.code === "ECONNREFUSED"` holds. |
| `PairFailed { retryAfterMs }` | `main/pair.ts` | Pairing failed, in the window's words. |
| `PhoneFailed { needsRelayDir }` | `main/phone.ts` | `deck mobile connect` did not do what was asked. |

Failures the caller shows as data (`{ ok: false, message }`, a string) are
mapped with `Effect.match` at the edge. Failures that reject keep their
class.

## Patterns in use

- **Timeouts**: `Effect.timeoutFail({ duration, onTimeout })` for a typed
  timeout (the engine's 60 s handshake, a pinned connect, the pair answer,
  `PageControl.waitFor`); `Effect.timeoutTo` when a timeout is an answer
  (`listening` is false, `whenLoaded` is false). A timeout interrupts the
  work, so its cleanup runs.
- **Resources**: `Effect.acquireRelease` in a `Scope` for the engine process
  and its gRPC client while it starts (`RpcDecks.startEffect`): on failure
  or interruption they are let go, on success handed on.
  `Effect.acquireUseRelease` for a probe's socket, an icon's file handle and
  a CDP element handle (`PageControl.withElement`).
- **Cancellation**: `Effect.async` returns its canceller (a gRPC call's
  `cancel()`, a socket's `destroy()`, `execFile`'s kill). Background loops
  are fibers, stopped with `interrupt`; the browser's execution deadline is
  a fiber that `approve` interrupts while a person decides.
- **Retries**: `Schedule`. The event stream resubscribes on `RESUBSCRIBE`
  (`renderer/rpcEffect.ts`): 500 ms doubling, capped at 10 s by
  `Schedule.union` with `Schedule.spaced`. `PageControl.waitFor` polls with
  `Effect.repeat({ schedule: Schedule.spaced(pollMs), until })`.
- **Concurrency**: `Effect.forEach(…, { concurrency: "unbounded" })` for the
  port probes and a crew round.

Unary RPCs are not retried automatically: `Send`, `Answer` and most
mutations have no idempotency key, so a retry could act twice. main's gRPC
deadline is each call's timeout.

## Emitters: listen in the same tick

`Effect.async` registers its listeners when its fiber first runs, and a
fiber forked by a timeout or a race runs a microtask later. Node emits some
events on `process.nextTick`, before that: a spawn's `ENOENT`, some socket
errors. Unheard, an `'error'` event crashes the main process. So when an
emitter is created and then raced against a timeout, create it and listen to
it in one synchronous step, and await the result:

```ts
const { child, heard } = yield* Effect.acquireRelease(
  Effect.sync(() => {
    const child = spawn(deck, ["serve", "--rpc"], opts);
    return { child, heard: listenForHandshake(child, tail, onLine) }; // listenNow inside
  }),
  release,
);
const hs = yield* awaitHandshake(heard); // Deferred.await(heard).pipe(Effect.timeoutFail(…))
```

A plain `Effect.async` with no timeout or race around it registers at once
and needs none of this (`unaryEffect`, `execRunEffect`).

## Adding an effect

1. Write the Effect version next to the code it replaces, named
   `fooEffect` (or private), returning `Effect.Effect<A, E>` with a tagged
   error for each way it fails that a caller treats differently.
2. Wrap callbacks with `Effect.async` (return the canceller), promises with
   `Effect.tryPromise({ try, catch })` (map the cause to your error), and
   sync throwing code with `Effect.try`.
3. Keep the exported function's signature; implement it as
   `runPromise(...)` of the effect, mapping failures with `Effect.match`
   where the old function returned them as data.
4. For background work, keep the fiber from `runFork` and `interrupt` it
   where the old code cleared a timer.
5. Test it (below).

## Testing

Effects are tested in vitest like any Promise code, and two Effect tools
make time-dependent code fast and exact:

```ts
import { Duration, Effect, Either, Fiber, TestClock, TestContext } from "effect";

test("gives up after the start timeout", async () => {
  const r = await Effect.runPromise(
    Effect.gen(function* () {
      const fiber = yield* Effect.fork(Effect.either(readHandshake(child, tail, onLine, 60_000)));
      yield* TestClock.adjust(Duration.seconds(60)); // no real time passes
      return yield* Fiber.join(fiber);
    }).pipe(Effect.provide(TestContext.TestContext)),
  );
  expect(Either.isLeft(r) && r.left).toMatchObject({ reason: "timeout" });
});
```

- `Effect.either` turns a failure into a value to assert on, typed.
- `TestClock` drives everything built on `Effect.sleep`, `Clock`,
  `Schedule` and `Effect.timeout*`: see `main/decks.test.ts`,
  `main/pageControl.test.ts` ("waiting, on the test clock") and
  `renderer/rpcEffect.test.ts` (`RESUBSCRIBE`'s delays). `unrefSleep` uses
  a real timer, so tests of it use short real delays.
- The Promise APIs are tested as before; `src/shared/effect.test.ts` checks
  that `runPromise` rejects with the original error.

## What is not an Effect, and why

- **gRPC event and terminal streams** (`RpcDecks.subscribe`,
  `termSubscribe`, the browser controller's `Events`): long-lived push
  streams forwarded to the window event by event, owned by identity checks.
  Wrapping them in `Stream` would only re-wrap the same callbacks.
- **`Browser.execute`**: each op's cancellation is an `AbortController`
  shared with the tab (a takeover, a debugger detach, `closeDeck` abort
  every op on it) and carried into the CDP channel. Its awaits go through
  `abortable` (an Effect race with the signal), its deadline is a fiber, and
  `whenLoaded`, `follow` and the sweeper are Effects; replacing the
  controller with fiber interruption needs a redesign of the tab state.
  `tabs.ts`'s `Gate` stays: it is strictly FIFO, which Effect's semaphore
  does not promise.
- **`BrowserUpdate` ordering** stays a promise chain for the same reason:
  updates must reach the engine in the order they were made.
- **UI timers** (the archive undo, the completion debounce, notification
  batching, `useNow`, window-state saves): they take injected timers that
  their tests fake, and do no async work of their own.
- **The phone client's socket** (`link.ts`, `bridgeSocket.ts`) and the
  smoke run in `main.ts`: event plumbing with little to gain.
