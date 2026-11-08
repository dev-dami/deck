import { useEffect } from "react";

// Liquid glass that bends what is behind it (design-glass.md, section 11).
// Every glass panel of the Liquid Glass material gets a lens: an SVG filter
// whose displacement map is drawn for the panel's own size and corners, so
// its rim refracts the backdrop inward as the edge of a thick pane would,
// with the red, green and blue bent by slightly different amounts. The
// middle stays flat and clear. The panel's CSS reads the lens from --lens
// (styles.css, under data-material="glass"); a panel the runtime has not
// reached yet is plain frosted glass.

const SELECTOR = ".liquid, .liquid-thick, .liquid-clear, .material";
// Maps are drawn at half size: the bend is smooth, and the filter scales
// the map to the panel.
const MAP_SCALE = 0.5;
const MAX_FILTERS = 96;

export interface Lens {
  w: number;
  h: number;
  r: number;
  // The rim's width, where the glass bends, and how far it bends there.
  band: number;
  depth: number;
}

// The lens of a panel w×h px with corner radius r.
export function lensFor(w: number, h: number, radius: number): Lens {
  const r = Math.max(0, Math.min(radius, w / 2, h / 2));
  const band = Math.max(6, Math.min(32, Math.min(w, h) * 0.36));
  return { w, h, r, band, depth: Math.min(band * 1.05, 30) };
}

// The displacement map of a lens, RGBA, mw×mh: red and green carry where to
// read from (0.5 is in place), pointing inward across the rim, most at the
// very edge, as a circle's edge bends light (1 − √(1 − t²)).
export function lensMap(l: Lens, mw: number, mh: number): Uint8ClampedArray<ArrayBuffer> {
  const out = new Uint8ClampedArray(new ArrayBuffer(mw * mh * 4));
  const hx = l.w / 2;
  const hy = l.h / 2;
  const ix = hx - l.r;
  const iy = hy - l.r;
  for (let y = 0; y < mh; y++) {
    const py = ((y + 0.5) / mh) * l.h - hy;
    for (let x = 0; x < mw; x++) {
      const px = ((x + 0.5) / mw) * l.w - hx;
      // The rounded rectangle's signed distance, and its outward normal.
      const qx = Math.abs(px) - ix;
      const qy = Math.abs(py) - iy;
      let nx = 0;
      let ny = 0;
      let d: number;
      if (qx > 0 && qy > 0) {
        const len = Math.hypot(qx, qy);
        d = len - l.r;
        nx = qx / len;
        ny = qy / len;
      } else if (qx > qy) {
        d = qx - l.r;
        nx = 1;
      } else {
        d = qy - l.r;
        ny = 1;
      }
      nx *= Math.sign(px) || 1;
      ny *= Math.sign(py) || 1;
      const depth = -d;
      let dx = 0;
      let dy = 0;
      if (depth >= 0 && depth < l.band) {
        const t = 1 - depth / l.band;
        const m = 1 - Math.sqrt(1 - t * t);
        dx = -nx * m;
        dy = -ny * m;
      }
      const i = (y * mw + x) * 4;
      out[i] = 128 + dx * 127;
      out[i + 1] = 128 + dy * 127;
      out[i + 2] = 128;
      out[i + 3] = 255;
    }
  }
  return out;
}

function mapUrl(l: Lens): string {
  const mw = Math.max(2, Math.round(l.w * MAP_SCALE));
  const mh = Math.max(2, Math.round(l.h * MAP_SCALE));
  const c = document.createElement("canvas");
  c.width = mw;
  c.height = mh;
  const ctx = c.getContext("2d");
  if (ctx === null) return "";
  ctx.putImageData(new ImageData(lensMap(l, mw, mh), mw, mh), 0, 0);
  return c.toDataURL("image/png");
}

const NS = "http://www.w3.org/2000/svg";

function el(name: string, attrs: Record<string, string | number>, kids: Element[] = []): Element {
  const e = document.createElementNS(NS, name);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, String(v));
  for (const k of kids) e.append(k);
  return e;
}

