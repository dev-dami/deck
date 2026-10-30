import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeDeck } from "../fake-server";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// Screenshots of attachments, for design review: two labelled images (one
// being renamed), a video for a lead that watches video, a video held for
// one that cannot (with Send as frames), the labels in the timeline, and
// the phone's composer. SHOTS=<dir> npx playwright test media-shots;
// without SHOTS the e2e run skips them.
const OUT = process.env["SHOTS"] ?? "";
test.skip(OUT === "", "SHOTS names the folder for the screenshots");

const shot = (page: Page, name: string, clip?: { x: number; y: number; width: number; height: number }) =>
  page.screenshot({ path: join(OUT, `${name}.png`), ...(clip !== undefined ? { clip } : {}) });

// A short test-pattern video (WebM: Chromium plays it) when ffmpeg is
// here, else an MP4's first bytes.
function video(): Buffer {
  const out = join(mkdtempSync(join(tmpdir(), "deck-shots-")), "crash.webm");
  try {
    execFileSync("ffmpeg", ["-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=10", "-c:v", "libvpx", "-y", out]);
    return readFileSync(out);
  } catch {
    return Buffer.from("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2avc1mp41", "latin1");
  }
}

// Draws a mock screen into a PNG: a title bar, a form, and a line of text.
async function mockScreen(page: Page, title: string, line: string, tint: string): Promise<Buffer> {
  const b64 = await page.evaluate(
    ([t, l, c]) => {
      const cv = document.createElement("canvas");
      cv.width = 480;
      cv.height = 300;
      const g = cv.getContext("2d") as CanvasRenderingContext2D;
      g.fillStyle = "#f4f4f6";
      g.fillRect(0, 0, 480, 300);
      g.fillStyle = "#1f2230";
      g.fillRect(0, 0, 480, 44);
      g.fillStyle = "#fff";
      g.font = "bold 18px sans-serif";
      g.fillText(t as string, 18, 29);
      g.fillStyle = "#d9dbe3";
      for (const y of [80, 136]) g.fillRect(40, y, 400, 38);
      g.fillStyle = c as string;
      g.fillRect(40, 200, 400, 40);
      g.fillStyle = "#c62828";
      g.font = "15px sans-serif";
      g.fillText(l as string, 40, 270);
      return cv.toDataURL("image/png").split(",")[1] ?? "";
    },
    [title, line, tint],
  );
  return Buffer.from(b64, "base64");
}

// Pastes a PNG into the prompt, as a screenshot from the clipboard.
async function paste(page: Page, png: Buffer): Promise<void> {
  await page.getByRole("textbox", { name: "Message" }).evaluate((el, b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "image.png", { type: "image/png" }));
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  }, png.toString("base64"));
}

