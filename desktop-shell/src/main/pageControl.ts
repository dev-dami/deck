import { type AxNode, buildOutline, RefTable, renderOutline } from "./outline";

// What an agent does to a page, over the Chrome DevTools Protocol: see it
// (snapshot), click, type, press, scroll, choose, wait, run script, take a
// picture. The same code drives a tab of the app's Chromium (through
// webContents.debugger) and a page of any other Chromium (the tests).
//
// A ref (e12) names an element of the page's latest snapshot by its DOM
// node. It is good until the document is replaced or the node leaves the
// page; the op then fails and says to take a snapshot.

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
  /** Loads url; resolves when it finished or failed (or the wait gave out). */
  navigate(url: string): Promise<{ ok: boolean; error?: string }>;
  history(dir: "back" | "forward" | "reload"): Promise<boolean>;
  /** True once the page is not loading; false when ms ran out. */
  whenLoaded(ms: number): Promise<boolean>;
}

export class RefError extends Error {}

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

// Page-side helpers, as functions run on an element (this).
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

const COVER = `function (x, y) {
  const e = document.elementFromPoint(x, y);
  if (!e || this === e || this.contains(e) || e.contains(this)) return '';
  const t = e.tagName.toLowerCase();
  const l = (e.getAttribute('aria-label') || e.innerText || '').trim().replace(/\\s+/g, ' ').slice(0, 40);
  return l ? t + ' "' + l + '"' : t;
}`;

const SCROLLED = `(() => { const d = document.documentElement; const max = Math.max(0, d.scrollHeight - innerHeight);
  return { y: Math.round(scrollY), max: Math.round(max) }; })()`;

export const WAIT_LIMITS = { defaultSec: 10, pollMs: 150, maxShot: 1024 } as const;

export interface Shot {
  data: Uint8Array;
  mime: string;
  width: number;
  height: number;
}

/** The control of one page: its refs and the ops on them. */
export class PageControl {
  private refs = new RefTable();
  private refEpoch: number;

  constructor(private readonly page: ControlledPage) {
    this.refEpoch = page.epoch();
  }

  private async cdp<T>(method: string, params?: object): Promise<T> {
    return (await this.page.cdp.send(method, params)) as T;
  }

  /** The refs reset when the page got a new document. */
  private fresh(): boolean {
    if (this.refEpoch === this.page.epoch()) return false;
    this.refs = new RefTable();
    this.refEpoch = this.page.epoch();
    return true;
  }

  async snapshot(tab: string, opts: { interactive?: boolean; max?: number }): Promise<string> {
    this.fresh();
    await this.cdp("Accessibility.enable");
    const { nodes } = await this.cdp<{ nodes: AxNode[] }>("Accessibility.getFullAXTree");
    const secret = await this.secrets();
    const o = buildOutline(nodes, {
      refs: this.refs,
      url: this.page.url(),
      secret,
      ...(opts.interactive === undefined ? {} : { interactive: opts.interactive }),
      ...(opts.max === undefined ? {} : { max: opts.max }),
    });
    return renderOutline(o, { tab, title: this.page.title(), url: this.page.url(), loading: this.page.loading() }, opts.interactive === true);
  }

  /** The backend ids of fields whose content must not reach an agent. */
  private async secrets(): Promise<Set<number>> {
    const out = new Set<number>();
    try {
      const { root } = await this.cdp<{ root: { nodeId: number } }>("DOM.getDocument", { depth: 0 });
      const { nodeIds } = await this.cdp<{ nodeIds: number[] }>("DOM.querySelectorAll", {
        nodeId: root.nodeId,
        selector: 'input[type="password"], input[autocomplete^="cc-"], input[autocomplete="one-time-code"], input[autocomplete="current-password"], input[autocomplete="new-password"]',
      });
      for (const nodeId of nodeIds.slice(0, 50)) {
        const { node } = await this.cdp<{ node: { backendNodeId: number } }>("DOM.describeNode", { nodeId });
        out.add(node.backendNodeId);
      }
    } catch {
      // No document yet: nothing to hide.
    }
    return out;
  }