// One channel of the backdrop bent by `scale`, the others dropped.
function channel(scale: number, keep: "r" | "g" | "b"): Element[] {
  const m = { r: "1 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 1 0", g: "0 0 0 0 0  0 1 0 0 0  0 0 0 0 0  0 0 0 1 0", b: "0 0 0 0 0  0 0 0 0 0  0 0 1 0 0  0 0 0 1 0" }[keep];
  return [
    el("feDisplacementMap", { in: "SourceGraphic", in2: "map", scale: scale.toFixed(2), xChannelSelector: "R", yChannelSelector: "G", result: `d${keep}` }),
    el("feColorMatrix", { in: `d${keep}`, type: "matrix", values: m, result: `c${keep}` }),
  ];
}

function filterFor(id: string, l: Lens): Element {
  const { w, h, depth } = l;
  return el("filter", { id, x: 0, y: 0, width: w, height: h, filterUnits: "userSpaceOnUse", "color-interpolation-filters": "sRGB" }, [
    el("feImage", { href: mapUrl(l), x: 0, y: 0, width: w, height: h, preserveAspectRatio: "none", result: "map" }),
    ...channel(depth * 1.1, "r"),
    ...channel(depth, "g"),
    ...channel(depth * 0.9, "b"),
    el("feComposite", { in: "cr", in2: "cg", operator: "arithmetic", k1: 0, k2: 1, k3: 1, k4: 0, result: "rg" }),
    el("feComposite", { in: "rg", in2: "cb", operator: "arithmetic", k1: 0, k2: 1, k3: 1, k4: 0 }),
  ]);
}

// The lenses of the window: one filter per size and corner, kept while in
// use and dropped oldest first past MAX_FILTERS.
function startLenses(): () => void {
  const svg = el("svg", { width: 0, height: 0, "aria-hidden": "true", style: "position:absolute;width:0;height:0;overflow:hidden" }) as SVGSVGElement;
  const defs = el("defs", {});
  svg.append(defs);
  document.body.append(svg);
  const filters = new Map<string, string>();
  let seq = 0;
  const lensId = (l: Lens): string => {
    const key = `${l.w}x${l.h}r${l.r}`;
    const have = filters.get(key);
    if (have !== undefined) {
      filters.delete(key);
      filters.set(key, have);
      return have;
    }
    const id = `deck-lens-${++seq}`;
    defs.append(filterFor(id, l));
    filters.set(key, id);
    if (filters.size > MAX_FILTERS) {
      const [oldKey, oldId] = filters.entries().next().value as [string, string];
      filters.delete(oldKey);
      defs.querySelector(`#${oldId}`)?.remove();
    }
    return id;
  };

  const fit = (e: HTMLElement) => {
    // Layout size, not the box on screen, so a panel that scales as it
    // morphs keeps its lens.
    const w = Math.round(e.offsetWidth);
    const h = Math.round(e.offsetHeight);
    if (w < 12 || h < 12) {
      e.style.removeProperty("--lens");
      return;
    }
    const radius = Number.parseFloat(getComputedStyle(e).borderTopLeftRadius) || 0;
    e.style.setProperty("--lens", `url(#${lensId(lensFor(w, h, Math.round(radius)))})`);
  };

  // A panel that is resizing settles before its lens is redrawn.
  const waiting = new Map<HTMLElement, number>();
  const later = (e: HTMLElement) => {
    window.clearTimeout(waiting.get(e));
    waiting.set(
      e,
      window.setTimeout(() => {
        waiting.delete(e);
        if (e.isConnected) fit(e);
      }, 90),
    );
  };
  const ro = new ResizeObserver((entries) => {
    for (const en of entries) later(en.target as HTMLElement);
  });
  const seen = new WeakSet<Element>();
  const take = (root: ParentNode) => {
    const found: Element[] = root instanceof Element && root.matches(SELECTOR) ? [root] : [];
    found.push(...root.querySelectorAll(SELECTOR));
    for (const e of found) {
      if (seen.has(e)) continue;
      seen.add(e);
      ro.observe(e);
      fit(e as HTMLElement);
    }
  };
  const mo = new MutationObserver((records) => {
    for (const r of records) {
      if (r.type === "attributes") take(r.target as Element);
      else for (const n of r.addedNodes) if (n instanceof Element) take(n);
    }
  });
  take(document.body);
  mo.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  return () => {
    mo.disconnect();
    ro.disconnect();
    for (const t of waiting.values()) window.clearTimeout(t);
    svg.remove();
    for (const e of document.querySelectorAll<HTMLElement>(SELECTOR)) e.style.removeProperty("--lens");
  };
}