test.describe("desktop", () => {
  let fake: FakeRpcDeck;
  let host: TestHost;
  let dir: string;
  test.beforeEach(async ({ page }) => {
    fake = await FakeRpcDeck.start({ tickMs: 250 });
    dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
    process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
    host = await attachBridge(page, async () => dir);
    await page.emulateMedia({ colorScheme: "dark" });
    await page.addInitScript(() => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: "dark" })));
  });
  test.afterEach(async () => {
    host.decks.closeAll();
    await fake.close();
  });

  async function open(page: Page): Promise<void> {
    await page.goto("/");
    await page.getByRole("button", { name: "Open a folder" }).click();
    await expect(page.locator(".project")).toHaveText(basename(dir));
    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  }

  const composerClip = async (page: Page) => {
    const box = await page.locator(".composer").boundingBox();
    if (box === null) throw new Error("no composer");
    return { x: Math.max(0, box.x - 24), y: Math.max(0, box.y - 24), width: box.width + 48, height: box.height + 48 };
  };

  test("labelled images, a rename, and the sent labels", async ({ page }) => {
    await open(page);
    await paste(page, await mockScreen(page, "Sign in", "Invalid token: try again in 15 minutes", "#3b6cf6"));
    await paste(page, await mockScreen(page, "Checkout", "Payment failed (402)", "#2e9d5b"));
    const strip = page.getByLabel("Attachments");
    await strip.getByRole("button", { name: "Rename Screenshot 1" }).click();
    await strip.getByRole("textbox", { name: "Attachment label" }).fill("login page error");
    await strip.getByRole("textbox", { name: "Attachment label" }).press("Enter");
    await strip.getByRole("button", { name: "Rename Screenshot 2" }).click();
    await strip.getByRole("textbox", { name: "Attachment label" }).fill("checkout butt");
    await page.getByRole("textbox", { name: "Message" }).evaluate((el) => el.blur);
    await page.waitForTimeout(300);
    await shot(page, "desktop-composer-labels", await composerClip(page));
    await shot(page, "desktop-composer-labels-full");

    await strip.getByRole("textbox", { name: "Attachment label" }).fill("checkout button");
    await strip.getByRole("textbox", { name: "Attachment label" }).press("Enter");
    await page.getByRole("textbox", { name: "Message" }).fill("compare Image 1 and Image 2: why does checkout fail after the login error?");
    await page.getByRole("textbox", { name: "Message" }).press("Enter");
    const sent = page.locator(".msg").filter({ hasText: "compare Image 1 and Image 2" }).last();
    await expect(sent.locator(".image-label")).toHaveText(["login page error", "checkout button"]);
    await page.setViewportSize({ width: 1360, height: 1280 });
    const latest = page.getByRole("button", { name: "Jump to latest" });
    // It may come and go as the reply streams in.
    await latest.click({ timeout: 2000 }).catch(() => undefined);
    await page.waitForTimeout(600);
    await shot(page, "desktop-timeline-labels");
  });

  test("a video for a lead that watches video", async ({ page }) => {
    fake.video.add("claude");
    await open(page);
    await page.locator('.composer input[type="file"]').setInputFiles({ name: "crash.webm", mimeType: "video/webm", buffer: video() });
    await page.getByRole("textbox", { name: "Message" }).fill("what happens right before it crashes?");
    await page.waitForTimeout(600);
    await shot(page, "desktop-video-ok", await composerClip(page));
  });

  test("a video held for a lead that cannot watch one", async ({ page }) => {
    await open(page);
    await page.locator('.composer input[type="file"]').setInputFiles({ name: "crash.webm", mimeType: "video/webm", buffer: video() });
    await page.getByRole("textbox", { name: "Message" }).fill("what happens right before it crashes?");
    await page.waitForTimeout(600);
    await shot(page, "desktop-video-blocked", await composerClip(page));
    await page.getByLabel("Attachments").getByRole("button", { name: "Send as frames" }).click();
    await page.waitForTimeout(200);
    await shot(page, "desktop-video-as-frames", await composerClip(page));
    await page.getByRole("textbox", { name: "Message" }).press("Enter");
    const sent = page.locator(".msg").filter({ hasText: "what happens right before it crashes?" }).last();
    await expect(sent.locator(".image-label")).toHaveText(["crash · 0:01", "crash · 0:03"]);
    await page.waitForTimeout(400);
    await shot(page, "desktop-timeline-frames");
  });
});

test.describe("phone", () => {
  let fake: FakeDeck;
  test.beforeEach(async () => {
    fake = await FakeDeck.start({ tickMs: 250, static: resolve(__dirname, "../../../pkg/remote/web/dist") });
  });
  test.afterEach(async () => fake.close());

  test("the phone's composer with labelled attachments", async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, colorScheme: "dark", hasTouch: true });
    // The fixture's pending approval would cover the composer.
    for (const a of fake.invoke("approvals.list", {}) as Array<{ id: string; session_id: string }>) {
      fake.invoke("approvals.answer", { session_id: a.session_id, id: a.id, decision: "deny" });
    }
    const page = await ctx.newPage();
    await page.goto(fake.pairLink);
    await page.getByRole("button", { name: "Pair this phone" }).click();
    await page.getByRole("link", { name: /Fix the pairing lockout/ }).click();
    fake.setLead("ses_main", "claude", "claude-opus-5-5", false);
    const login = await mockScreen(page, "Sign in", "Invalid token: try again in 15 minutes", "#3b6cf6");
    const checkout = await mockScreen(page, "Checkout", "Payment failed (402)", "#2e9d5b");
    await page.locator('form.composer input[type="file"]').setInputFiles([
      { name: "IMG_2041.png", mimeType: "image/png", buffer: login },
      { name: "IMG_2042.png", mimeType: "image/png", buffer: checkout },
      { name: "crash.webm", mimeType: "video/webm", buffer: video() },
    ]);
    await page.getByRole("button", { name: "Rename IMG_2041" }).click();
    await page.getByRole("textbox", { name: "Attachment label" }).fill("login page error");
    await page.getByRole("textbox", { name: "Attachment label" }).press("Enter");
    await page.getByRole("textbox", { name: "Message" }).fill("compare Image 1 and Image 2");
    await page.waitForTimeout(600);
    await shot(page, "phone-composer-labels");
    await ctx.close();
  });
});
