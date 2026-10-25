import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { ARCHIVE_UNDO_MS, ArchiveQueue } from "./archive";

describe("archive with Undo", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  test("Deck is asked once the Undo time is over, and the room waits until it answers", async () => {
    const q = new ArchiveQueue();
    const calls: string[] = [];
    let answer = () => {};
    q.schedule("folder:/a/s1", () => {
      calls.push("s1");
      return new Promise<void>((r) => {
        answer = r;
      });
    });
    expect(q.snapshot()).toEqual(new Set(["folder:/a/s1"]));
    vi.advanceTimersByTime(ARCHIVE_UNDO_MS - 1);
    expect(calls).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(calls).toEqual(["s1"]);
    // Too late for Undo: the call went.
    expect(q.undo("folder:/a/s1")).toBe(false);
    expect(q.snapshot().has("folder:/a/s1")).toBe(true);
    answer();
    await vi.waitFor(() => expect(q.snapshot().size).toBe(0));
  });

  test("a Deck that can unarchive archives at once, and Undo unarchives after the archive", async () => {
    const q = new ArchiveQueue();
    const calls: string[] = [];
    let answer = () => {};
    q.schedule(
      "k",
      () => {
        calls.push("archive");
        return new Promise<void>((r) => {
          answer = r;
        });
      },
      async () => {
        calls.push("unarchive");
      },
    );
    expect(calls).toEqual(["archive"]);
    expect(q.snapshot().has("k")).toBe(true);
    // Undo while Deck still archives: the unarchive waits for its answer.
    expect(q.undo("k")).toBe(true);
    await Promise.resolve();
    expect(calls).toEqual(["archive"]);
    answer();
    await vi.waitFor(() => expect(calls).toEqual(["archive", "unarchive"]));
    expect(q.snapshot().size).toBe(0);
    expect(q.undo("k")).toBe(false);
  });

  test("Undo in time cancels the call", () => {
    const q = new ArchiveQueue();
    const archive = vi.fn(async () => {});
    const seen: number[] = [];
    q.subscribe(() => seen.push(q.snapshot().size));
    q.schedule("k", archive);
    // A second archive of the same room does not start another wait.
    q.schedule("k", archive);
    vi.advanceTimersByTime(ARCHIVE_UNDO_MS - 1);
    expect(q.undo("k")).toBe(true);
    vi.advanceTimersByTime(ARCHIVE_UNDO_MS);
    expect(archive).not.toHaveBeenCalled();
    expect(seen).toEqual([1, 0]);
    expect(q.undo("k")).toBe(false);
  });
});
