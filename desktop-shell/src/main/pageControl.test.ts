import { describe, expect, test } from "vitest";
import { type Cdp, type ControlledPage, PageControl, PageError, parseKey, RefError } from "./pageControl";

describe("parseKey", () => {
  test("names, letters and combinations", () => {
    expect(parseKey("Enter")).toMatchObject({ key: "Enter", code: "Enter", vk: 13, text: "\r", modifiers: 0 });
    expect(parseKey("Esc")).toMatchObject({ key: "Escape", vk: 27 });
    expect(parseKey("ArrowDown")).toMatchObject({ key: "ArrowDown", vk: 40, text: undefined });
    expect(parseKey("f5")).toMatchObject({ key: "F5", vk: 116 });
    expect(parseKey("a")).toMatchObject({ key: "a", code: "KeyA", vk: 65, text: "a" });
    expect(parseKey("Shift+a")).toMatchObject({ key: "A", modifiers: 8, text: "A" });
    expect(parseKey("7")).toMatchObject({ code: "Digit7", text: "7" });
    // A shortcut types nothing and runs the editing command.
    expect(parseKey("Control+A")).toMatchObject({ modifiers: 2, text: undefined, commands: ["selectAll"] });
    expect(parseKey("Meta+c")).toMatchObject({ modifiers: 4, commands: ["copy"] });
    expect(parseKey("Control+Shift+Tab")).toMatchObject({ key: "Tab", modifiers: 10 });
  });

  test("rejects what is not a key", () => {
    for (const s of ["", "Hyper+Q", "Enter+", "Control+", "NotAKey", "ab"]) expect(parseKey(s), s).toBeNull();
  });
});

// A page whose protocol answers come from a table, and that records the calls.
function page(answers: Record<string, (p: Record<string, unknown>) => unknown>) {
  const calls: [string, Record<string, unknown>][] = [];
  const cdp: Cdp = {
    async send(method, params = {}) {
      const p = params as Record<string, unknown>;
      calls.push([method, p]);
      const f = answers[method];
      if (f === undefined) return {};
      return f(p);
    },
  };
  const cp: ControlledPage = {
    cdp,
    url: () => "https://x.example/",
    title: () => "X",
    loading: () => false,
    epoch: () => 0,
    whenLoaded: async () => true,
  };
  return { control: new PageControl(cp), calls };
}

