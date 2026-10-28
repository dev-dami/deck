import { type AxNode, buildOutline, RefTable, renderOutline } from "./outline";

// What an agent does to a page, over the Chrome DevTools Protocol: see it
// (snapshot), click, type, press, scroll, choose, wait, take a picture. The
// same code drives a tab of the app's Chromium (through webContents.debugger)
// and a page of any other Chromium (the tests).
//
// A ref (e12) names an element of the page's latest snapshot by its DOM
// node. It is good until the document is replaced or the node leaves the
// page; the op then fails and says to take a snapshot. Which snapshot an
// agent may still act on (its snapshot_version) is the browser's business.

export interface Cdp {
  send(method: string, params?: object): Promise<unknown>;
}

/** The page under control, as the control needs to see it. */
export interface ControlledPage {
  cdp: Cdp;
  url(): string;
  title(): string;
  loading(): boolean;
  /** Changes whenever the main frame gets a new document. */
  epoch(): number;
  /** True once the page is not loading; false when ms ran out. */
  whenLoaded(ms: number): Promise<boolean>;
}

export type PageErrorCode = "stale_snapshot" | "invalid_request" | "user_in_control";

/** An op that cannot be done as asked; the code is what the agent is told. */
export class PageError extends Error {
  constructor(
    message: string,
    readonly code: PageErrorCode = "invalid_request",
  ) {
    super(message);
  }
}

/** A ref that is gone or never was: the agent must take a snapshot. */
export class RefError extends PageError {
  constructor(message: string) {
    super(message, "stale_snapshot");
  }
}


const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

// Keys by name: DOM key, code, Windows virtual key code, and the text it types.
const KEYS: Record<string, { key: string; code: string; vk: number; text?: string }> = {
  Enter: { key: "Enter", code: "Enter", vk: 13, text: "\r" },
  Tab: { key: "Tab", code: "Tab", vk: 9 },
  Escape: { key: "Escape", code: "Escape", vk: 27 },
  Backspace: { key: "Backspace", code: "Backspace", vk: 8 },
  Delete: { key: "Delete", code: "Delete", vk: 46 },
  Space: { key: " ", code: "Space", vk: 32, text: " " },
  ArrowUp: { key: "ArrowUp", code: "ArrowUp", vk: 38 },
  ArrowDown: { key: "ArrowDown", code: "ArrowDown", vk: 40 },
  ArrowLeft: { key: "ArrowLeft", code: "ArrowLeft", vk: 37 },
  ArrowRight: { key: "ArrowRight", code: "ArrowRight", vk: 39 },
  Home: { key: "Home", code: "Home", vk: 36 },
  End: { key: "End", code: "End", vk: 35 },
  PageUp: { key: "PageUp", code: "PageUp", vk: 33 },
  PageDown: { key: "PageDown", code: "PageDown", vk: 34 },
  Insert: { key: "Insert", code: "Insert", vk: 45 },
};
for (let i = 1; i <= 12; i++) KEYS[`F${i}`] = { key: `F${i}`, code: `F${i}`, vk: 111 + i };
const ALIASES: Record<string, string> = { Return: "Enter", Esc: "Escape", Up: "ArrowUp", Down: "ArrowDown", Left: "ArrowLeft", Right: "ArrowRight", " ": "Space" };

const MODS: Record<string, number> = { alt: 1, option: 1, control: 2, ctrl: 2, meta: 4, cmd: 4, command: 4, super: 4, shift: 8 };

// Editing shortcuts the page's text fields run from the key event itself.
const COMMANDS: Record<string, string> = { a: "selectAll", c: "copy", v: "paste", x: "cut", z: "undo", y: "redo" };

export interface KeyEvent {
  key: string;
  code: string;
  vk: number;
  text: string | undefined;
  modifiers: number;
  commands: string[];
}

