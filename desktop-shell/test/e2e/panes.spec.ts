import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Locator, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The room's panes (src/renderer/layout/): the terminal dragged beside and
// under the conversation with the mouse, split like tmux, a divider
// dragged and keyed, a pane zoomed, a preset, the layout kept across a
// reload, and nothing out of the window at 1360 and 860 pixels.
// SHOTS=<dir> also saves screenshots of the layouts for design review.
const OUT = process.env["SHOTS"] ?? "";

let fake: FakeRpcDeck;
let host: TestHost;
test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-panes-"));
  host = await attachBridge(page, async () => dir);
});
test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

async function openRoom(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator(".msg").first()).toBeVisible();
}

const room = (page: Page) => page.locator(".workspace .room-sheet");
const terminals = (page: Page) => page.getByRole("region", { name: "Terminal" });
const box = async (l: Locator) => {
  const b = await l.boundingBox();
  if (b === null) throw new Error("not on screen");
  return b;
};

// Drags a pane's tab with the mouse, in steps as a hand would, to a point;
// before it drops, the drop zone says where it will go.
async function drag(page: Page, handle: Locator, to: { x: number; y: number }, zone: string, shot?: string): Promise<void> {
  const h = await box(handle);
  await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2);
  await page.mouse.down();
  await page.mouse.move(h.x + h.width / 2 + 8, h.y + h.height / 2 + 8, { steps: 2 });
  await page.mouse.move(to.x, to.y, { steps: 8 });
  await expect(page.locator(".drop-zone")).toContainText(zone);
  await expect(page.locator(".drag-ghost")).toBeVisible();
  if (shot !== undefined && OUT !== "") await page.screenshot({ path: join(OUT, shot) });
  await page.mouse.up();
  await expect(page.locator(".drop-zone")).toHaveCount(0);
}

// Nothing runs past the window or out of the room: every pane on screen
// sits inside the workspace, and the page never scrolls sideways.
async function fits(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = [];
    const ws = document.querySelector(".workspace")?.getBoundingClientRect();
    if (ws === undefined) return ["no workspace"];
    for (const el of document.querySelectorAll<HTMLElement>(".workspace .stack")) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      if (r.left < ws.left - 1 || r.right > ws.right + 1 || r.top < ws.top - 1 || r.bottom > ws.bottom + 1) out.push(`${el.dataset["stack"]} leaves the room`);
      if (el.scrollWidth > el.clientWidth + 1) out.push(`${el.dataset["stack"]} scrolls sideways`);
    }
    const doc = document.scrollingElement;
    if (doc !== null && doc.scrollWidth > window.innerWidth + 1) out.push("the page scrolls sideways");
    if (doc !== null && doc.scrollHeight > window.innerHeight + 1) out.push("the page scrolls down");
    return out;
  });
}

const saved = (page: Page) => page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.layout") ?? "null"));

