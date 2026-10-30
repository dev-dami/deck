import { describe, expect, test } from "bun:test";
import {
  blobBase64,
  blockedVideo,
  checkVideo,
  cleanLabel,
  defaultLabel,
  MAX_LABEL,
  MAX_VIDEO_BYTES_REMOTE,
  videoGate,
} from "./images";

describe("attachment labels", () => {
  test("a paste is a screenshot, a file its name, else its place", () => {
    expect(defaultLabel("image", "paste", "image.png", [])).toBe("Screenshot 1");
    expect(defaultLabel("image", "paste", "image.png", ["Screenshot 1"])).toBe("Screenshot 2");
    expect(defaultLabel("image", "file", "C:\\shots\\login error.png", [])).toBe("login error");
    expect(defaultLabel("image", "file", "image.png", ["Image 1"])).toBe("Image 2");
    expect(defaultLabel("video", "file", "crash.mov", [])).toBe("crash");
    expect(defaultLabel("video", "paste", "video.mp4", [])).toBe("Video 1");
  });

  test("a label is one short line", () => {
    expect(cleanLabel("  login\n page   error ")).toBe("login page error");
    expect([...cleanLabel("é".repeat(100))]).toHaveLength(MAX_LABEL);
  });
});

describe("video", () => {
  test("the gate follows the lead's model and the Deck's features", () => {
    const lead = { kind: "claude", slug: "claude-opus-5-5", video: false };
    const gate = videoGate(["images", "video", "video_frames"], lead);
    expect(gate).toEqual({
      available: true,
      ok: false,
      reason: "claude-opus-5-5 can't watch video",
      frames: true,
      framesReason: "",
    });
    expect(videoGate(["video"], { ...lead, video: true }).ok).toBe(true);
    const noFfmpeg = videoGate(["video"], lead);
    expect(noFfmpeg.frames).toBe(false);
    expect(noFfmpeg.framesReason).toContain("ffmpeg");
    expect(videoGate(["images"], { ...lead, video: true }).ok).toBe(false);
    expect(videoGate(["video"], undefined).reason).toBe("This agent can't watch video");
  });

  test("a held video blocks the message until it goes as frames", () => {
    const gate = videoGate(["video", "video_frames"], { kind: "codex", video: false });
    const video = { name: "a.mp4", mime: "video/mp4" };
    expect(blockedVideo([video], gate)).toBe(true);
    expect(blockedVideo([{ ...video, frames: true }], gate)).toBe(false);
    expect(blockedVideo([{ name: "a.png", mime: "image/png" }], gate)).toBe(false);
    expect(blockedVideo([video], videoGate(["video"], { kind: "opencode", video: true }))).toBe(
      false,
    );
  });

  test("only MP4, WebM and QuickTime, up to the limit", () => {
    expect(
      checkVideo({ name: "a.mp4", type: "video/mp4", size: 10 }, MAX_VIDEO_BYTES_REMOTE).ok,
    ).toBe(true);
    expect(
      checkVideo({ name: "a.mkv", type: "video/x-matroska", size: 10 }, MAX_VIDEO_BYTES_REMOTE).ok,
    ).toBe(false);
    const big = checkVideo(
      { name: "a.mov", type: "video/quicktime", size: MAX_VIDEO_BYTES_REMOTE + 1 },
      MAX_VIDEO_BYTES_REMOTE,
    );
    expect(big).toEqual({ ok: false, reason: "a.mov is larger than 25 MB." });
  });

  test("a chunk is base64'd without FileReader", async () => {
    expect(await blobBase64(new Blob([new Uint8Array([0, 1, 2, 250])]))).toBe("AAEC+g==");
  });
});