/** Parses "Enter", "a" or "Control+Shift+A" into what the page is sent. */
export function parseKey(spec: string): KeyEvent | null {
  const parts = spec.split("+");
  // A lone "+" is the plus key.
  if (spec === "+") parts.splice(0, 2, "+");
  let name = parts.pop() ?? "";
  if (name === "" && spec.endsWith("++")) name = "+";
  let modifiers = 0;
  for (const p of parts) {
    const m = MODS[p.toLowerCase()];
    if (m === undefined) return null;
    modifiers |= m;
  }
  name = ALIASES[name] ?? name;
  const named = KEYS[name] ?? KEYS[name.charAt(0).toUpperCase() + name.slice(1)];
  if (named !== undefined) {
    return { ...named, text: modifiers & ~8 ? undefined : named.text, modifiers, commands: [] };
  }
  if ([...name].length !== 1) return null;
  const upper = name.toUpperCase();
  const isLetter = /^[a-z]$/i.test(name);
  const isDigit = /^[0-9]$/.test(name);
  const shifted = (modifiers & 8) !== 0 && isLetter ? upper : name;
  return {
    key: shifted,
    code: isLetter ? `Key${upper}` : isDigit ? `Digit${name}` : "",
    vk: isLetter || isDigit ? upper.charCodeAt(0) : name.charCodeAt(0),
    text: modifiers & ~8 ? undefined : shifted,
    modifiers,
    commands: modifiers & 6 && (modifiers & ~6) === 0 ? [COMMANDS[name.toLowerCase()] ?? ""].filter(Boolean) : [],
  };
}

interface RemoteObject {
  objectId?: string;
  value?: unknown;
  description?: string;
}

interface Evaluated {
  result: RemoteObject;
  exceptionDetails?: { text?: string; exception?: RemoteObject };
}

const FILL = `function (append) {
  this.focus();
  if (!append) {
    if (typeof this.select === 'function' && !this.isContentEditable) this.select();
    else { const s = getSelection(); s.selectAllChildren(this); }
  } else if (typeof this.setSelectionRange === 'function') {
    try { const n = this.value.length; this.setSelectionRange(n, n); } catch (e) {}
  } else { const s = getSelection(); s.selectAllChildren(this); s.collapseToEnd(); }
  return true;
}`;

const PICK = `function (want) {
  if (this.tagName !== 'SELECT') return { err: 'not a select element (' + this.tagName.toLowerCase() + ')' };
  const w = String(want).trim().toLowerCase();
  const opts = Array.from(this.options);
  const hit = opts.find(o => o.value.toLowerCase() === w) || opts.find(o => o.label.trim().toLowerCase() === w)
    || opts.find(o => o.label.trim().toLowerCase().includes(w));
  if (!hit) return { err: 'no option ' + JSON.stringify(want), options: opts.slice(0, 12).map(o => o.label.trim()) };
  if (hit.disabled) return { err: 'option ' + JSON.stringify(hit.label.trim()) + ' is disabled' };
  this.value = hit.value;
  this.dispatchEvent(new Event('input', { bubbles: true }));
  this.dispatchEvent(new Event('change', { bubbles: true }));
  return { picked: hit.label.trim() };
}`;

const SCROLLED = `(() => { const d = document.documentElement; const max = Math.max(0, d.scrollHeight - innerHeight);
  return { y: Math.round(scrollY), max: Math.round(max) }; })()`;

// The fields whose content an agent must not read or type: passwords,
// payment cards and one-time codes.
const SECRET_FIELDS = 'input[type="password"], input[autocomplete^="cc-"], input[autocomplete="one-time-code"], input[autocomplete$="-password"]';
const SECRET_AUTOCOMPLETE = /^(cc-|one-time-code$|.*-password$)/i;

export const WAIT_LIMITS = { defaultSec: 10, pollMs: 150, maxShot: 1024 } as const;

export interface Shot {
  data: Uint8Array;
  mime: string;
  width: number;
  height: number;
}

interface Element {
  node: number;
  objectId: string;
  attrs: Map<string, string>;
}

/** The control of one page: its refs and the ops on them. */
export class PageControl {

  private refEpoch: number;
  private table = new RefTable();

  /** The page, which the browser may swap for each op (its channel carries the op's abort). */
  constructor(public page: ControlledPage) {
    this.refEpoch = page.epoch();
  }

  private async cdp<T>(method: string, params?: object): Promise<T> {
    return (await this.page.cdp.send(method, params)) as T;
  }

  /** The refs reset when the page got a new document. */
  private fresh(): boolean {
    if (this.refEpoch === this.page.epoch()) return false;
    this.table = new RefTable();
    this.refEpoch = this.page.epoch();
    return true;
  }

  /** The refs the latest snapshot handed out. */
  get count(): number {
    return this.table.size;
  }