  /** The element behind a ref, or why it cannot be used. */
  private async element(ref: string): Promise<{ node: number; objectId: string }> {
    if (this.fresh()) throw new RefError(`${ref}: the page loaded a new document (${this.page.url()}); its refs are gone. Call browser.snapshot.`);
    const node = this.refs.node(ref);
    if (node === undefined) throw new RefError(`${ref} is not in the latest snapshot of this tab. Call browser.snapshot to get the current refs.`);
    try {
      const { object } = await this.cdp<{ object: RemoteObject }>("DOM.resolveNode", { backendNodeId: node });
      if (object.objectId === undefined) throw new Error("gone");
      const alive = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
        objectId: object.objectId,
        functionDeclaration: "function () { return this.isConnected; }",
        returnByValue: true,
      });
      if (alive.result.value !== true) throw new Error("detached");
      return { node, objectId: object.objectId };
    } catch (e) {
      if (e instanceof RefError) throw e;
      throw new RefError(`${ref} is stale: the element left the page. Call browser.snapshot for the current refs.`);
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

  /** After an action: lets a navigation it started finish. */
  private async settle(before: { epoch: number }): Promise<string> {
    const started = Date.now();
    while (Date.now() - started < 350) {
      if (this.page.epoch() !== before.epoch || this.page.loading()) {
        await this.page.whenLoaded(8000);
        await sleep(60);
        return `\npage: ${this.page.title() || "(untitled)"}  ${this.page.url()}`;
      }
      await sleep(40);
    }
    return "";
  }

  async click(ref: string, double = false): Promise<string> {
    const { node, objectId } = await this.element(ref);
    const { x, y } = await this.centre(node, ref);
    const cover = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
      objectId,
      functionDeclaration: COVER,
      arguments: [{ value: x }, { value: y }],
      returnByValue: true,
    });
    const before = { epoch: this.page.epoch() };
    await this.cdp("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
    for (let n = 1; n <= (double ? 2 : 1); n++) {
      await this.cdp("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", buttons: 1, clickCount: n });
      await this.cdp("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", buttons: 0, clickCount: n });
    }
    const moved = await this.settle(before);
    const covered = typeof cover.result.value === "string" && cover.result.value !== "" ? `\nnote: the click landed on ${cover.result.value}, which covers ${ref}.` : "";
    return `clicked ${ref}.${moved}${covered}`;
  }

  async type(ref: string, text: string, submit: boolean, append: boolean): Promise<string> {
    const { objectId } = await this.element(ref);
    const r = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
      objectId,
      functionDeclaration: FILL,
      arguments: [{ value: append }],
      returnByValue: true,
    });
    if (r.exceptionDetails !== undefined) throw new RefError(`${ref} cannot take text.`);
    if (text !== "") await this.cdp("Input.insertText", { text });
    const before = { epoch: this.page.epoch() };
    let moved = "";
    if (submit) {
      await this.key(parseKey("Enter") as KeyEvent);
      moved = await this.settle(before);
    }
    // Never says what was typed: it may be a secret.
    return `typed ${[...text].length} characters into ${ref}${submit ? " and pressed Enter" : ""}.${moved}`;
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
    if (k === null) throw new RefError(`${JSON.stringify(spec)} is not a key. Use a name like Enter, Tab, Escape, ArrowDown, or Control+A.`);
    const before = { epoch: this.page.epoch() };
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
      const { node } = await this.element(a.ref);
      await this.cdp("DOM.scrollIntoViewIfNeeded", { backendNodeId: node });
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
    const { objectId } = await this.element(ref);
    const r = await this.cdp<Evaluated>("Runtime.callFunctionOn", {
      objectId,
      functionDeclaration: PICK,
      arguments: [{ value }],
      returnByValue: true,
    });
    const v = r.result.value as { err?: string; options?: string[]; picked?: string } | undefined;
    if (v?.err !== undefined) {
      throw new RefError(`${ref}: ${v.err}${v.options === undefined ? "" : `. Options: ${v.options.map((o) => JSON.stringify(o)).join(", ")}`}`);
    }
    return `chose ${JSON.stringify(v?.picked ?? value)} in ${ref}.`;
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
        if (Date.now() - started >= limit) throw new RefError(`waited ${limit / 1000}s: the page does not show ${want}.`);
        await sleep(WAIT_LIMITS.pollMs);
      }
    }
    if (a.load === true) {
      const ok = await this.page.whenLoaded(limit);
      if (!ok) throw new RefError(`waited ${limit / 1000}s: the page is still loading.`);
      return `the page finished loading: ${this.page.title() || "(untitled)"}  ${this.page.url()}`;
    }
    const ms = Math.max(0, a.ms ?? 0);
    await sleep(ms);
    return `waited ${ms} ms.`;
  }

  async evaluate(js: string): Promise<string> {
    const run = async (expression: string) =>
      this.cdp<Evaluated>("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true, userGesture: true, timeout: 10_000 });
    // An expression first; a body with statements and return second.
    let r = await run(`(async () => (\n${js}\n))()`);
    if (r.exceptionDetails?.exception?.description?.startsWith("SyntaxError") === true) r = await run(`(async () => {\n${js}\n})()`);
    if (r.exceptionDetails !== undefined) {
      const d = r.exceptionDetails.exception?.description ?? r.exceptionDetails.text ?? "error";
      throw new RefError(`the script threw: ${d.split("\n")[0]}`);
    }
    const v = r.result.value;
    const text = v === undefined ? "undefined" : (JSON.stringify(v) ?? String(r.result.description ?? v));
    return text.length > 4000 ? `${text.slice(0, 4000)}\n[... cut at 4000 characters ...]` : text;
  }

  async screenshot(ref?: string): Promise<Shot> {
    const m = await this.cdp<{ cssVisualViewport: { clientWidth: number; clientHeight: number } }>("Page.getLayoutMetrics");
    let clip = { x: 0, y: 0, width: Math.max(1, m.cssVisualViewport.clientWidth), height: Math.max(1, m.cssVisualViewport.clientHeight) };
    if (ref !== undefined && ref !== "") {
      const { node } = await this.element(ref);
      await this.centre(node, ref);
      const { model } = await this.cdp<{ model: { border: number[] } }>("DOM.getBoxModel", { backendNodeId: node });
      const b = model.border;
      const xs = [b[0] ?? 0, b[2] ?? 0, b[4] ?? 0, b[6] ?? 0];
      const ys = [b[1] ?? 0, b[3] ?? 0, b[5] ?? 0, b[7] ?? 0];
      clip = { x: Math.min(...xs), y: Math.min(...ys), width: Math.max(1, Math.max(...xs) - Math.min(...xs)), height: Math.max(1, Math.max(...ys) - Math.min(...ys)) };
    }
    const scale = Math.min(1, WAIT_LIMITS.maxShot / Math.max(clip.width, clip.height));
    const r = await this.cdp<{ data: string }>("Page.captureScreenshot", { format: "jpeg", quality: 60, clip: { ...clip, scale } });
    return { data: Buffer.from(r.data, "base64"), mime: "image/jpeg", width: Math.round(clip.width * scale), height: Math.round(clip.height * scale) };
  }

  /** How many refs the page has handed out (for the tab's summary). */
  get refCount(): number {
    return this.refs.size;
  }
}

