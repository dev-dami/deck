import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// Named agents, "@" mentions and "#" context
// (docs/design/agents-and-mentions.md) over a fake Deck that keeps
// profiles: the composer's pickers, a named member's role in the room, and
// the profiles editor in Settings > Agents.
let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;

const CHARLES = { name: "charles", kind: "claude", role: "reviewer", scope: "project", model: "opus", paths: ["pkg/remote/**"] };
const ADA = { name: "ada", kind: "codex", role: "writes the tests", scope: "global", color: "#d08770" };

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start();
  fake.serveProfiles([CHARLES, ADA]);
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

async function openMain(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
}

test("@ offers agents, profiles and people; # and @path offer files; $ agents", async ({ page }) => {
  await openMain(page);
  await composer(page).pressSequentially("@");
  const people = page.getByRole("listbox", { name: "Agents and people" });
  // Members by name, the kinds, then the profiles and the team's people,
  // each with its glyph, its name and its role.
  await expect(people.getByRole("option").filter({ hasText: "charles" })).toContainText("reviewer");
  await expect(people.getByRole("option").filter({ hasText: "ada" })).toContainText("writes the tests");
  await expect(people.getByRole("option").filter({ hasText: "vps" })).toContainText("teammate");
  await expect(people.getByRole("option").first().locator(".suggest-mark")).not.toBeEmpty();
  await expect(people.getByRole("option").filter({ hasText: "ada" }).locator(".suggest-insert")).toHaveCSS("color", "rgb(208, 135, 112)");

  await composer(page).pressSequentially("ch");
  await expect(people.getByRole("option")).toHaveCount(1);
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("@charles ");

  // "#" picks a file and puts it in as an @path pick always did.
  await composer(page).pressSequentially("look at #limit");
  const files = page.getByRole("listbox", { name: "Files" });
  await expect(files.getByRole("option").first()).toContainText("pkg/remote/auth/limit.go");
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("@charles look at `pkg/remote/auth/limit.go` ");

  // "@" before a path still offers files.
  await composer(page).fill("");
  await composer(page).pressSequentially("see @pkg/remote/auth/lim");
  await expect(files.getByRole("option").first()).toContainText("pkg/remote/auth/limit.go");
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("see `pkg/remote/auth/limit.go` ");
  await composer(page).fill("");
  await composer(page).pressSequentially("@READ");
  await expect(page.getByRole("listbox", { name: "Agents and people" })).toHaveCount(0);
  await composer(page).pressSequentially("ME.md");
  await expect(files.getByRole("option")).toHaveText([/README\.md/]);

  // "$" keeps opening the agents picker, without profiles or people.
  await composer(page).fill("");
  await composer(page).pressSequentially("$co");
  const agents = page.getByRole("listbox", { name: "Agents" });
  await expect(agents.getByRole("option").first()).toContainText("$codex-1");
  await expect(agents.getByRole("option").filter({ hasText: "charles" })).toHaveCount(0);
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("$codex-1 ");
});

test("@charles starts the profile's agent, and its role shows by its name", async ({ page }) => {
  await openMain(page);
  await composer(page).pressSequentially("@charl");
  await composer(page).press("Enter");
  await composer(page).pressSequentially("review the limiter");
  await composer(page).press("Enter");
  await expect.poll(() => fake.deck.sent.at(-1)?.text).toBe("@charles review the limiter");
  // The timeline's header and the lane strip set the role after the name.
  const reply = page.locator('[data-agent="charles"].msg').last();
  await expect(reply.locator(".msg-who")).toHaveText("charles");
  await expect(reply.locator(".agent-role")).toHaveText("reviewer");
  await expect(page.locator(".agent-role", { hasText: "reviewer" }).first()).toBeVisible();
});