test("the terminal drags beside and under the conversation, splits, resizes, zooms, and the layout persists", async ({ page }) => {
  await openRoom(page);
  // Default: the terminal shows under the conversation.
  await page.keyboard.press("Control+j");
  await expect(terminals(page).locator(".xterm-rows")).toContainText("fake shell");
  let r = await box(room(page));
  let t = await box(terminals(page));
  expect(t.y).toBeGreaterThan(r.y + r.height - 1);

  // Dragged by its tab onto the conversation's right edge: beside it.
  const tab = terminals(page).getByRole("tab", { name: "Terminal 1" });
  await drag(page, tab, { x: r.x + r.width - 30, y: r.y + r.height / 2 }, "Split right");
  r = await box(room(page));
  t = await box(terminals(page));
  expect(t.x).toBeGreaterThan(r.x + r.width - 1);
  expect(Math.abs(t.y - r.y)).toBeLessThan(2);
  // The shell is the same one, still running.
  await expect(terminals(page).locator(".xterm-rows")).toContainText("fake shell");
  expect(fake.calls.filter((m) => m === "TerminalClose")).toHaveLength(0);

  // And onto its bottom edge: under it again.
  await drag(page, terminals(page).getByRole("tab", { name: "Terminal 1" }), { x: r.x + r.width / 2, y: r.y + r.height - 20 }, "Split down");
  r = await box(room(page));
  t = await box(terminals(page));
  expect(t.y).toBeGreaterThan(r.y + r.height - 1);
  expect(Math.abs(t.x - r.x)).toBeLessThan(2);

  // Back to its right, then split like tmux: a second shell under the first.
  await drag(page, terminals(page).getByRole("tab", { name: "Terminal 1" }), { x: r.x + r.width - 30, y: r.y + r.height / 3 }, "Split right");
  await terminals(page).locator(".xterm").click();
  await page.keyboard.press("Control+Shift+Minus");
  await expect(terminals(page)).toHaveCount(2);
  await expect(terminals(page).nth(1).getByRole("tab", { name: "Terminal 2" })).toBeVisible();
  await expect(terminals(page).nth(1).locator(".xterm-rows")).toContainText("fake shell");
  const [t1, t2] = [await box(terminals(page).nth(0)), await box(terminals(page).nth(1))];
  expect(t2.y).toBeGreaterThan(t1.y + t1.height - 1);
  // Each shell runs on its own.
  await terminals(page).nth(1).locator(".xterm").click();
  await page.keyboard.type("ls");
  await page.keyboard.press("Enter");
  await expect(terminals(page).nth(1).locator(".xterm-rows")).toContainText("ran: ls");
  await expect(terminals(page).nth(0).locator(".xterm-rows")).not.toContainText("ran: ls");
  if (OUT !== "") await page.screenshot({ path: join(OUT, "dark-3-terminal-right-split.png") });

  // The divider between the conversation and the terminals drags, and
  // takes the arrow keys.
  const divider = page.getByRole("separator", { name: "Resize Conversation and Terminal 1" });
  const d = await box(divider);
  const before = (await box(room(page))).width;
  await page.mouse.move(d.x + d.width / 2, d.y + d.height / 2);
  await page.mouse.down();
  await page.mouse.move(d.x + d.width / 2 + 60, d.y + d.height / 2, { steps: 5 });
  await page.mouse.up();
  await expect.poll(async () => (await box(room(page))).width).toBeGreaterThan(before + 50);
  const dragged = (await box(room(page))).width;
  await divider.focus();
  await page.keyboard.press("ArrowLeft");
  await expect.poll(async () => Math.round((await box(room(page))).width)).toBe(Math.round(dragged - 16));

  // The keys move between the panes, in reading order, and mark the one
  // they reach.
  await terminals(page).nth(0).locator(".xterm").click();
  await page.keyboard.press("Control+Shift+BracketRight");
  await expect(terminals(page).nth(1)).toHaveAttribute("data-focused", "");
  await page.keyboard.press("Control+Shift+BracketRight");
  await expect(room(page)).toHaveAttribute("data-focused", "");
  await expect(page.getByRole("textbox", { name: "Message" })).toBeFocused();
  await page.keyboard.press("Control+Shift+BracketLeft");
  await expect(terminals(page).nth(1)).toHaveAttribute("data-focused", "");

  // Zoom: the focused terminal fills the room, then gives it back.
  await terminals(page).nth(1).locator(".xterm").click();
  await page.keyboard.press("Control+Shift+M");
  await expect(room(page)).toBeHidden();
  await expect(terminals(page)).toHaveCount(1);
  const ws = await box(page.locator(".workspace"));
  expect((await box(terminals(page))).width).toBeGreaterThan(ws.width - 2);
  await page.keyboard.press("Control+Shift+M");
  await expect(room(page)).toBeVisible();
  await expect(terminals(page)).toHaveCount(2);

  // A preset from the Layout menu: the terminals across the bottom.
  await page.getByRole("button", { name: "Layout" }).click();
  if (OUT !== "") {
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(OUT, "dark-5-layout-menu.png") });
  }
  await page.getByRole("menu", { name: "Layout" }).getByRole("menuitem", { name: "Terminal below" }).click();
  await expect(terminals(page)).toHaveCount(1);
  await expect(terminals(page).getByRole("tab")).toHaveCount(2);
  const below = await box(terminals(page));
  expect(Math.abs(below.x - ws.x)).toBeLessThan(2);
  expect(below.width).toBeGreaterThan(ws.width - 2);
  expect(below.y).toBeGreaterThan((await box(room(page))).y + 100);
  // The shells live on through the preset.
  await terminals(page).getByRole("tab", { name: "Terminal 2" }).click();
  await expect(terminals(page).locator(".pane-slot:not([hidden]) .xterm-rows")).toContainText("ran: ls");
  expect(fake.calls.filter((m) => m === "TerminalClose")).toHaveLength(0);

  // A reload keeps the arrangement.
  const kept = await saved(page);
  expect(kept.global.root.dir).toBe("col");
  // The folder opens again by itself.
  await page.reload();
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator(".msg").first()).toBeVisible();
  await page.keyboard.press("Control+j");
  const again = await box(terminals(page));
  expect(Math.abs(again.x - ws.x)).toBeLessThan(2);
  expect(again.width).toBeGreaterThan(ws.width - 2);
  await expect(terminals(page).getByRole("tab")).toHaveCount(2);
  expect(await fits(page)).toEqual([]);

  // Reset goes back to Default: the terminal under the conversation.
  await page.getByRole("button", { name: "Layout" }).click();
  await page.getByRole("menu", { name: "Layout" }).getByRole("menuitem", { name: "Reset layout" }).click();
  expect((await saved(page)).global).toBeNull();
  r = await box(room(page));
  t = await box(terminals(page));
  expect(Math.abs(t.x - r.x)).toBeLessThan(2);
});

