import { describe, expect, test } from "vitest";
import type { BrowserTab } from "../shared/bridge";
import { activeTab, browserErrorText, driverLabel, hostOf, normalizeUrl, tabLabel } from "./browser";

const tab = (id: string, over: Partial<BrowserTab> = {}): BrowserTab => ({
  id,
  session_id: "s1",
  owner: "claude",
  url: "https://example.com/a",
  title: "",
  loading: false,
  can_back: false,
  can_forward: false,
  user_in_control: false,
  snapshot_version: "1",
  ...over,
});

describe("normalizeUrl", () => {
  test("keeps http and https addresses", () => {
    expect(normalizeUrl("https://example.com/x")).toBe("https://example.com/x");
    expect(normalizeUrl(" http://localhost:3000 ")).toBe("http://localhost:3000");
  });

  test("a bare host gets https, a local address http", () => {
    expect(normalizeUrl("example.com/docs")).toBe("https://example.com/docs");
    expect(normalizeUrl("localhost:5173/app")).toBe("http://localhost:5173/app");
    expect(normalizeUrl("127.0.0.1:8080")).toBe("http://127.0.0.1:8080");
  });

  test("refuses other schemes, spaces and words", () => {
    for (const bad of ["file:///etc/passwd", "javascript:alert(1)", "chrome://settings", "data:text/html,x", "ftp://x.com", "two words", "", "hello", "http://"]) {
      expect(normalizeUrl(bad)).toBeNull();
    }
  });
});

describe("activeTab", () => {
  test("prefers the picked tab, then the one the person holds, then the first", () => {
    const tabs = [tab("a"), tab("b", { user_in_control: true }), tab("c")];
    expect(activeTab(tabs, "c")?.id).toBe("c");
    expect(activeTab(tabs, "gone")?.id).toBe("b");
    expect(activeTab([tab("a"), tab("c")], null)?.id).toBe("a");
    expect(activeTab([], null)).toBeNull();
  });
});

describe("browserErrorText", () => {
  test("is empty without an error and never blank with one", () => {
    expect(browserErrorText("")).toBe("");
    expect(browserErrorText("origin_denied")).toBe("That site was not allowed.");
    expect(browserErrorText("something_new")).toBe("The browser refused that.");
  });
});

describe("labels", () => {
  test("says who drives", () => {
    expect(driverLabel(tab("a"))).toBe("claude is driving");
    expect(driverLabel(tab("a", { user_in_control: true }))).toBe("You are in control");
    expect(driverLabel(tab("a", { owner: "" }))).toBe("No agent is using this tab");
  });

  test("a tab is named by its title, else its host", () => {
    expect(tabLabel(tab("a", { title: "Docs" }))).toBe("Docs");
    expect(tabLabel(tab("a"))).toBe("example.com");
    expect(tabLabel(tab("a", { url: "" }))).toBe("New tab");
    expect(hostOf("nope")).toBe("");
  });
});