test("the profiles editor lists, checks, saves and deletes profiles", async ({ page }) => {
  await openMain(page);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Agents" }).click();
  const list = page.getByRole("list", { name: "Profiles" });
  // Project profiles first, each with its role and scope.
  await expect(list.getByRole("button")).toHaveText([/charles\s*reviewer.*Project/, /ada\s*writes the tests.*Global/]);

  // A new profile: the checks show as a field is left, and on Save.
  await page.getByRole("button", { name: "New profile" }).click();
  const dialog = page.getByRole("dialog", { name: "New profile" });
  const name = dialog.getByRole("textbox", { name: "Name" });
  await name.fill("all");
  await name.blur();
  await expect(dialog.locator(".field-error")).toHaveText("“all” is reserved by Deck.");
  await name.fill("vps");
  await expect(dialog.locator(".field-error")).toHaveText("vps is a teammate. Pick another name.");
  await name.fill("dr.who");
  await expect(dialog.locator(".field-error")).toHaveText("Only letters, digits, - and _, so that @dr.who reaches it.");
  await name.fill("grace");
  await expect(dialog.locator(".field-error")).toHaveCount(0);
  await dialog.getByRole("combobox", { name: "Runs" }).selectOption("codex");
  await dialog.getByRole("textbox", { name: "Role" }).fill("implements the plan");
  await dialog.getByRole("textbox", { name: "Colour" }).fill("not a colour");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog.locator(".field-error")).toHaveText("Not a CSS colour.");
  expect(fake.named.saved).toEqual([]);
  await dialog.getByRole("textbox", { name: "Colour" }).fill("teal");
  await dialog.getByRole("textbox", { name: "Paths" }).fill("src/**\ndocs/*.md");
  await dialog.getByRole("radio", { name: "Every project" }).click();
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toHaveCount(0);
  expect(fake.named.saved).toEqual(["global:grace"]);
  expect(fake.named.profiles.find((p) => p["name"] === "grace")).toMatchObject({
    kind: "codex",
    role: "implements the plan",
    color: "teal",
    paths: ["src/**", "docs/*.md"],
    scope: "global",
  });
  await expect(list.getByRole("button", { name: "Edit grace" })).toContainText("implements the plan");

  // An edit keeps the name; Deck's refusal shows in the dialog.
  await list.getByRole("button", { name: "Edit charles" }).click();
  const edit = page.getByRole("dialog", { name: "Edit charles" });
  await expect(edit.getByRole("textbox", { name: "Paths" })).toHaveValue("pkg/remote/**");
  await edit.getByRole("textbox", { name: "Role" }).fill("reviews every change");
  fake.named.refuse = 'profile "charles": path "pkg/[a" is not a valid glob';
  await edit.getByRole("button", { name: "Save" }).click();
  await expect(edit.locator(".profile-refused")).toHaveText('profile "charles": path "pkg/[a" is not a valid glob');
  await edit.getByRole("button", { name: "Save" }).click();
  await expect(edit).toHaveCount(0);
  await expect(list.getByRole("button", { name: "Edit charles" })).toContainText("reviews every change");

  // Delete asks first.
  await list.getByRole("button", { name: "Edit ada" }).click();
  await page.getByRole("dialog", { name: "Edit ada" }).getByRole("button", { name: "Delete" }).click();
  await page.getByRole("dialog", { name: "Delete ada?" }).getByRole("button", { name: "Delete" }).click();
  await expect(list.getByRole("button", { name: "Edit ada" })).toHaveCount(0);
  expect(fake.named.deleted).toEqual(["global:ada"]);
});

test("a Deck that keeps no profiles: @ picks files and the editor says so", async ({ page }) => {
  fake.room.caps = fake.room.caps.filter((m) => !m.endsWith("Profile") && m !== "ListProfiles");
  await openMain(page);
  await composer(page).pressSequentially("look at @limit");
  await expect(page.getByRole("listbox", { name: "Files" })).toBeVisible();
  await expect(page.getByRole("listbox", { name: "Agents and people" })).toHaveCount(0);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Agents" }).click();
  await expect(page.getByText("This Deck keeps no profiles.", { exact: false })).toBeVisible();
  expect(fake.calls).not.toContain("ListProfiles");
});

// Screenshots of the "@" and "#" pickers and the profiles editor, dark and
// light, for design review: SHOTS=<dir> npx playwright test mentions; the
// e2e run skips them.
const OUT = process.env["SHOTS"] ?? "";
for (const mode of ["dark", "light"] as const) {
  test(`mentions shots ${mode}`, async ({ page }) => {
    test.skip(OUT === "", "SHOTS names the folder for the screenshots");
    const shot = (name: string) => page.screenshot({ path: join(OUT, `${mode}-${name}.png`) });
    await page.emulateMedia({ colorScheme: mode });
    await page.addInitScript((m) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m })), mode);
    await openMain(page);
    await page.mouse.move(2, 600);
    await composer(page).pressSequentially("@");
    await expect(page.getByRole("listbox", { name: "Agents and people" })).toBeVisible();
    await page.waitForTimeout(300);
    await shot("at-picker");
    await composer(page).fill("");
    await composer(page).pressSequentially("@charles look at #auth");
    await expect(page.getByRole("listbox", { name: "Files" })).toBeVisible();
    await page.waitForTimeout(400);
    await shot("hash-picker");
    await composer(page).fill("");

    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
    await page.getByRole("button", { name: "Agents" }).click();
    await expect(page.getByRole("list", { name: "Profiles" })).toBeVisible();
    await page.waitForTimeout(300);
    await shot("profiles");
    await page.getByRole("list", { name: "Profiles" }).getByRole("button", { name: "Edit charles" }).click();
    const edit = page.getByRole("dialog", { name: "Edit charles" });
    await edit.getByRole("textbox", { name: "Instructions" }).fill("Read the diff twice. Ask for a test when a fix has none.");
    await edit.getByRole("textbox", { name: "Voice" }).fill("dry, terse");
    await page.waitForTimeout(300);
    await shot("profile-edit");
    await edit.getByRole("button", { name: "Cancel" }).click();
    await page.getByRole("button", { name: "New profile" }).click();
    const fresh = page.getByRole("dialog", { name: "New profile" });
    await fresh.getByRole("textbox", { name: "Name" }).fill("codex");
    await fresh.getByRole("button", { name: "Save" }).click();
    await page.waitForTimeout(300);
    await shot("profile-new-invalid");
  });
}