describe("refs and fields", () => {
  const field = (role: string, name: string, id: number, value?: string) => ({
    nodeId: `n${id}`,
    parentId: "1",
    role: { value: role },
    name: { value: name },
    ...(value === undefined ? {} : { value: { value } }),
    backendDOMNodeId: id,
    childIds: [],
  });
  const withNode = (extra: Record<string, (p: Record<string, unknown>) => unknown> = {}, nodes: unknown[] = []) =>
    page({
      "Accessibility.getFullAXTree": () => ({
        nodes: [
          { nodeId: "1", role: { value: "RootWebArea" }, childIds: ["n5", "n6"] },
          field("textbox", "Email", 5, "ada@example.com"),
          field("combobox", "Country", 6, "Chile"),
          ...nodes,
        ],
      }),
      "DOM.getDocument": () => ({ root: { nodeId: 1 } }),
      "DOM.querySelectorAll": () => ({ nodeIds: [] }),
      "DOM.describeNode": () => ({ node: { attributes: ["type", "text"] } }),
      "DOM.resolveNode": (p) => ({ object: { objectId: `o${p["backendNodeId"]}` } }),
      "Runtime.callFunctionOn": () => ({ result: { value: true } }),
      ...extra,
    });

  test("a text field says only that it is filled; a choice shows", async () => {
    const { control } = withNode();
    const text = await control.snapshot({});
    expect(text).toContain('textbox [e1] "Email" = (filled)');
    expect(text).toContain('combobox [e2] "Country" = "Chile"');
    expect(text).not.toContain("ada@example.com");
  });

  test("when the secret fields cannot be found no value is shown at all", async () => {
    const { control } = withNode({ "DOM.querySelectorAll": () => Promise.reject(new Error("no document")) });
    const text = await control.snapshot({});
    expect(text).toContain('textbox [e1] "Email" = (hidden)');
    expect(text).toContain('combobox [e2] "Country" = (hidden)');
  });

  test("every secret field is hidden, not only the first few", async () => {
    const ids = Array.from({ length: 80 }, (_, i) => 100 + i);
    const { control, calls } = withNode(
      {
        "DOM.querySelectorAll": () => ({ nodeIds: ids }),
        "DOM.describeNode": (p) => ({ node: { backendNodeId: p["nodeId"] } }),
      },
      [],
    );
    await control.snapshot({});
    expect(calls.filter(([m]) => m === "DOM.describeNode")).toHaveLength(80);
  });

  test("typing replaces by default and appends on request, and never says what it typed", async () => {
    const { control, calls } = withNode();
    await control.snapshot({});
    expect(await control.type("e1", "hello", false)).toBe("typed 5 characters into e1.");
    expect(await control.type("e1", "!", true)).toBe("typed 1 characters into e1.");
    const fills = calls.filter(([m, p]) => m === "Runtime.callFunctionOn" && String(p["functionDeclaration"]).includes("select")).map(([, p]) => (p["arguments"] as { value: boolean }[])[0]!.value);
    expect(fills).toEqual([false, true]);
    expect(calls.filter(([m]) => m === "Input.insertText").map(([, p]) => p["text"])).toEqual(["hello", "!"]);
  });

  test("passwords, card fields and codes are the person's to type", async () => {
    for (const attrs of [["type", "password"], ["autocomplete", "cc-number"], ["autocomplete", "one-time-code"], ["autocomplete", "new-password"]]) {
      const { control, calls } = withNode({ "DOM.describeNode": () => ({ node: { attributes: attrs } }) });
      await control.snapshot({});
      const err = await control.type("e1", "secret", false).catch((e: unknown) => e);
      expect(err, attrs.join("=")).toBeInstanceOf(PageError);
      expect((err as PageError).code).toBe("user_in_control");
      expect(calls.some(([m]) => m === "Input.insertText")).toBe(false);
    }
  });

  test("a select that has no such option lists the options", async () => {
    const { control } = withNode({
      "Runtime.callFunctionOn": (p) =>
        String(p["functionDeclaration"]).includes("isConnected")
          ? { result: { value: true } }
          : { result: { value: { err: 'no option "Mars"', options: ["France", "Spain"] } } },
    });
    await control.snapshot({});
    await expect(control.select("e2", "Mars")).rejects.toThrow(/no option "Mars"\. Options: "France", "Spain"/);
  });

  test("an element with no box cannot be clicked, and says so", async () => {
    const { control } = withNode({ "DOM.getContentQuads": () => ({ quads: [] }) });
    await control.snapshot({});
    const err = await control.click("e1").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RefError);
    expect((err as Error).message).toContain("e1 is not visible");
  });

  test("a covered element is not clicked", async () => {
    const { control, calls } = withNode({
      "DOM.getContentQuads": () => ({ quads: [[0, 0, 100, 0, 100, 40, 0, 40]] }),
      "DOM.getNodeForLocation": () => ({ backendNodeId: 99 }),
      "Runtime.callFunctionOn": (p) => ({ result: { value: !String(p["functionDeclaration"]).includes("contains") } }),
    });
    await control.snapshot({});
    await expect(control.click("e1")).rejects.toThrow(/covered or has moved/);
    expect(calls.some(([m]) => m === "Input.dispatchMouseEvent")).toBe(false);
  });

  test("a click presses and releases, and a ref the snapshot never gave is stale", async () => {
    const { control, calls } = withNode({
      "DOM.getContentQuads": () => ({ quads: [[0, 0, 100, 0, 100, 40, 0, 40]] }),
      "DOM.getNodeForLocation": () => ({ backendNodeId: 5 }),
    });
    await control.snapshot({});
    expect(await control.click("e1")).toBe("clicked e1.");
    expect(calls.filter(([m]) => m === "Input.dispatchMouseEvent").map(([, p]) => p["type"])).toEqual(["mousePressed", "mouseReleased"]);
    const err = await control.click("e9").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RefError);
    expect((err as RefError).code).toBe("stale_snapshot");
  });

  test("waiting for text gives up with the reason, and a wait of ms waits", async () => {
    const { control } = withNode({ "Runtime.evaluate": () => ({ result: { value: false } }) });
    await expect(control.wait({ text: "Done", timeout: 1 })).rejects.toThrow('waited 1s: the page does not show "Done".');
    const t = Date.now();
    expect(await control.wait({ ms: 120 })).toBe("waited 120 ms.");
    expect(Date.now() - t).toBeGreaterThanOrEqual(100);
  });
});