// Lenses while the material is liquid glass and the window may be
// translucent.
export function useLiquidGlass(material: string): void {
  useEffect(() => {
    if (material !== "glass") return;
    if (window.matchMedia("(prefers-reduced-transparency: reduce)").matches) return;
    if (typeof ResizeObserver === "undefined") return;
    return startLenses();
  }, [material]);
}

// ── The segmented controls' pill ──
// The chosen segment's pill is one shape that slides to the next choice:
// it stretches across both, its far edge leading, and settles on the new
// one with a little spring, squashing as it goes, as a drop of liquid
// moves. The track draws it (.seg-track::before in styles.css) from
// --seg-l and --seg-w; this places it and plays the move.

const SPRING =
  "linear(0, 0.023, 0.084, 0.168, 0.266, 0.369, 0.471, 0.568, 0.657, 0.736, 0.805, 0.863, 0.911, 0.949, 0.979, 1.001, 1.017, 1.028, 1.035, 1.038, 1.038, 1.037, 1.034, 1.031, 1.027, 1.023, 1.019, 1.016, 1.012, 1.009, 1.007, 1.005, 1.003, 1.002, 1.001, 1)";

interface Placed {
  on: Element;
  l: number;
  w: number;
}

function startPills(): () => void {
  const placed = new WeakMap<HTMLElement, Placed>();
  const still = () => document.documentElement.getAttribute("data-motion") === "reduced";
  const place = (track: HTMLElement, animate: boolean) => {
    const on = track.querySelector<HTMLElement>(":scope > .seg-on") ?? track.querySelector<HTMLElement>(".seg-on");
    if (on === null || on.offsetParent !== track) {
      track.removeAttribute("data-seg");
      placed.delete(track);
      return;
    }
    const l = on.offsetLeft;
    const w = on.offsetWidth;
    const prev = placed.get(track);
    track.style.setProperty("--seg-l", `${l}px`);
    track.style.setProperty("--seg-w", `${w}px`);
    track.setAttribute("data-seg", "");
    placed.set(track, { on, l, w });
    if (!animate || prev === undefined || prev.on === on || still() || (prev.l === l && prev.w === w)) return;
    const a = Math.min(prev.l, l);
    const b = Math.max(prev.l + prev.w, l + w);
    track.animate(
      [
        { left: `${prev.l}px`, width: `${prev.w}px`, transform: "scaleY(1)" },
        { left: `${a}px`, width: `${b - a}px`, transform: "scaleY(0.84)", offset: 0.38 },
        { left: `${l}px`, width: `${w}px`, transform: "scaleY(1)" },
      ],
      { duration: 560, easing: SPRING, pseudoElement: "::before" },
    );
  };
  const all = (animate: boolean) => {
    for (const t of document.querySelectorAll<HTMLElement>(".seg-track")) place(t, animate);
  };
  let frame = 0;
  const soon = () => {
    if (frame !== 0) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      all(true);
    });
  };
  // Only changes in or of a track: a streaming reply never measures them.
  const touches = (r: MutationRecord) =>
    (r.target instanceof Element && r.target.closest(".seg-track") !== null) ||
    [...r.addedNodes].some((n) => n instanceof Element && (n.matches(".seg-track") || n.querySelector(".seg-track") !== null));
  const mo = new MutationObserver((records) => {
    if (records.some(touches)) soon();
  });
  mo.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  const resize = () => all(false);
  window.addEventListener("resize", resize);
  void document.fonts?.ready.then(resize);
  all(false);
  return () => {
    mo.disconnect();
    window.removeEventListener("resize", resize);
    cancelAnimationFrame(frame);
  };
}

export function useLiquidPills(): void {
  useEffect(() => startPills(), []);
}
