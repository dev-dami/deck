import { type ChildProcess, spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import { Duration, Effect, Either, Fiber, TestClock, TestContext } from "effect";
import { describe, expect, test, vi } from "vitest";
import { RpcError } from "../shared/effect";
import { HandshakeFailed, readHandshake, stopChild, type Tail, unaryEffect } from "./decks";

const TOKEN = "ab".repeat(32);
const HANDSHAKE = JSON.stringify({ v: 1, sock: "/tmp/deck-1.sock", token: TOKEN, pid: 7, version: "1.2.3" });

// A child process with pipes, as far as the start code looks at one.
function fakeChild() {
  const c = Object.assign(new EventEmitter(), {
    stdout: new PassThrough(),
    stderr: new PassThrough(),
    stdin: { end: vi.fn() },
    kill: vi.fn(() => true),
    exitCode: null as number | null,
    signalCode: null as string | null,
  });
  return c;
}
const asChild = (c: ReturnType<typeof fakeChild>) => c as unknown as ChildProcess;
const tick = () => new Promise((r) => setImmediate(r));

describe("readHandshake", () => {
  test("reads the handshake line among others, and passes stderr lines on", async () => {
    const c = fakeChild();
    const lines: string[] = [];
    const out = Effect.runPromise(readHandshake(asChild(c), { text: "" }, (l) => lines.push(l)));
    c.stderr.write("starting\n");
    c.stdout.write("Deck is starting\n");
    c.stdout.write(`${HANDSHAKE}\n`);
    await expect(out).resolves.toMatchObject({ sock: "/tmp/deck-1.sock", version: "1.2.3" });
    expect(lines).toEqual(["starting"]);
  });

  test("a Deck that exits first fails with its own error line", async () => {
    const c = fakeChild();
    const tail: Tail = { text: "" };
    const out = Effect.runPromise(Effect.either(readHandshake(asChild(c), tail, () => {})));
    c.stderr.write("Error: none of claude, codex was found on PATH\nUsage:\n  deck serve [flags]\n");
    await tick();
    c.emit("exit", 1);
    const r = await out;
    expect(Either.isLeft(r) && r.left).toBeInstanceOf(HandshakeFailed);
    expect(Either.isLeft(r) && r.left).toMatchObject({
      reason: "exit",
      message: "Deck stopped before it was ready: none of claude, codex was found on PATH",
    });
  });

  test("a Deck that cannot run says why", async () => {
    const c = fakeChild();
    const out = Effect.runPromise(Effect.either(readHandshake(asChild(c), { text: "" }, () => {})));
    c.emit("error", new Error("spawn deck ENOENT"));
    const r = await out;
    expect(Either.isLeft(r) && r.left).toMatchObject({ reason: "spawn", message: "Could not run deck: spawn deck ENOENT" });
  });

  test("a missing binary's spawn error, which Node emits on the next tick, is heard", async () => {
    const child = spawn(join(tmpdir(), "no-such-deck-binary"), ["serve", "--rpc"], { stdio: ["pipe", "pipe", "pipe"] });
    const r = await Effect.runPromise(Effect.either(readHandshake(child, { text: "" }, () => {})));
    expect(Either.isLeft(r) && r.left).toMatchObject({ reason: "spawn", message: expect.stringMatching(/^Could not run deck: .*ENOENT/) });
  });

  test("gives up after the start timeout (on the test clock)", async () => {
    const c = fakeChild();
    const r = await Effect.runPromise(
      Effect.gen(function* () {
        const fiber = yield* Effect.fork(Effect.either(readHandshake(asChild(c), { text: "" }, () => {}, 60_000)));
        yield* TestClock.adjust(Duration.seconds(59));
        expect(fiber.unsafePoll()).toBeNull();
        yield* TestClock.adjust(Duration.seconds(1));
        return yield* Fiber.join(fiber);
      }).pipe(Effect.provide(TestContext.TestContext)),
    );
    expect(Either.isLeft(r) && r.left).toMatchObject({ reason: "timeout", message: "Deck did not start in time." });
  });
});

describe("stopChild", () => {
  test("closes stdin and does not kill a Deck that exits", async () => {
    const c = fakeChild();
    const done = Effect.runPromise(stopChild(asChild(c), 50));
    expect(c.stdin.end).toHaveBeenCalledOnce();
    c.emit("exit", 0);
    await done;
    await new Promise((r) => setTimeout(r, 80));
    expect(c.kill).not.toHaveBeenCalled();
    expect(c.listenerCount("exit")).toBe(0);
  });

  test("kills a Deck that does not exit in time", async () => {
    const c = fakeChild();
    await Effect.runPromise(stopChild(asChild(c), 20));
    expect(c.kill).toHaveBeenCalledWith("SIGTERM");
    expect(c.listenerCount("exit")).toBe(0);
  });

  test("a Deck that already exited is left alone", async () => {
    const c = fakeChild();
    c.exitCode = 0;
    await Effect.runPromise(stopChild(asChild(c), 20));
    expect(c.kill).not.toHaveBeenCalled();
  });
});

describe("unaryEffect", () => {
  type Cb = (err: unknown, value: unknown) => void;
  const client = (impl: (req: unknown, opts: { deadline: Date }, cb: Cb) => unknown) =>
    ({ Info: impl }) as unknown as Parameters<typeof unaryEffect>[0];

  test("the answer, or the call's status as an RpcError", async () => {
    await expect(Effect.runPromise(unaryEffect(client((_r, _o, cb) => cb(null, { ok: 1 })), "Info", {}))).resolves.toEqual({ ok: 1 });
    const r = await Effect.runPromise(
      Effect.either(unaryEffect(client((_r, _o, cb) => cb({ code: 14, details: "connect ENOENT", message: "14 UNAVAILABLE" }, null)), "Info", {})),
    );
    expect(Either.isLeft(r) && r.left).toBeInstanceOf(RpcError);
    expect(Either.isLeft(r) && r.left).toMatchObject({ code: 14, message: "connect ENOENT" });
  });

  test("a method the service lacks is UNIMPLEMENTED", async () => {
    const r = await Effect.runPromise(Effect.either(unaryEffect(client(() => {}), "Nope", {})));
    expect(Either.isLeft(r) && r.left).toMatchObject({ code: 12, message: "no method Nope" });
  });

  test("the deadline is the timeout, and interrupting cancels the call", async () => {
    const cancel = vi.fn();
    let deadline = 0;
    const fiber = Effect.runFork(
      unaryEffect(
        client((_r, o) => {
          deadline = o.deadline.getTime() - Date.now();
          return { cancel };
        }),
        "Info",
        {},
        15_000,
      ),
    );
    expect(deadline).toBeGreaterThan(14_000);
    await Effect.runPromise(Fiber.interrupt(fiber));
    expect(cancel).toHaveBeenCalledOnce();
  });
});
