import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// Attachments over the fake rpc Deck: each image's label (pasted, renamed,
// sent, and shown under it in the timeline), and video: sent to a lead
// whose model takes it, else shown as blocked with the reason and sent as
// frames.
let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;

// A 1×1 PNG, and the start of an MP4 file.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=";
const MP4 = Buffer.from("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2avc1mp41", "latin1");

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });
const strip = (page: Page) => page.getByLabel("Attachments");

async function openMain(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
}

// Pastes a PNG into the prompt, as a screenshot from the clipboard.
async function pasteShot(page: Page): Promise<void> {
  await composer(page).evaluate((el, b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "image.png", { type: "image/png" }));
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  }, PNG);
}

async function attachVideo(page: Page): Promise<void> {
  await page.locator('.composer input[type="file"]').setInputFiles({ name: "clip.mp4", mimeType: "video/mp4", buffer: MP4 });
}

test("a pasted screenshot is labelled, renamed, sent with its label and shown under it", async ({ page }) => {
  await openMain(page);
  await pasteShot(page);
  await expect(strip(page).getByRole("button", { name: "Rename Screenshot 1" })).toBeVisible();
  await pasteShot(page);
  await expect(strip(page).getByRole("button", { name: "Rename Screenshot 2" })).toBeVisible();

  // Enter keeps a new label; Esc keeps the old one.
  await strip(page).getByRole("button", { name: "Rename Screenshot 1" }).click();
  const input = strip(page).getByRole("textbox", { name: "Attachment label" });
  await expect(input).toBeFocused();
  await input.fill("login page error");
  await input.press("Enter");
  await expect(strip(page).getByRole("button", { name: "Rename login page error" })).toBeVisible();
  await strip(page).getByRole("button", { name: "Rename Screenshot 2" }).click();
  await input.fill("nope");
  await input.press("Escape");
  await expect(strip(page).getByRole("button", { name: "Rename Screenshot 2" })).toBeVisible();
  await expect(input).toHaveCount(0);
  await expect(composer(page)).toHaveValue("", { timeout: 1000 });

  await composer(page).fill("compare Image 1 and Image 2");
  await composer(page).press("Enter");
  await expect.poll(() => fake.sends.at(-1)?.images.map((im) => im["label"])).toEqual(["login page error", "Screenshot 2"]);
  expect(fake.sends.at(-1)?.images.every((im) => im["mime"] === "image/png" && Number(im["bytes"]) > 0)).toBe(true);
  await expect(strip(page)).toHaveCount(0);

  // The timeline shows each label under its image.
  const sent = page.locator(".msg").filter({ hasText: "compare Image 1 and Image 2" }).last();
  await expect(sent.locator(".image-label")).toHaveText(["login page error", "Screenshot 2"]);
});

test("a video goes to a lead whose model takes it", async ({ page }) => {
  fake.video.add("claude");
  await openMain(page);
  await attachVideo(page);
  const chip = strip(page).locator('[data-kind="video"]');
  await expect(chip.getByRole("button", { name: "Rename clip" })).toBeVisible();
  await expect(chip.getByRole("button", { name: "Send as frames" })).toHaveCount(0);
  await composer(page).fill("what breaks here?");
  await composer(page).press("Enter");
  await expect.poll(() => fake.sends.at(-1)?.images).toEqual([{ name: "clip.mp4", mime: "video/mp4", label: "clip", frames: false, path: "", bytes: MP4.length }]);
  const sent = page.locator(".msg").filter({ hasText: "what breaks here?" }).last();
  await expect(sent.locator('.chip[data-kind="video"] .image-label')).toHaveText("clip");
});

test("a lead that cannot watch video: the video is blocked with the reason, and goes as frames", async ({ page }) => {
  await openMain(page);
  await attachVideo(page);
  const chip = strip(page).locator('[data-kind="video"]');
  await expect(chip.locator(".attachment-note")).toHaveText("claude-opus-5-5 can't watch video");
  const send = page.getByRole("button", { name: "Send", exact: true });
  await composer(page).fill("what breaks here?");
  await expect(send).toBeDisabled();
  await expect(send).toHaveAttribute("title", /can't watch video: remove the video or send it as frames/);

  await chip.getByRole("button", { name: "Send as frames" }).click();
  await expect(chip.locator(".attachment-note")).toHaveText("Goes as still frames");
  await expect(send).toBeEnabled();
  await composer(page).press("Enter");
  await expect.poll(() => fake.sends.at(-1)?.images.map((im) => [im["mime"], im["label"], im["frames"]])).toEqual([["video/mp4", "clip", true]]);
  const sent = page.locator(".msg").filter({ hasText: "what breaks here?" }).last();
  await expect(sent.locator(".image-label")).toHaveText(["clip · 0:01", "clip · 0:03"]);
});

test("without ffmpeg a blocked video cannot go as frames either, and says why", async ({ page }) => {
  fake.room.caps.splice(fake.room.caps.indexOf("VideoFrames"), 1);
  await openMain(page);
  await attachVideo(page);
  const frames = strip(page).getByRole("button", { name: "Send as frames" });
  await expect(frames).toBeDisabled();
  await expect(frames).toHaveAttribute("title", "Sending frames needs ffmpeg on the computer running Deck.");
  // Removing the video unblocks the message.
  await strip(page).getByRole("button", { name: "Remove clip.mp4" }).click();
  await composer(page).fill("never mind");
  await expect(page.getByRole("button", { name: "Send", exact: true })).toBeEnabled();
});