  async snapshot(opts: { interactive?: boolean; max?: number }): Promise<string> {
    this.fresh();
    await this.cdp("Accessibility.enable");
    const { nodes } = await this.cdp<{ nodes: AxNode[] }>("Accessibility.getFullAXTree");
    const secret = await this.secrets();
    const o = buildOutline(nodes, {
      refs: this.table,
      url: this.page.url(),
      ...(secret === null ? { hideAll: true } : { secret }),
      ...(opts.interactive === undefined ? {} : { interactive: opts.interactive }),
      ...(opts.max === undefined ? {} : { max: opts.max }),
    });
    return renderOutline(o, { title: this.page.title(), url: this.page.url(), loading: this.page.loading() }, opts.interactive === true);
  }

  /**
   * The backend ids of the fields whose content must not reach an agent, all
   * of them. null when they could not be found: then no field's value is
   * shown at all.
   */
  private async secrets(): Promise<Set<number> | null> {
    try {
      const { root } = await this.cdp<{ root: { nodeId: number } }>("DOM.getDocument", { depth: 0 });
      const { nodeIds } = await this.cdp<{ nodeIds: number[] }>("DOM.querySelectorAll", { nodeId: root.nodeId, selector: SECRET_FIELDS });
      const described = await Promise.all(nodeIds.map((nodeId) => this.cdp<{ node: { backendNodeId: number } }>("DOM.describeNode", { nodeId })));
      return new Set(described.map((d) => d.node.backendNodeId));
    } catch {
      return null;
    }
  }

