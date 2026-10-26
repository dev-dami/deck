import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The Work Graph in the side panel, over a local project whose thread has
// a crew at work: two members, a subagent each, tasks with dependencies,
// the files they touch, and one agent editing a file now. With SHOTS set it
// also screenshots the graph in dark and light (npm run shots).
const OUT = process.env["SHOTS"] ?? "";
let fake: FakeRpcDeck;
let host: TestHost;

const LIMIT = "pkg/remote/auth/limit.go";
const TEST = "pkg/remote/auth/limit_test.go";
const PRUNE = "pkg/remote/auth/prune.go";

// The crew and the board, as deck.v1's Agents and Board answer.
function crewAtWork(f: FakeRpcDeck): void {
  const now = Date.now();
  f.crew = {
    agents: [
      {
        name: "claude-1",
        kind: "claude",
        model: "opus",
        effort: "high",
        status: "tool",
        context_tokens: 92_000,
        context_window: 200_000,
        cost_usd: 1.84,
        since: now - 194_000,
        leases: "pkg/remote/auth/** (exclusive)",
      },
      { name: "codex-1", kind: "codex", model: "gpt-5-codex", status: "idle", context_tokens: 41_000, context_window: 272_000, cost_usd: 0.62 },
      {
        name: "claude-sub",
        kind: "claude",
        model: "sonnet",
        status: "working",
        depth: 1,
        parent: "claude-1",
        branch: "deck/t2",
        context_tokens: 18_000,
        context_window: 200_000,
        cost_usd: 0.21,
        since: now - 71_000,
      },
      { name: "codex-sub", kind: "codex", model: "gpt-5-codex", status: "exited", depth: 1, parent: "codex-1", branch: "deck/t3", cost_usd: 0.33 },
    ],
    running: [
      { agent: "claude-1", kind: "claude", name: "Edit", summary: LIMIT, started: now - 8_000 },
      { agent: "claude-sub", kind: "claude", name: "Bash", summary: "go test ./pkg/remote/auth -run Limiter", started: now - 21_000, command: true },
    ],
  };
  f.board.orchestrate = true;
  f.board.tasks = [
    { id: "t1", title: "Map the limiter", status: "done", owner: "claude-1", files: [LIMIT] },
    { id: "t2", title: "Key failures by address", status: "running", owner: "claude-sub", deps: ["t1"], files: [LIMIT, TEST], branch: "deck/t2" },
    { id: "t3", title: "Prune idle buckets", status: "done", owner: "codex-sub", deps: ["t1"], files: [PRUNE], branch: "deck/t3" },
    { id: "t4", title: "Document the limit", status: "ready", owner: "codex-1", files: ["docs/pairing.md"] },
  ];
  f.deck.setChanges("ses_main", [
    { path: LIMIT, added: 18, removed: 6, diff: "@@ -12,3 +12,4 @@\n-\tglobal.fail()\n+\tl.fail(addr)\n" },
    { path: TEST, added: 42, removed: 0, diff: "@@ -0,0 +1,1 @@\n+func TestLimiterIsPerAddress(t *testing.T) {}\n" },
    { path: PRUNE, added: 27, removed: 3, diff: "@@ -0,0 +1,1 @@\n+func (l *Limiter) prune() {}\n" },
  ]);
}

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  crewAtWork(fake);
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-graph-"));
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

