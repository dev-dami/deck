import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// The window's layout under load and at its smallest: a long room renders
// the rows in view only, and 960×600 keeps the header and the composer whole.

let fake: FakeDeck;

test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 100, bigRoom: 500 });
  await attachBridge(page);
});

test.afterEach(async () => {
  await fake.close();
});

async function pair(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Pair with a Deck" }).click();
  await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
  await page.getByRole("button", { name: "Pair", exact: true }).click();
  await expect(page.locator(".project")).toHaveText("deck");
}

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

test("a room of 500 messages renders the rows in view and follows the last one", async ({ page }) => {
  await pair(page);
  await sidebar(page).getByRole("button", { name: "Big room", exact: true }).click();
  await expect(page.locator(".msg").last()).toContainText("Big 500");
  // Pages of 100 come in as the person asks for older ones.
  const older = page.getByRole("button", { name: "Load older messages" });
  for (let i = 0; i < 4; i++) {
    await older.click();
    await expect(page.getByRole("button", { name: "Loading…" })).toHaveCount(0);
  }
  await expect(older).toHaveCount(0);
  const count = await page.locator(".msg").count();
  expect(count).toBeGreaterThan(0);
  expect(count).toBeLessThan(100);

  // The person scrolled up: the pill takes them back to the last message.
  const jump = page.getByRole("button", { name: "Jump to latest" });
  await expect(jump).toBeVisible();
  await jump.click();
  await expect(page.locator(".msg", { hasText: "Big 500" })).toBeInViewport();
  await expect(jump).toHaveCount(0);
  expect(await page.locator(".msg").count()).toBeLessThan(100);

  // At the bottom, the timeline follows a new turn and its reply.
  await composer(page).fill("one more");
  await composer(page).press("Enter");
  await expect(page.locator('[data-agent="claude-1"].msg').last()).toContainText("one more");
  await expect(page.locator(".msg").last()).toBeInViewport();
  await expect(jump).toHaveCount(0);
});

test("at 960×600 the header, the lane strip and the composer show whole", async ({ page }) => {
  await page.setViewportSize({ width: 960, height: 600 });
  await pair(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator(".title")).toHaveText("Fix the pairing lockout");
  // Nothing covers the title, the lanes or the prompt.
  const covered = () =>
    page.evaluate(() =>
      [".title", ".lanes", 'textarea[aria-label="Message"]', ".panel-toggles"].map((s) => {
        const el = document.querySelector(s);
        if (el === null) return `${s}: missing`;
        const r = el.getBoundingClientRect();
        const at = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return el.contains(at) ? "" : `${s}: covered`;
      }),
    );
  expect(await covered()).toEqual(["", "", "", ""]);
  await expect(page.locator(".topbar")).toBeInViewport({ ratio: 1 });
  await expect(page.locator(".composer-box")).toBeInViewport({ ratio: 1 });
  // The header's parts sit side by side, and the strip wraps in place of a scroll.
  const title = await page.locator(".title").boundingBox();
  const toggles = await page.locator(".panel-toggles").boundingBox();
  expect(title !== null && toggles !== null && title.x + title.width <= toggles.x).toBe(true);
  const lanes = page.locator(".lanes");
  expect(await lanes.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

  // Below 1100px the side panel floats over the room, which keeps its width.
  const before = await page.locator("main.center").boundingBox();
  await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  await expect(panel).toBeVisible();
  expect(await panel.evaluate((el) => getComputedStyle(el).position)).toBe("absolute");
  expect((await page.locator("main.center").boundingBox())?.width).toBe(before?.width);});

// The x of each part of the shell, left to right.
const xs = (page: Page, sel: string[]) =>
  page.evaluate((ss) => ss.map((s) => document.querySelector(s)?.getBoundingClientRect().x ?? -1), sel);
const kept = (page: Page) => page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}"));

test("the layout mirrors: the sidebar on the right, the side panel opposite it or beside it", async ({ page }) => {
  await pair(page);
  await sidebar(page).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Appearance" }).click();
  await page.getByRole("radiogroup", { name: "Sidebar", exact: true }).getByRole("radio", { name: "Sidebar on the right" }).click();
  await page.getByRole("radiogroup", { name: "Side panel" }).getByRole("radio", { name: "Panel beside the sidebar" }).click();
  expect(await kept(page)).toMatchObject({ sidebarSide: "right", panelDock: "beside" });
  const [room, nav] = await xs(page, [".room-sheet", "nav.sidebar"]);
  expect(room).toBeLessThan(nav as number);

  await sidebar(page).getByRole("button", { name: "Back to threads" }).click();
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
  await expect(page.getByRole("complementary", { name: "Side panel" })).toBeVisible();
  // Room, then the panel beside the sidebar, then the sidebar.
  const [r, p, n] = await xs(page, [".room-sheet", "aside.panel", "nav.sidebar"]);
  expect(r).toBeLessThan(p as number);
  expect(p).toBeLessThan(n as number);
  // Ctrl+B still hides and shows the sidebar.
  await page.keyboard.press("Control+b");
  await expect(sidebar(page)).toHaveCount(0);
  await page.keyboard.press("Control+b");
  await expect(sidebar(page)).toBeVisible();
});

test("the sidebar's edge resizes it, keeps the width, and drags it across to the other side", async ({ page }) => {
  await pair(page);
  const grip = page.getByRole("separator", { name: "Resize the sidebar" });
  const width = async () => (await sidebar(page).boundingBox())?.width ?? 0;
  expect(await width()).toBe(256);
  const g = await grip.boundingBox();
  if (g === null) throw new Error("no grip");
  const y = g.y + g.height / 2;
  await page.mouse.move(g.x + g.width / 2, y);
  await page.mouse.down();
  await page.mouse.move(g.x + 60, y, { steps: 4 });
  await page.mouse.up();
  await expect.poll(width).toBeGreaterThan(300);
  expect((await kept(page)).sidebarWidth).toBeGreaterThan(300);
  // The keys resize it too.
  const before = await width();
  await grip.focus();
  await page.keyboard.press("ArrowLeft");
  await expect.poll(width).toBe(before - 16);
  // Dragged most of the way across, it moves to the right.
  const h = await grip.boundingBox();
  if (h === null) throw new Error("no grip");
  await page.mouse.move(h.x + h.width / 2, y);
  await page.mouse.down();
  await page.mouse.move(1200, y, { steps: 6 });
  await expect(page.getByText("Move the sidebar here")).toBeVisible();
  await page.mouse.up();
  await expect.poll(async () => (await kept(page)).sidebarSide).toBe("right");
  const [room, nav] = await xs(page, [".room-sheet", "nav.sidebar"]);
  expect(room).toBeLessThan(nav as number);
  expect(await width()).toBe(before - 16);
});

test("with the sidebar on the right, the notifications open inside the window", async ({ page }) => {
  await pair(page);
  await sidebar(page).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Appearance" }).click();
  await page.getByRole("radiogroup", { name: "Sidebar", exact: true }).getByRole("radio", { name: "Sidebar on the right" }).click();
  await sidebar(page).getByRole("button", { name: "Back to threads" }).click();
  await page.getByRole("button", { name: /^Notifications/ }).click();
  const panel = page.getByRole("dialog", { name: "Notifications" });
  await expect(panel).toBeVisible();
  const box = (await panel.boundingBox())!;
  const width = page.viewportSize()!.width;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(width);
});
