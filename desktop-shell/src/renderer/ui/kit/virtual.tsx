import { Fragment, type ReactElement, type RefObject, useLayoutEffect, useRef, useState } from "react";

// A list longer than this renders only the rows in view.
export const VIRTUAL_AFTER = 200;

// Where each row starts, when each row takes its height plus gap:
// offsets[i] is the top of row i and offsets[n] the end of the last gap.
export function rowOffsets(heights: readonly number[], gap: number): number[] {
  const out = [0];
  for (const h of heights) out.push(out[out.length - 1]! + h + gap);
  return out;
}

// The rows that show between top and bottom (pixels from the list's top),
// with overscan rows on each side: [start, end).
export function visibleRange(offsets: readonly number[], top: number, bottom: number, overscan: number): { start: number; end: number } {
  const n = offsets.length - 1;
  if (n <= 0) return { start: 0, end: 0 };
  // The first row that ends below top, and the first row that starts at or below bottom.
  let lo = 0;
  let hi = n;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (offsets[mid + 1]! > top) hi = mid;
    else lo = mid + 1;
  }
  const first = Math.min(lo, n - 1);
  lo = first;
  hi = n;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (offsets[mid]! >= bottom) hi = mid;
    else lo = mid + 1;
  }
  const last = Math.max(lo, first + 1);
  return { start: Math.max(0, first - overscan), end: Math.min(n, last + overscan) };
}

interface ListProps<T> {
  items: readonly T[];
  keyOf: (item: T) => string;
  // One element per item.
  render: (item: T, index: number) => ReactElement;
  // The element that scrolls the list.
  scroller: RefObject<HTMLElement | null>;
  // The height of a row before it is measured.
  estimate: number;
  // The gap the list's own flex column puts between rows.
  gap: number;
  // "li" for a list inside a <ul>: the window is an <li> that holds a <ul>.
  as?: "div" | "li";
  className?: string;
  // Called after rows change their height, to keep a scroll pinned.
  onResize?: () => void;
  overscan?: number;
  // An index to scroll into view, by its measured or estimated place.
  reveal?: number | null;
}

// A list that renders all its rows up to VIRTUAL_AFTER, and past that only
// the rows in view, with spacers for the rest.
export function VirtualList<T>(props: ListProps<T>) {
  if (props.items.length <= VIRTUAL_AFTER) {
    return (
      <>
        {props.items.map((t, i) => (
          <Fragment key={props.keyOf(t)}>{props.render(t, i)}</Fragment>
        ))}
      </>
    );
  }
  return <Windowed {...props} />;
}

function Windowed<T>({ items, keyOf, render, scroller, estimate, gap, as = "div", className, onResize, overscan = 6, reveal = null }: ListProps<T>) {
  const heights = useRef(new Map<string, number>());
  const [measured, setMeasured] = useState(0);
  const outer = useRef<HTMLElement | null>(null);
  const inner = useRef<HTMLElement | null>(null);
  const keys = items.map(keyOf);
  const offsets = rowOffsets(
    keys.map((k) => heights.current.get(k) ?? estimate),
    gap,
  );
  // The part of the list in view, in pixels from its top.
  const [view, setView] = useState({ top: 0, height: 0 });
  const { start, end } = visibleRange(offsets, view.top, view.top + Math.max(view.height, estimate), overscan);
  const latest = useRef({ offsets, start, end });
  latest.current = { offsets, start, end };

  // Follow the scroller: a new view only when it shows other rows.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el === null) return;
    const read = () => {
      const list = outer.current;
      if (list === null) return;
      const top = el.getBoundingClientRect().top - list.getBoundingClientRect().top;
      const height = el.clientHeight;
      const l = latest.current;
      const next = visibleRange(l.offsets, top, top + Math.max(height, estimate), overscan);
      if (next.start !== l.start || next.end !== l.end) setView({ top, height });
    };
    read();
    el.addEventListener("scroll", read, { passive: true });
    const ro = new ResizeObserver(read);
    ro.observe(el);
    return () => {
      el.removeEventListener("scroll", read);
      ro.disconnect();
    };
  }, [scroller, estimate, overscan]);

  // Scroll to a row: its place puts it in the window, which then renders it.
  useLayoutEffect(() => {
    const el = scroller.current;
    const list = outer.current;
    if (reveal === null || el === null || list === null) return;
    const top = list.getBoundingClientRect().top - el.getBoundingClientRect().top + el.scrollTop;
    el.scrollTop = top + (latest.current.offsets[reveal] ?? 0) - el.clientHeight / 3;
  }, [reveal, scroller]);

  // Measure each rendered row now, and again when it changes size.
  const keyOfEl = useRef(new WeakMap<Element, string>());
  const watched = useRef(new Set<Element>());
  const observer = useRef<ResizeObserver | null>(null);
  useLayoutEffect(() => {
    const ro = new ResizeObserver((entries) => {
      let changed = false;
      for (const e of entries) {
        const k = keyOfEl.current.get(e.target);
        const h = e.target.getBoundingClientRect().height;
        if (k !== undefined && h > 0 && heights.current.get(k) !== h) {
          heights.current.set(k, h);
          changed = true;
        }
      }
      if (changed) setMeasured((n) => n + 1);
    });
    observer.current = ro;
    return () => {
      ro.disconnect();
      watched.current.clear();
    };
  }, []);
  useLayoutEffect(() => {
    const box = inner.current;
    const ro = observer.current;
    if (box === null || ro === null) return;
    const rows = [...box.children].filter((c) => !(c as HTMLElement).dataset["vspacer"]);
    const now = new Set<Element>();
    let changed = false;
    rows.forEach((el, i) => {
      const k = keys[start + i];
      if (k === undefined) return;
      now.add(el);
      keyOfEl.current.set(el, k);
      if (!watched.current.has(el)) {
        ro.observe(el);
        watched.current.add(el);
      }
      const h = el.getBoundingClientRect().height;
      if (h > 0 && heights.current.get(k) !== h) {
        heights.current.set(k, h);
        changed = true;
      }
    });
    for (const el of watched.current) {
      if (!now.has(el)) {
        ro.unobserve(el);
        watched.current.delete(el);
      }
    }
    if (changed) setMeasured((n) => n + 1);
  });

  const onResizeRef = useRef(onResize);
  onResizeRef.current = onResize;
  useLayoutEffect(() => {
    onResizeRef.current?.();
  }, [measured, items.length]);

  const n = items.length;
  const top = start > 0 ? offsets[start]! - gap : 0;
  const bottom = end < n ? offsets[n]! - offsets[end]! - gap : 0;
  const spacer = (h: number, at: string) =>
    as === "li" ? (
      <li key={at} aria-hidden data-vspacer="1" className="list-none" style={{ height: h }} />
    ) : (
      <div key={at} aria-hidden data-vspacer="1" style={{ height: h }} />
    );
  const rows = (
    <>
      {start > 0 && spacer(top, "top")}
      {items.slice(start, end).map((t, i) => (
        <Fragment key={keys[start + i]}>{render(t, start + i)}</Fragment>
      ))}
      {end < n && spacer(bottom, "bottom")}
    </>
  );
  if (as === "li") {
    return (
      <li ref={(el) => void (outer.current = el)} className="virtual list-none">
        <ul ref={(el) => void (inner.current = el)} className={className}>
          {rows}
        </ul>
      </li>
    );
  }
  return (
    <div
      ref={(el) => {
        outer.current = el;
        inner.current = el;
      }}
      className={className === undefined ? "virtual" : `virtual ${className}`}
    >
      {rows}
    </div>
  );
}
