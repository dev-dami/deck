import { Cause, Data, Deferred, Duration, Effect, Exit, Fiber, FiberId, Schedule } from "effect";

// The app's async work is written as Effects (see docs/effect.md). Modules
// keep their Promise APIs at the edge: callers, the IPC bridge and the UI
// see the same functions they always did, and these helpers run the
// Effect behind each one.

// Runs an effect that needs no services. It settles like a plain async
// function would: with the value, or rejected with the error the effect
// failed with (or the defect it died of), not Effect's FiberFailure
// wrapper, so `instanceof` and `.code` checks in callers keep working.
export function runPromise<A, E>(effect: Effect.Effect<A, E>, options?: { readonly signal?: AbortSignal }): Promise<A> {
  return Effect.runPromiseExit(effect, options).then((exit) => {
    if (Exit.isSuccess(exit)) return exit.value;
    throw Cause.squash(exit.cause);
  });
}

// Starts an effect in the background. Its fiber is the handle that stops
// it: interrupt() runs its finalizers (timers cleared, sockets closed).
export function runFork<A, E>(effect: Effect.Effect<A, E>): Fiber.RuntimeFiber<A, E> {
  return Effect.runFork(effect);
}

// Interrupts a background fiber without waiting for it to wind down.
export function interrupt(fiber: Fiber.Fiber<unknown, unknown> | null | undefined): void {
  if (fiber != null) Effect.runFork(Fiber.interrupt(fiber));
}

// Starts listening at once and hands back what is heard as a Deferred to
// await. Effect.async registers its listeners when its fiber first runs,
// and a fiber that a timeout or a race forks runs a microtask later: after
// the events Node emits on process.nextTick (a spawn's ENOENT, some socket
// errors), which would then go unheard, or crash the app as an unhandled
// 'error'. So an emitter made in this tick is listened to with this, in the
// same tick; done settles it once, later calls are ignored.
export function listenNow<A, E = never>(register: (done: (result: Effect.Effect<A, E>) => void) => void): Deferred.Deferred<A, E> {
  const d = Deferred.unsafeMake<A, E>(FiberId.none);
  register((result) => Deferred.unsafeDone(d, result));
  return d;
}

// A sleep whose timer does not keep Node alive (setTimeout(...).unref()),
// for fallbacks such as "kill the engine if it has not exited in 5 s"
// that must not hold up the app's exit. It does not follow TestClock, so
// prefer Effect.sleep everywhere else.
export function unrefSleep(duration: Duration.DurationInput): Effect.Effect<void> {
  const ms = Duration.toMillis(duration);
  return Effect.async<void>((resume) => {
    const timer = setTimeout(() => resume(Effect.void), ms);
    (timer as { unref?: () => void }).unref?.();
    return Effect.sync(() => clearTimeout(timer));
  });
}

// Runs step, sleeps for the delay it returns, and again, until the fiber
// running it is interrupted. The first step runs at once.
export function pollLoop(step: Effect.Effect<Duration.DurationInput>): Effect.Effect<never> {
  return Effect.forever(step.pipe(Effect.flatMap((delay) => Effect.sleep(delay))));
}

// Retries of something that fails by event rather than by return value (a
// stream that ended): after(f) runs f once the schedule's next delay has
// passed, reset() starts the schedule over (the stream came back), and
// cancel() drops a retry that is waiting. A new after() replaces one that
// is waiting.
export class Backoff {
  private readonly driver: Schedule.ScheduleDriver<unknown, unknown, never>;
  private fiber: Fiber.RuntimeFiber<void, unknown> | null = null;
  // Which after() is the latest.
  private turn = 0;

  constructor(schedule: Schedule.Schedule<unknown, unknown>) {
    this.driver = Effect.runSync(Schedule.driver(schedule));
  }

  after(f: () => void): void {
    this.cancel();
    const turn = ++this.turn;
    const fiber = runFork(
      this.driver.next(undefined).pipe(
        Effect.andThen(() => {
          if (this.turn === turn) this.fiber = null;
          f();
        }),
        // A schedule that ran out retries no more.
        Effect.ignore,
      ),
    );
    // The schedule may have run out (or f ran) before runFork returned.
    if (fiber.unsafePoll() === null) this.fiber = fiber;
  }

  reset(): void {
    Effect.runSync(this.driver.reset);
  }

  cancel(): void {
    interrupt(this.fiber);
    this.fiber = null;
  }
}

// A gRPC call that failed: its status code and the server's words. Main
// turns it into RpcResult's { ok: false, code, message } at the bridge;
// the renderer turns that back into this error.
export class RpcError extends Data.TaggedError("RpcError")<{
  readonly code: number;
  readonly message: string;
}> {}

// The IPC bridge itself rejected (main threw, or the arguments were
// refused), as opposed to the Deck answering with an error.
export class BridgeError extends Data.TaggedError("BridgeError")<{
  readonly message: string;
  readonly cause: unknown;
}> {}

export function bridgeError(cause: unknown): BridgeError {
  return new BridgeError({ message: cause instanceof Error ? cause.message : String(cause), cause });
}

// gRPC status codes the app names.
export const GRPC = { CANCELLED: 1, RESOURCE_EXHAUSTED: 8, UNIMPLEMENTED: 12, UNAVAILABLE: 14 } as const;