  /** The element behind a ref, or why it cannot be used. */
  private async element(ref: string): Promise<Element> {
    if (this.fresh()) throw new RefError(`${ref}: the page loaded a new document (${this.page.url()}); its refs are gone. Call browser.snapshot.`);
    const node = this.table.node(ref);
    if (node === undefined) throw new RefError(`${ref} is not in the latest snapshot of this tab. Call browser.snapshot to get the current refs.`);
    let objectId: string | undefined;
    try {
      const { object } = await this.cdp<{ object: RemoteObject }>("DOM.resolveNode", { backendNodeId: node });
      objectId = object.objectId;
      if (objectId === undefined) throw new Error("gone");
      const alive = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
        objectId,
        functionDeclaration: "function () { return this.isConnected; }",
        returnByValue: true,
      });
      if (alive.result.value !== true) throw new Error("detached");
      const { node: described } = await this.cdp<{ node: { attributes?: string[] } }>("DOM.describeNode", { backendNodeId: node });
      const attrs = new Map<string, string>();
      const list = described.attributes ?? [];
      for (let i = 0; i + 1 < list.length; i += 2) attrs.set((list[i] ?? "").toLowerCase(), list[i + 1] ?? "");
      return { node, objectId, attrs };
    } catch (e) {
      if (objectId !== undefined) this.release(objectId);
      if (e instanceof PageError) throw e;
      throw new RefError(`${ref} is stale: the element left the page. Call browser.snapshot for the current refs.`);
    }
  }

  private release(objectId: string): void {
    void this.cdp("Runtime.releaseObject", { objectId }).catch(() => {});
  }

  private async withElement<T>(ref: string, f: (e: Element) => Promise<T>): Promise<T> {
    const e = await this.element(ref);
    try {
      return await f(e);
    } finally {
      this.release(e.objectId);
    }
  }

  private async centre(node: number, ref: string): Promise<{ x: number; y: number }> {
    try {
      await this.cdp("DOM.scrollIntoViewIfNeeded", { backendNodeId: node });
    } catch {
      // Elements with no box fail here; the quads say so below.
    }
    const { quads } = await this.cdp<{ quads: number[][] }>("DOM.getContentQuads", { backendNodeId: node });
    const q = quads.find((x) => x.length === 8 && Math.abs((x[2] ?? 0) - (x[0] ?? 0)) > 0.5 && Math.abs((x[5] ?? 0) - (x[1] ?? 0)) > 0.5);
    if (q === undefined) throw new RefError(`${ref} is not visible (hidden or empty). Scroll to it or open what contains it.`);
    return { x: ((q[0] ?? 0) + (q[2] ?? 0) + (q[4] ?? 0) + (q[6] ?? 0)) / 4, y: ((q[1] ?? 0) + (q[3] ?? 0) + (q[5] ?? 0) + (q[7] ?? 0)) / 4 };
  }

  /** Whether the element is what a click at its centre would reach. */
  private async reaches(node: number, x: number, y: number, ref: string): Promise<void> {
    const hit = await this.cdp<{ backendNodeId?: number }>("DOM.getNodeForLocation", { x: Math.round(x), y: Math.round(y), includeUserAgentShadowDOM: false });
    if (hit.backendNodeId === node) return;
    const objects: string[] = [];
    try {
      if (hit.backendNodeId === undefined) throw new Error("nothing there");
      const target = await this.cdp<{ object: RemoteObject }>("DOM.resolveNode", { backendNodeId: node });
      const over = await this.cdp<{ object: RemoteObject }>("DOM.resolveNode", { backendNodeId: hit.backendNodeId });
      objects.push(target.object.objectId ?? "", over.object.objectId ?? "");
      const r = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
        objectId: target.object.objectId,
        arguments: [{ objectId: over.object.objectId }],
        functionDeclaration: "function (hit) { return this === hit || this.contains(hit); }",
        returnByValue: true,
      });
      if (r.result.value === true) return;
    } catch {
      // Treated as covered below.
    } finally {
      for (const o of objects) if (o !== "") this.release(o);
    }
    throw new RefError(`${ref} is covered or has moved; something else would get the click. Call browser.snapshot.`);
  }

  /** After an action: lets a navigation it started finish. */
  private async settle(before: number): Promise<string> {
    const started = Date.now();
    while (Date.now() - started < 350) {
      if (this.page.epoch() !== before || this.page.loading()) {
        await this.page.whenLoaded(8000);
        await sleep(60);
        return `\npage: ${this.page.title() || "(untitled)"}  ${this.page.url()}`;
      }
      await sleep(40);
    }
    return "";
  }

  async click(ref: string, double = false): Promise<string> {
    return this.withElement(ref, async ({ node }) => {
      const { x, y } = await this.centre(node, ref);
      await this.reaches(node, x, y, ref);
      const before = this.page.epoch();
      for (let n = 1; n <= (double ? 2 : 1); n++) {
        await this.cdp("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", buttons: 1, clickCount: n });
        // Released even when the press navigated: the button must not stay down.
        await this.cdp("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", buttons: 0, clickCount: n });
      }
      return `clicked ${ref}.${await this.settle(before)}`;
    });
  }

  async type(ref: string, text: string, append: boolean): Promise<string> {
    return this.withElement(ref, async ({ objectId, attrs }) => {
      if (attrs.get("type")?.toLowerCase() === "password" || SECRET_AUTOCOMPLETE.test(attrs.get("autocomplete") ?? "")) {
        throw new PageError("Use Take control to enter passwords, payment details and codes.", "user_in_control");
      }
      const r = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
        objectId,
        functionDeclaration: FILL,
        arguments: [{ value: append }],
        returnByValue: true,
      });
      if (r.exceptionDetails !== undefined) throw new RefError(`${ref} cannot take text.`);
      if (text !== "") await this.cdp("Input.insertText", { text });
      // Never says what was typed: it may be a secret.
      return `typed ${[...text].length} characters into ${ref}.`;
    });
  }

  private async key(k: KeyEvent): Promise<void> {
    const base = { key: k.key, code: k.code, windowsVirtualKeyCode: k.vk, nativeVirtualKeyCode: k.vk, modifiers: k.modifiers };
    await this.cdp("Input.dispatchKeyEvent", {
      type: k.text === undefined ? "rawKeyDown" : "keyDown",
      ...base,
      ...(k.text === undefined ? {} : { text: k.text, unmodifiedText: k.text }),
      ...(k.commands.length > 0 ? { commands: k.commands } : {}),
    });
    await this.cdp("Input.dispatchKeyEvent", { type: "keyUp", ...base });
  }

  async press(spec: string): Promise<string> {
    const k = parseKey(spec);
    if (k === null) throw new PageError(`${JSON.stringify(spec)} is not a key. Use a name like Enter, Tab, Escape, ArrowDown, or Control+A.`);
    const before = this.page.epoch();
    await this.key(k);
    return `pressed ${spec}.${await this.settle(before)}`;
  }

  private async position(): Promise<string> {
    const r = await this.cdp<Evaluated>("Runtime.evaluate", { expression: SCROLLED, returnByValue: true });
    const v = r.result.value as { y: number; max: number } | undefined;
    if (v === undefined) return "";
    return v.max === 0 ? "the page does not scroll" : `scroll position ${v.y} of ${v.max} px (${Math.round((v.y / v.max) * 100)}%)`;
  }

  async scroll(a: { dy?: number; to?: string; ref?: string }): Promise<string> {
    if (a.ref !== undefined && a.ref !== "") {
      await this.withElement(a.ref, async ({ node }) => {
        await this.cdp("DOM.scrollIntoViewIfNeeded", { backendNodeId: node });
      });
    } else if (a.to === "top" || a.to === "bottom") {
      await this.cdp("Runtime.evaluate", { expression: `scrollTo(0, ${a.to === "top" ? 0 : "document.documentElement.scrollHeight"})` });
    } else {
      const m = await this.cdp<{ cssVisualViewport: { clientWidth: number; clientHeight: number } }>("Page.getLayoutMetrics");
      await this.cdp("Input.dispatchMouseEvent", {
        type: "mouseWheel",
        x: m.cssVisualViewport.clientWidth / 2,
        y: m.cssVisualViewport.clientHeight / 2,
        deltaX: 0,
        deltaY: a.dy ?? 0,
      });
    }
    await sleep(250);
    return `scrolled. ${await this.position()}`;
  }

  async select(ref: string, value: string): Promise<string> {
    return this.withElement(ref, async ({ objectId }) => {
      const r = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
        objectId,
        functionDeclaration: PICK,
        arguments: [{ value }],
        returnByValue: true,
      });
      const v = r.result.value as { err?: string; options?: string[]; picked?: string } | undefined;
      if (v?.err !== undefined) {
        throw new PageError(`${ref}: ${v.err}${v.options === undefined ? "" : `. Options: ${v.options.map((o) => JSON.stringify(o)).join(", ")}`}`);
      }
      return `chose ${JSON.stringify(v?.picked ?? value)} in ${ref}.`;
    });
  }

  async wait(a: { text?: string; load?: boolean; ms?: number; timeout?: number }): Promise<string> {
    const limit = (a.timeout ?? WAIT_LIMITS.defaultSec) * 1000;
    if (a.text !== undefined && a.text !== "") {
      const want = JSON.stringify(a.text);
      const started = Date.now();
      for (;;) {
        const r = await this.cdp<Evaluated>("Runtime.evaluate", {
          expression: `!!document.body && document.body.innerText.includes(${want})`,
          returnByValue: true,
        });
        if (r.result.value === true) return `the page shows ${want} (after ${Date.now() - started} ms).`;
        if (Date.now() - started >= limit) throw new PageError(`waited ${limit / 1000}s: the page does not show ${want}.`);
        await sleep(WAIT_LIMITS.pollMs);
      }
    }
    if (a.load === true) {
      const ok = await this.page.whenLoaded(limit);
      if (!ok) throw new PageError(`waited ${limit / 1000}s: the page is still loading.`);
      return `the page finished loading: ${this.page.title() || "(untitled)"}  ${this.page.url()}`;
    }
    const ms = Math.max(0, a.ms ?? 0);
    await sleep(ms);
    return `waited ${ms} ms.`;
  }

  async screenshot(ref?: string): Promise<Shot> {
    const m = await this.cdp<{ cssVisualViewport: { clientWidth: number; clientHeight: number } }>("Page.getLayoutMetrics");
    let clip = { x: 0, y: 0, width: Math.max(1, m.cssVisualViewport.clientWidth), height: Math.max(1, m.cssVisualViewport.clientHeight) };
    if (ref !== undefined && ref !== "") {
      clip = await this.withElement(ref, async ({ node }) => {
        await this.centre(node, ref);
        const { model } = await this.cdp<{ model: { border: number[] } }>("DOM.getBoxModel", { backendNodeId: node });
        const b = model.border;
        const xs = [b[0] ?? 0, b[2] ?? 0, b[4] ?? 0, b[6] ?? 0];
        const ys = [b[1] ?? 0, b[3] ?? 0, b[5] ?? 0, b[7] ?? 0];
        return { x: Math.min(...xs), y: Math.min(...ys), width: Math.max(1, Math.max(...xs) - Math.min(...xs)), height: Math.max(1, Math.max(...ys) - Math.min(...ys)) };
      });
    }
    const scale = Math.min(1, WAIT_LIMITS.maxShot / Math.max(clip.width, clip.height));
    const r = await this.cdp<{ data: string }>("Page.captureScreenshot", { format: "jpeg", quality: 60, clip: { ...clip, scale } });
    return { data: Buffer.from(r.data, "base64"), mime: "image/jpeg", width: Math.round(clip.width * scale), height: Math.round(clip.height * scale) };
  }

  /** The node behind a ref of the latest snapshot, for the browser's own checks. */
  node(ref: string): number | undefined {
    return this.table.node(ref);
  }
}