test("panes show and hide from the Layout menu, the side panel's buttons drive them, and a project keeps its own", async ({ page }) => {
  await openRoom(page);
  const panel = page.getByRole("complementary", { name: "Side panel" });
  const menu = page.getByRole("menu", { name: "Layout" });
  // Review: the diff and the agents beside the conversation, each a stack.
  await page.getByRole("button", { name: "Layout" }).click();
  await menu.getByRole("menuitem", { name: "Review" }).click();
  await expect(panel).toHaveCount(2);
  await expect(panel.nth(0).getByRole("tab", { name: "Changes" })).toHaveAttribute("aria-selected", "true");
  await expect(panel.nth(1).getByRole("tab", { name: "Agents" })).toBeVisible();
  // The header's toggle hides both, and shows them again.
  await page.getByRole("button", { name: "Hide the side panel" }).click();
  await expect(panel).toHaveCount(0);
  await page.getByRole("button", { name: "Show the side panel" }).click();
  await expect(panel).toHaveCount(2);
  // Agents hidden from the menu keeps its place for later.
  await page.getByRole("button", { name: "Layout" }).click();
  await menu.getByRole("menuitemcheckbox", { name: "Agents" }).click();
  await expect(panel).toHaveCount(1);
  await page.getByRole("button", { name: "Layout" }).click();
  await expect(menu.getByRole("menuitemcheckbox", { name: "Agents" })).toHaveAttribute("aria-checked", "false");
  await menu.getByRole("menuitemcheckbox", { name: "Agents" }).click();
  await expect(panel).toHaveCount(2);
  await expect(panel.nth(1).getByRole("tab", { name: "Agents" })).toBeVisible();

  // A tab dragged onto another stack's middle joins it.
  const target = await box(panel.nth(0));
  await drag(page, panel.nth(1).getByRole("tab", { name: "Agents" }), { x: target.x + target.width / 2, y: target.y + target.height / 2 }, "Add as a tab");
  await expect(panel).toHaveCount(1);
  await expect(panel.getByRole("tab", { name: "Agents" })).toHaveAttribute("aria-selected", "true");

  // Only this project: its layout is its own; turning it off goes back to
  // the shared one.
  await page.getByRole("button", { name: "Layout" }).click();
  await menu.getByRole("menuitemcheckbox", { name: "Only this project" }).click();
  const own = await saved(page);
  expect(Object.keys(own.projects)).toHaveLength(1);
  await page.getByRole("button", { name: "Layout" }).click();
  await menu.getByRole("menuitem", { name: "Focus", exact: true }).click();
  await expect(panel).toHaveCount(0);
  await page.getByRole("button", { name: "Layout" }).click();
  await expect(menu.getByRole("menuitemcheckbox", { name: "Only this project" })).toHaveAttribute("aria-checked", "true");
  await menu.getByRole("menuitemcheckbox", { name: "Only this project" }).click();
  expect((await saved(page)).projects).toEqual({});
});

