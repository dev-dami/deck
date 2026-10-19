import { describe, expect, test } from "bun:test";
import type { PushMessage } from "../protocol3";
import type { ComputerRecord } from "../store/computers";
import { answerNotification, notificationActions, notificationRoute, parsePush } from "./messages";

const message: PushMessage = {
  cid: "computer-b",
  kind: "approval",
  pj: "project",
  session_id: "session",
  title: "Approval",
  body: "Run tests",
  approval_id: "approval",
  digest: "digest",
  tier: "normal",
  at: "2026-10-15T10:00:00Z",
};
const computer = (cid: string): ComputerRecord => ({
  cid,
  name: cid,
  relayUrl: "https://relay.example",
  lan: [],
  deviceId: "phone",
  relayToken: "token",
  createdAt: message.at,
  phoneKey: { pub: new Uint8Array(32), priv: {} as CryptoKey },
});

describe("push notifications", () => {
  test("malformed push data is rejected", () => {
    expect(parsePush({ kind: "approval" })).toBeNull();
    expect(parsePush(message)).toEqual(message);
  });
  test("destructive, done and unaddressed pushes offer no actions", () => {
    expect(notificationActions(message).map((a) => a.action)).toEqual(["approve", "deny"]);
    expect(notificationActions({ ...message, tier: "destructive" })).toEqual([]);
    expect(notificationActions({ ...message, kind: "done" })).toEqual([]);
    const { cid: _, ...legacy } = message;
    expect(notificationActions(legacy)).toEqual([]);
  });
  test("approval is queued only to the addressed computer with bound preconditions", async () => {
    const calls: unknown[] = [];
    const answered = await answerNotification(message, "approve", {
      list: async () => [computer("computer-a"), computer("computer-b")],
      queue: async (c, input) => {
        calls.push([c.cid, input]);
        return { qid: "qid", state: "sent", expiresAt: message.at };
      },
    });
    expect(answered).toBe(true);
    expect(calls).toEqual([
      [
        "computer-b",
        {
          kind: "approvals.answer",
          pj: "project",
          sessionId: "session",
          payload: { session_id: "session", id: "approval", decision: "allow" },
          precond: { approval_id: "approval", digest: "digest" },
          expiresInMs: 3_600_000,
        },
      ],
    ]);
  });
  test("never falls back to another paired computer or a revoked phone", async () => {
    let calls = 0;
    const queue = async () => {
      calls++;
      return { qid: "q", state: "sent" as const, expiresAt: message.at };
    };
    expect(
      await answerNotification(message, "deny", {
        list: async () => [computer("computer-a")],
        queue,
      }),
    ).toBe(false);
    expect(
      await answerNotification(message, "deny", {
        list: async () => [{ ...computer("computer-b"), relayToken: null }],
        queue,
      }),
    ).toBe(false);
    expect(
      await answerNotification({ ...message, tier: "destructive" }, "approve", { queue }),
    ).toBe(false);
    expect(calls).toBe(0);
  });
  test("routes encode project and session values", () => {
    expect(notificationRoute({ ...message, pj: "a/b", session_id: "s?" })).toBe(
      "#/c/computer-b/p/a%2Fb/s/s%3F",
    );
  });
});
