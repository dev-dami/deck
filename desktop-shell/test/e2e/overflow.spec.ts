import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { type TestHost } from "../bridge";
import { crewAtWork, FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// Nothing in the room may run out of its box: long names, paths, URLs,
// commands, task titles and results wrap or end in an ellipsis inside
// their card, at every window width. The fake deck is filled with the
// longest things an engine sends, then every visible element is measured.
let fake: FakeRpcDeck;
let host: TestHost;

const LONG_PATH = "internal/deck/session/orchestration/scheduler/dispatch/very_long_directory_name_for_testing/handoff_contract_settlement_test.go";
const LONG_DIR = "internal/deck/session/orchestration/scheduler/dispatch/very_long_directory_name_for_testing/handoff";
const TOKEN = "a".repeat(40) + "UNBROKEN_IDENTIFIER_WITHOUT_ANY_SPACES_" + "b".repeat(60);
const URL = "https://example.com/" + "segment/".repeat(24) + "?query=" + "x".repeat(80);
const CMD = "go test ./internal/deck/session/... -run 'TestSchedulerDoesNotReturnSelfOwnedWorkFinishedInTheSameTurn|TestLevelsGrow' -count=20 -race -timeout 600s -v 2>&1 | tee /tmp/very/long/output/path/log.txt";
const NAME = "reviewer-with-a-really-long-profile-name";

function stress(f: FakeRpcDeck): void {
  crewAtWork(f);
  f.crew.agents.push({ name: NAME, kind: "codex", model: "gpt-5-codex-max-preview", status: "tool", context_tokens: 1_000, context_window: 272_000, leases: `${LONG_DIR}/** , ${LONG_PATH} (exclusive); internal/deck/session/*.go` });
  f.crew.running.push({ agent: NAME, kind: "codex", name: "Bash", summary: CMD, started: Date.now() - 5_000, command: true });
  f.board.tasks.push(
    { id: "t5", title: `Investigate ${TOKEN}`, status: "failed", owner: NAME, error: `${CMD}\n${TOKEN}`, attempts: 2, tries: 2, files: [LONG_PATH] },
    { id: "t6", title: `Document ${URL}`, status: "done", owner: "claude-1", result: `${URL} ${TOKEN}`, branch: `deck/${TOKEN}`, files: [LONG_PATH, LONG_PATH + ".bak"] },
  );
  f.deck.setChanges("ses_main", [{ path: LONG_PATH, added: 3, removed: 1, diff: `@@ -1 +1 @@\n-${TOKEN}\n+${TOKEN}${TOKEN}\n` }]);
  f.deck.reply("ses_main", "claude-1", `See ${URL}\n\n${TOKEN}\n\n\`${LONG_PATH}\`\n\n| col | ${TOKEN} |\n| --- | --- |\n| a | b |`);
  f.deck.toolRun("ses_main", "codex-1", CMD, `${TOKEN}\n${URL}`);
  f.deck.reply("ses_main", "claude-1", `→ $codex-1 $${NAME}\n\nReview ${LONG_PATH} and run ${CMD}\n\n${TOKEN}`);
  f.deck.reply("ses_main", "codex-1", `→ $claude-1\n\ntask t6 "Document ${TOKEN}" failed: ${CMD}\n${URL}\n${"line\n".repeat(10)}`);
  f.deck.reply("ses_main", "claude-1", `| file | status |\n| --- | :-: |\n| \`${LONG_PATH}\` | ok |\n| ${URL} | ${TOKEN} |`);
  f.deck.notice("ses_main", `Deck paused this thread: ${TOKEN}`);
  f.deck.requestApproval("ses_main", "claude-1", CMD + " " + TOKEN);
}

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  stress(fake);
  const dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

// What may scroll sideways on purpose: code, diffs, tables, terminals, the
// graph and strips of tabs or chips.
const SCROLLERS = "pre, code, table, .md-table, .diff, .xterm, .graph, [role=tablist], .chips, .scroll-x";

interface Offender {
  what: string;
  text: string;
  by: number;
  kind: "escapes-card" | "escapes-window" | "clipped" | "scrolls";
}

async function offenders(page: Page): Promise<Offender[]> {
  return page.evaluate((scrollers) => {
    const out: Offender[] = [];
    const label = (el: Element): string => {
      const cls = typeof el.className === "string" ? el.className.split(/\s+/).filter((c) => c && !c.includes(":") && !c.includes("[")).slice(0, 3).join(".") : "";
      return el.tagName.toLowerCase() + (cls ? "." + cls : "");
    };
    const path = (el: Element): string => {
      const parts: string[] = [];
      for (let e: Element | null = el; e && parts.length < 4; e = e.parentElement) parts.unshift(label(e));
      return parts.join(" > ");
    };
    const visible = (el: Element, r: DOMRect) => {
      if (r.width === 0 || r.height === 0) return false;
      const s = getComputedStyle(el);
      return s.visibility !== "hidden" && s.display !== "none" && Number(s.opacity) > 0;
    };
    // A card: a box drawn around content (a border or a background), not
    // the page itself.
    const isCard = (el: Element) => {
      const s = getComputedStyle(el);
      const border = ["Left", "Right"].some((side) => parseFloat(s.getPropertyValue(`border-${side.toLowerCase()}-width`)) > 0);
      const bg = s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent";
      return (border || bg) && el !== document.body && el !== document.documentElement && s.position !== "fixed";
    };
    for (const el of Array.from(document.querySelectorAll("body *"))) {
      const r = el.getBoundingClientRect();
      if (!visible(el, r) || el.closest(scrollers) || el.closest("[aria-hidden=true], svg, canvas, .sr-only")) continue;
      const s = getComputedStyle(el);
      const hasText = Array.from(el.childNodes).some((n) => n.nodeType === 3 && (n.textContent ?? "").trim() !== "");
      const text = (el.textContent ?? "").trim().slice(0, 60);
      // Cut off by a box that clips it, the text does not reach the window.
      let clipped = false;
      for (let p = el.parentElement; p && !clipped; p = p.parentElement) {
        const ps = getComputedStyle(p);
        if (ps.overflowX !== "visible" && p.getBoundingClientRect().right <= window.innerWidth + 1) clipped = true;
      }
      if (hasText && !clipped && r.right > window.innerWidth + 1 && s.position !== "fixed") {
        out.push({ what: path(el), text, by: Math.round(r.right - window.innerWidth), kind: "escapes-window" });
      }
      if (hasText) {
        for (let p = el.parentElement; p; p = p.parentElement) {
          if (getComputedStyle(p).overflowX !== "visible") break; // a clip or scroll box handles it below
          if (!isCard(p)) continue;
          const pr = p.getBoundingClientRect();
          if (r.right > pr.right + 1 || r.left < pr.left - 1) {
            out.push({ what: path(el), text, by: Math.round(Math.max(r.right - pr.right, pr.left - r.left)), kind: "escapes-card" });
          }
          break;
        }
      }
      const over = el.scrollWidth - el.clientWidth;
      if (over > 1 && el.clientWidth > 0) {
        if ((s.overflowX === "hidden" || s.overflowX === "clip") && s.textOverflow !== "ellipsis" && hasText) {
          out.push({ what: path(el), text, by: over, kind: "clipped" });
        } else if (s.overflowX === "auto" || s.overflowX === "scroll") {
          out.push({ what: path(el), text, by: over, kind: "scrolls" });
        }
      }
    }
    const doc = document.scrollingElement;
    if (doc && doc.scrollWidth > window.innerWidth + 1) {
      out.push({ what: "document", text: "", by: doc.scrollWidth - window.innerWidth, kind: "scrolls" });
    }
    return out;
  }, SCROLLERS);
}

async function roomTab(page: Page, name: string): Promise<void> {
  const panel = page.getByRole("complementary", { name: "Side panel" });
  if ((await panel.count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel.getByRole("tab", { name }).click();
}

const WIDTHS = [1360, 1024, 860];

for (const width of WIDTHS) {
  test(`nothing overflows its card at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 860 });
    await page.goto("/");
    await page.getByRole("button", { name: "Open a folder" }).click();
    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await expect(page.locator(".msg").first()).toBeVisible();
    for (const d of await page.locator("details:not([open]) > summary").all()) await d.click({ timeout: 1000 }).catch(() => {});
    const found: Array<Offender & { view: string }> = [];
    const look = async (view: string) => {
      await page.waitForTimeout(250);
      if (process.env["SHOTS"]) await page.screenshot({ path: join(process.env["SHOTS"], `overflow-${width}-${view}.png`) });
      for (const o of await offenders(page)) found.push({ ...o, view });
    };
    // The hand-offs are cards: the recipients, the task and how it ended.
    await expect(page.locator(".handoff")).toHaveCount(2);
    await expect(page.locator('.handoff[data-handoff="failed"]')).toContainText("t6");
    await expect(page.locator('.handoff[data-handoff="failed"]').getByRole("button", { name: "Show all" })).toBeVisible();
    if (process.env["SHOTS"]) {
      await page.locator(".handoff").first().scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(process.env["SHOTS"], `overflow-${width}-handoff.png`) });
    }
    await look("timeline");
    // A long title in the header and the sidebar.
    await page.locator("h1.title").dblclick();
    await page.getByLabel("Thread title").fill(`Fix ${TOKEN} in ${LONG_PATH}`);
    await page.getByLabel("Thread title").press("Enter");
    await look("title");
    for (const tab of ["Changes", "Tasks", "Agents"]) {
      await roomTab(page, tab);
      await look(tab.toLowerCase());
      if (tab === "Changes") {
        // A long path gives up its folder, never its file name.
        const name = page.locator(".file-path > span").last();
        await expect(name).toHaveText("handoff_contract_settlement_test.go");
        expect(await name.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
      }
      if (tab === "Agents") {
        // A lease is a chip per glob: the end of each stays in view, its
        // note marks it, and no chip runs out of the agents card.
        const leases = page.locator(`.crew-agent[data-agent="${NAME}"] li.lease`);
        await expect(leases).toHaveCount(3);
        const tails = leases.locator(".path-tail > span:last-child");
        await expect(tails).toHaveText(["handoff/**", "handoff_contract_settlement_test.go", "session/*.go"]);
        for (const t of await tails.all()) expect(await t.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
        await expect(leases.locator(".lease-note")).toHaveText(["exclusive", "exclusive"]);
        await expect(leases.nth(1)).toHaveAttribute("title", `${LONG_PATH} (exclusive)`);
        const card = await page.locator("ul.crew").boundingBox();
        for (const l of await leases.all()) {
          const r = await l.boundingBox();
          expect(r && card && r.x >= card.x && r.x + r.width <= card.x + card.width + 0.5).toBe(true);
        }
      }
    }
    // Every task card opened, with its result, error and files.
    await roomTab(page, "Tasks");
    for (const row of await page.getByRole("complementary", { name: "Side panel" }).locator("li.board-task > button[aria-expanded=false]").all()) await row.click({ timeout: 1000 }).catch(() => {});
    await look("tasks-open");
    await page.keyboard.press("Control+Shift+G");
    await look("graph");
    await page.keyboard.press("Control+Shift+G");
    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: /^Inbox/ }).click();
    await look("inbox");
    const seen = new Set<string>();
    const unique = found.filter((o) => {
      const k = `${o.kind} ${o.what}`;
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
    if (process.env["OVERFLOW_REPORT"]) console.log(JSON.stringify(unique, null, 1));
    expect(unique, "elements that run out of their box").toEqual([]);
  });
}
