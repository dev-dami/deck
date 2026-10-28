import { describe, expect, test } from "vitest";
import { type Cdp, type ControlledPage, PageControl, parseKey, RefError } from "./pageControl";

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
    navigate: async () => ({ ok: true }),
    history: async () => true,
    whenLoaded: async () => true,
  };
  return { control: new PageControl(cp), calls };
}

describe("script", () => {
  test("an expression returns its value, a body with return works too, errors are reported", async () => {
    let n = 0;
    const { control, calls } = page({
      "Runtime.evaluate": (p) => {
        n++;
        const e = String(p["expression"]);
        if (e.includes("return 7")) return n === 2 ? { result: {}, exceptionDetails: { exception: { description: "SyntaxError: Illegal return statement" } } } : { result: { value: 7 } };
        if (e.includes("boom")) return { result: {}, exceptionDetails: { exception: { description: "ReferenceError: boom is not defined\n    at <anonymous>:2:1" } } };
        return { result: { value: { a: [1, 2] } } };
      },
    });
    expect(await control.evaluate("({a:[1,2]})")).toBe('{"a":[1,2]}');
    // The first try as an expression is a syntax error: it is run as a body.
    n = 1;
    expect(await control.evaluate("return 7")).toBe("7");
    expect(calls.length).toBeGreaterThanOrEqual(3);
    await expect(control.evaluate("boom()")).rejects.toThrow(/the script threw: ReferenceError: boom is not defined$/);
  });

  test("a long value is cut", async () => {
    const { control } = page({ "Runtime.evaluate": () => ({ result: { value: "x".repeat(9000) } }) });
    const out = await control.evaluate("big()");
    expect(out.length).toBeLessThan(4100);
    expect(out).toContain("cut at 4000");
  });
});

describe("refs and fields", () => {
  const withNode = (extra: Record<string, (p: Record<string, unknown>) => unknown> = {}) =>
    page({
      "Accessibility.getFullAXTree": () => ({
        nodes: [
          { nodeId: "1", role: { value: "RootWebArea" }, childIds: ["2", "3"] },
          { nodeId: "2", parentId: "1", role: { value: "textbox" }, name: { value: "Email" }, backendDOMNodeId: 5, childIds: [] },
          { nodeId: "3", parentId: "1", role: { value: "combobox" }, name: { value: "Country" }, backendDOMNodeId: 6, childIds: [] },
        ],
      }),
      "DOM.getDocument": () => ({ root: { nodeId: 1 } }),
      "DOM.querySelectorAll": () => ({ nodeIds: [] }),
      "DOM.resolveNode": (p) => ({ object: { objectId: `o${p["backendNodeId"]}` } }),
      "Runtime.callFunctionOn": (p) => (String(p["functionDeclaration"]).includes("isConnected") ? { result: { value: true } } : { result: { value: true } }),
      ...extra,
    });

  test("typing replaces by default, appends on request, and submits with Enter", async () => {
    const { control, calls } = withNode();
    await control.snapshot("t1", {});
    expect(await control.type("e1", "hello", false, false)).toBe("typed 5 characters into e1.");
    expect(await control.type("e1", "!", true, true)).toBe("typed 1 characters into e1 and pressed Enter.");
    const fills = calls.filter(([m, p]) => m === "Runtime.callFunctionOn" && String(p["functionDeclaration"]).includes("select")).map(([, p]) => (p["arguments"] as { value: boolean }[])[0]!.value);
    expect(fills).toEqual([false, true]);
    expect(calls.filter(([m]) => m === "Input.insertText").map(([, p]) => p["text"])).toEqual(["hello", "!"]);
    const keys = calls.filter(([m]) => m === "Input.dispatchKeyEvent").map(([, p]) => `${p["type"]}:${p["key"]}`);
    expect(keys).toEqual(["keyDown:Enter", "keyUp:Enter"]);
  });

  test("a select that has no such option lists the options", async () => {
    const { control } = withNode({
      "Runtime.callFunctionOn": (p) =>
        String(p["functionDeclaration"]).includes("isConnected")
          ? { result: { value: true } }
          : { result: { value: { err: 'no option "Mars"', options: ["France", "Spain"] } } },
    });
    await control.snapshot("t1", {});
    await expect(control.select("e2", "Mars")).rejects.toThrow(/no option "Mars"\. Options: "France", "Spain"/);
  });

  test("an element with no box cannot be clicked, and says so", async () => {
    const { control } = withNode({ "DOM.getContentQuads": () => ({ quads: [] }) });
    await control.snapshot("t1", {});
    const err = await control.click("e1").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RefError);
    expect((err as Error).message).toContain("e1 is not visible");
  });

  test("waiting for text gives up with the reason, and a wait of ms waits", async () => {
    const { control } = withNode({ "Runtime.evaluate": () => ({ result: { value: false } }) });
    await expect(control.wait({ text: "Done", timeout: 1 })).rejects.toThrow('waited 1s: the page does not show "Done".');
    const t = Date.now();
    expect(await control.wait({ ms: 120 })).toBe("waited 120 ms.");
    expect(Date.now() - t).toBeGreaterThanOrEqual(100);
  });
});
