import { describe, expect, test } from "vitest";
import { conflictFromStatus } from "./artifactConflict";

const enc = new TextEncoder();
const varint = (n: number): number[] => {
  const out: number[] = [];
  let v = n;
  while (v >= 0x80) {
    out.push((v & 0x7f) | 0x80);
    v = Math.floor(v / 128);
  }
  out.push(v);
  return out;
};
const str = (num: number, s: string): number[] => [(num << 3) | 2, ...varint(s.length), ...enc.encode(s)];
const num = (n: number, v: number): number[] => [(n << 3) | 0, ...varint(v)];
const bytes = (n: number, b: number[]): number[] => [(n << 3) | 2, ...varint(b.length), ...b];

const conflict = (reason: string) => [...str(1, "art-1"), ...num(2, 3), ...num(3, 300), ...str(4, reason)];
const status = (typeUrl: string, value: number[]) =>
  new Uint8Array([...num(1, 10), ...str(2, "artifact revision conflict"), ...bytes(3, [...str(1, typeUrl), ...bytes(2, value)])]);

describe("conflictFromStatus", () => {
  test("reads the conflict out of a status", () => {
    expect(conflictFromStatus(status("type.googleapis.com/deck.v1.ArtifactConflict", conflict("stale_revision")))).toEqual({
      id: "art-1",
      baseRevision: 3,
      currentRevision: 300,
      reason: "stale_revision",
    });
  });

  test("ignores other details, unknown reasons and garbage", () => {
    expect(conflictFromStatus(status("type.googleapis.com/other.Thing", conflict("stale_revision")))).toBeNull();
    expect(conflictFromStatus(status("type.googleapis.com/deck.v1.ArtifactConflict", conflict("because")))).toBeNull();
    expect(conflictFromStatus(new Uint8Array([0xff, 0xff, 0xff]))).toBeNull();
    expect(conflictFromStatus(new Uint8Array([]))).toBeNull();
    expect(conflictFromStatus(new Uint8Array([(3 << 3) | 2, 50, 1]))).toBeNull();
  });
});