test("a broken saved layout falls back to Default", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("deck.desktop.layout", '{"global":{"v":1,"root":{"kind":"split","dir":"row","children":[{"kind":"stack","panes":["changes"]}]}},"projects":7}'));
  await openRoom(page);
  await page.keyboard.press("Control+j");
  const r = await box(room(page));
  const t = await box(terminals(page));
  expect(t.y).toBeGreaterThan(r.y + r.height - 1);
});

for (const width of [1360, 860]) {
  test(`nothing overflows at ${width}px with the terminal beside and the panel open`, async ({ page }) => {
    await page.setViewportSize({ width, height: 860 });
    await openRoom(page);
    await page.getByRole("button", { name: "Layout" }).click();
    await page.getByRole("menu", { name: "Layout" }).getByRole("menuitem", { name: "Terminal right" }).click();
    await expect(terminals(page).locator(".xterm-rows")).toContainText("fake shell");
    await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
    await expect(page.getByRole("complementary", { name: "Side panel" })).toBeVisible();
    await page.waitForTimeout(300);
    expect(await fits(page)).toEqual([]);
    // Too narrow for all three side by side, the others float over the
    // conversation, which keeps its width.
    const r = await box(room(page));
    if (width === 860) {
      expect(await page.locator(".stack[data-float]").count()).toBeGreaterThan(0);
      expect(r.width).toBeGreaterThan(480);
    } else {
      expect(await page.locator(".stack[data-float]").count()).toBe(0);
    }
    // Every pane keeps its least width.
    for (const s of await page.locator(".workspace .stack:visible").all()) expect((await box(s)).width).toBeGreaterThanOrEqual(219);
    // The header and the composer stay whole.
    await expect(page.locator(".topbar")).toBeInViewport({ ratio: 1 });
    await expect(page.locator(".composer-box")).toBeInViewport({ ratio: 1 });
  });
}

for (const mode of ["dark", "light"] as const) {
  test(`pane shots ${mode}`, async ({ page }) => {
    test.skip(OUT === "", "SHOTS names the folder for the screenshots");
    await page.emulateMedia({ colorScheme: mode });
    await page.addInitScript((m) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m })), mode);
    await openRoom(page);
    await page.keyboard.press("Control+j");
    await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
    await expect(terminals(page).locator(".xterm-rows")).toContainText("fake shell");
    await page.mouse.move(2, 600);
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(OUT, `${mode}-1-default.png`) });
    if (mode === "light") return;
    // Mid-drag: the terminal over the conversation's right edge.
    const r = await box(room(page));
    const h = await box(terminals(page).getByRole("tab", { name: "Terminal 1" }));
    await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2);
    await page.mouse.down();
    await page.mouse.move(r.x + r.width - 40, r.y + r.height / 2, { steps: 8 });
    await expect(page.locator(".drop-zone")).toContainText("Split right");
    await page.waitForTimeout(200);
    await page.screenshot({ path: join(OUT, `${mode}-4-drop-zone.png`) });
    await page.keyboard.press("Escape");
    await page.mouse.up();
    await page.getByRole("button", { name: "Layout" }).click();
    await page.getByRole("menu", { name: "Layout" }).getByRole("menuitem", { name: "Terminal below" }).click();
    await page.mouse.move(2, 600);
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(OUT, `${mode}-2-terminal-below.png`) });
    // At 860px the panes beside the conversation float over it.
    await page.setViewportSize({ width: 860, height: 760 });
    await page.getByRole("button", { name: "Layout" }).click();
    await page.getByRole("menu", { name: "Layout" }).getByRole("menuitem", { name: "Terminal right" }).click();
    await page.mouse.move(2, 600);
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(OUT, `${mode}-6-narrow-860.png`) });
  });
}