async function openGraph(page: Page, mode: "dark" | "light" = "dark"): Promise<void> {
  await page.emulateMedia({ colorScheme: mode });
  await page.addInitScript((m) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m })), mode);
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).not.toHaveText("");
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  if ((await panel.count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel.getByRole("tab", { name: "Graph" }).click();
  await expect(graph(page).locator('[data-node="agent:claude-sub"]')).toBeVisible();
}

const graph = (page: Page) => page.getByRole("group", { name: "Work graph" });
const node = (page: Page, id: string) => graph(page).locator(`[data-node="${id}"]`);

test("the graph maps the crew, its tasks and files, and acts on them", async ({ page }) => {
  await openGraph(page);
  const g = graph(page);
  // You, four agents, four tasks and four files.
  await expect(g.locator("[data-node]")).toHaveCount(13);
  await expect(g.locator('[data-kind="agent"]')).toHaveCount(4);
  await expect(g.locator('[data-kind="task"]')).toHaveCount(4);
  await expect(g.locator('[data-kind="file"]')).toHaveCount(4);
  // The live edit flows; a lease is a dotted line.
  await expect(g.locator(".graph-flow")).toHaveCount(1);
  await expect(g.locator('[data-edge="holds"]')).toHaveCount(2);
  await expect(node(page, "agent:claude-1")).toContainText("Edit");
  await expect(node(page, "agent:claude-1")).toContainText(LIMIT);

  // A hover dims all but the neighbourhood.
  await node(page, "agent:claude-sub").hover();
  await expect(node(page, "task:t4").locator("..")).toHaveClass(/opacity-35/);
  await expect(node(page, "task:t2").locator("..")).not.toHaveClass(/opacity-35/);

  // An agent: its card, and Steer.
  await node(page, "agent:claude-1").click();
  const card = page.getByRole("region", { name: "claude-1 details" });
  await expect(card).toContainText("Working");
  await expect(card).toContainText("46%");
  await expect(card).toContainText("$1.84");
  await expect(card.getByRole("list", { name: "Paths claude-1 holds" })).toContainText("pkg/remote/auth/**");
  await card.getByRole("button", { name: "Steer" }).click();
  await expect(page.getByRole("textbox", { name: "Message" })).toHaveValue("$claude-1 ");
  // Stop interrupts it.
  await card.getByRole("button", { name: "Stop claude-1" }).click();
  await expect.poll(() => fake.calls).toContain("Interrupt");
  // An idle agent cannot be stopped; Escape closes the card.
  await node(page, "agent:codex-1").click();
  await expect(page.getByRole("region", { name: "codex-1 details" }).getByRole("button", { name: "Stop codex-1" })).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("region", { name: "codex-1 details" })).toHaveCount(0);

  // The keys: arrows move along the graph, + zooms, 0 fits.
  await node(page, "you").focus();
  await page.keyboard.press("ArrowRight");
  await expect(g.locator('[data-kind="agent"]:focus')).toHaveCount(1);
  const zoom = page.getByRole("toolbar", { name: "Zoom" });
  const before = await zoom.locator("span").first().textContent();
  await page.keyboard.press("+");
  await expect(zoom.locator("span").first()).not.toHaveText(before ?? "");
  await page.keyboard.press("0");
  await expect(zoom.locator("span").first()).toHaveText(before ?? "");

  // A task opens Tasks; a file opens its diff in Changes.
  await node(page, "task:t2").click();
  await expect(page.getByRole("region", { name: "Tasks" })).toBeVisible();
  await page.getByRole("complementary", { name: "Side panel" }).getByRole("tab", { name: "Graph" }).click();
  await node(page, `file:${PRUNE}`).click();
  await expect(page.getByRole("region", { name: "Changes" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Changes" }).locator(".file")).toHaveCount(3);
});

test("a pan moves the canvas, not the cards; a widened panel shows the legend and minimap", async ({ page }) => {
  await openGraph(page);
  const g = graph(page);
  await expect(g.locator(".graph-legend")).toHaveCount(0);
  const card = node(page, "agent:codex-1");
  const a = (await card.boundingBox())!;
  const box = (await g.boundingBox())!;
  await page.mouse.move(box.x + 20, box.y + box.height - 80);
  await page.mouse.down();
  await page.mouse.move(box.x + 80, box.y + box.height - 40, { steps: 4 });
  await page.mouse.up();
  const b = (await card.boundingBox())!;
  expect(Math.round(b.x - a.x)).toBe(60);
  expect(Math.round(b.y - a.y)).toBe(40);
  // The press that ended the pan picked nothing.
  await expect(page.getByRole("region", { name: /details$/ })).toHaveCount(0);

  await page.getByRole("button", { name: "Widen the panel" }).click();
  await expect(g.locator(".graph-legend")).toContainText("2 working");
  await expect(g.locator(".graph-legend")).toContainText("2/4 tasks done");
  await expect(g.getByRole("img", { name: "Minimap" })).toBeVisible();
});

test("without a tree or a board, the graph maps the roster", async ({ page }) => {
  fake.crew = { agents: [], running: [] };
  fake.board.tasks = [];
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Long history", exact: true }).click();
  await page.getByRole("button", { name: "Show the side panel" }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  await panel.getByRole("tab", { name: "Graph" }).click();
  // You and its one agent.
  await expect(panel.locator('[data-node="agent:claude-1"]')).toBeVisible();
  await expect(panel.locator("[data-node]")).toHaveCount(2);
});

for (const mode of ["dark", "light"] as const) {
  test(`graph shots ${mode}`, async ({ page }) => {
    test.skip(OUT === "", "SHOTS names the folder for the screenshots");
    await openGraph(page, mode);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(OUT, `${mode}-7-graph-panel.png`) });
    await node(page, "agent:claude-1").click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(OUT, `${mode}-8-graph-agent.png`) });
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "Widen the panel" }).click();
    await page.waitForTimeout(500);
    await node(page, "agent:claude-sub").hover();
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(OUT, `${mode}-9-graph-wide.png`) });
  });
}
