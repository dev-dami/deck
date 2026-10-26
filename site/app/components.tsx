"use client";

import { Check, Copy, HalfMoon, SunLight } from "iconoir-react";
import { useEffect, useRef, useState } from "react";

// A command line with a copy button. The command is selectable text too.
export function Command({ label, cmd }: { label?: string; cmd: string }) {
  const [done, setDone] = useState(false);
  const copy = () => {
    void navigator.clipboard.writeText(cmd).then(() => {
      setDone(true);
      setTimeout(() => setDone(false), 1400);
    });
  };
  return (
    <div className="command">
      {label !== undefined && <span className="command-label">{label}</span>}
      <code>
        <span aria-hidden className="prompt">
          $
        </span>
        {cmd}
      </code>
      <button type="button" onClick={copy} aria-label={`Copy: ${cmd}`}>
        {done ? <Check aria-hidden className="icon icon-sm" /> : <Copy aria-hidden className="icon icon-sm" />}
        {done ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

// The footer wordmark, made of rubber. The page scrolls to its end as any
// page does; keep scrolling there (wheel, trackpad or a swipe) and the word
// stretches up, pushing the page up with it, then springs back when you
// stop. Each letter is a spring: it also leans taller under the pointer,
// stretches when you grab it and pull up, and wobbles as the page goes by.
export function Wordmark({ href, word }: { href: string; word: string }) {
  const root = useRef<HTMLAnchorElement>(null);
  const moved = useRef(false);

  useEffect(() => {
    const el = root.current;
    if (el === null) return;
    // Reduced motion keeps what the reader does by hand (the pull at the
    // bottom, a grab), without the lean, the wobble or the overshoot.
    const calm = matchMedia("(prefers-reduced-motion: reduce)").matches;
    const parts = Array.from(el.querySelectorAll<HTMLElement>(".mega-letter, .mega-cursor"));
    const n = parts.length;
    const bar = n - 1; // the cursor stretches sideways, the letters upward
    const doc = document.documentElement;
    const x = new Float64Array(n).fill(1);
    const v = new Float64Array(n);
    let centres: number[] = [];
    let letterH = 0;
    let over = false;
    let px = 0.5;
    let dragging = false;
    let grab = 0.5;
    let startY = 0;
    let pull = 0;
    // How far past the bottom of the page the reader has scrolled, in px.
    let past = 0;
    let lastPast = 0;
    let touching = false;
    let touchY = 0;
    let grown = 0;
    let lastScroll = window.scrollY;
    let visible = false;
    let raf = 0;
    let last = 0;

    const measure = () => {
      const r = el.getBoundingClientRect();
      centres = parts.map((p) => {
        const b = p.getBoundingClientRect();
        return (b.left + b.width / 2 - r.left) / r.width;
      });
      letterH = parts[0]?.offsetHeight ?? 0;
    };
    // A few px of slack: on a scaled display the scroll position is fractional.
    const atBottom = () => window.scrollY + window.innerHeight >= doc.scrollHeight - 4;
    // Soft limits: past them the rubber gets stiffer instead of stopping.
    const soft = (t: number) => (t >= 1 ? 1 + 1.5 * Math.tanh((t - 1) / 1.5) : 1 - 0.4 * Math.tanh((1 - t) / 0.4));

    const frame = (now: number) => {
      const dt = Math.min((now - last) / 1000, 1 / 30);
      last = now;
      // Stop pulling and the rubber goes back.
      if (!touching && now - lastPast > 120) past *= Math.exp(-dt * 8);
      if (past < 0.5) past = 0;
      const stretch = 1.5 * Math.tanh(past / 650);
      let busy = dragging || past > 0;
      let tallest = 1;
      for (let i = 0; i < n; i++) {
        const d = centres[i] - (dragging ? grab : px);
        const near = Math.exp(-(d * d) / 0.05);
        let t = 1 + stretch * (0.88 + 0.12 * Math.sin(i * 1.7));
        if (dragging) t += pull * (0.3 + 0.7 * near);
        else if (over && !calm) t += 0.14 * near;
        if (i === bar) t = 1 + (t - 1) * 0.25;
        t = soft(t);
        v[i] += (240 * (t - x[i]) - (calm ? 31 : 12) * v[i]) * dt;
        x[i] += v[i] * dt;
        if (Math.abs(v[i]) > 0.0005 || Math.abs(t - x[i]) > 0.0005) busy = true;
        if (i !== bar) tallest = Math.max(tallest, x[i]);
        parts[i].style.transform = i === bar ? `scaleX(${x[i]})` : `scaleY(${x[i]})`;
      }
      // The footer grows with the tallest letter, so the stretch pushes the
      // page up instead of drawing over it, and the page stays at its end.
      const grow = Math.round((tallest - 1) * letterH);
      if (grow !== grown) {
        const pinned = atBottom();
        grown = grow;
        el.style.setProperty("--grow", `${grow}px`);
        if (pinned) window.scrollTo({ top: doc.scrollHeight, behavior: "instant" });
      }
      raf = busy || over ? requestAnimationFrame(frame) : 0;
    };
    const kick = () => {
      if (raf === 0 && visible) {
        last = performance.now();
        raf = requestAnimationFrame(frame);
      }
    };

    const onEnter = (e: PointerEvent) => {
      if (e.pointerType === "mouse") over = true;
      kick();
    };
    const onLeave = () => {
      over = false;
      kick();
    };
    const onMove = (e: PointerEvent) => {
      const r = el.getBoundingClientRect();
      px = (e.clientX - r.left) / r.width;
      if (dragging) {
        const dy = startY - e.clientY;
        if (Math.abs(dy) > 5) moved.current = true;
        pull = (dy / Math.max(1, letterH)) * 1.6;
      }
      kick();
    };
    const onDown = (e: PointerEvent) => {
      if (e.button !== 0 || e.pointerType !== "mouse") return;
      const r = el.getBoundingClientRect();
      dragging = true;
      moved.current = false;
      grab = (e.clientX - r.left) / r.width;
      startY = e.clientY;
      pull = 0;
      el.setPointerCapture(e.pointerId);
      kick();
    };
    const onUp = () => {
      dragging = false;
      pull = 0;
      kick();
    };
    // Scrolling on at the bottom of the page pulls the word; scrolling back
    // up lets it go at once.
    const onWheel = (e: WheelEvent) => {
      if (!visible) return;
      const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
      if (dy > 0 && atBottom()) past += dy;
      else if (dy < 0 && past > 0) past = Math.max(0, past + dy * 3);
      else return;
      lastPast = performance.now();
      kick();
    };
    const onTouchStart = (e: TouchEvent) => {
      touchY = e.touches[0].clientY;
      touching = true;
    };
    const onTouchMove = (e: TouchEvent) => {
      const dy = touchY - e.touches[0].clientY;
      if (!visible || !atBottom()) return;
      past = Math.max(0, dy * 1.5);
      lastPast = performance.now();
      kick();
    };
    const onTouchEnd = () => {
      touching = false;
      lastPast = performance.now();
      kick();
    };
    const onScroll = () => {
      const dy = Math.max(-80, Math.min(80, window.scrollY - lastScroll));
      lastScroll = window.scrollY;
      if (!visible || past > 0 || calm) return;
      for (let i = 0; i < n; i++) v[i] += dy * 0.02 * (0.6 + (0.4 * i) / n);
      kick();
    };

    measure();
    const io = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting;
      if (visible) {
        measure();
        kick();
      }
    });
    io.observe(el);
    el.addEventListener("pointerenter", onEnter);
    el.addEventListener("pointerleave", onLeave);
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerdown", onDown);
    el.addEventListener("pointerup", onUp);
    el.addEventListener("pointercancel", onUp);
    window.addEventListener("wheel", onWheel, { passive: true });
    window.addEventListener("touchstart", onTouchStart, { passive: true });
    window.addEventListener("touchmove", onTouchMove, { passive: true });
    window.addEventListener("touchend", onTouchEnd, { passive: true });
    window.addEventListener("touchcancel", onTouchEnd, { passive: true });
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", measure);
    return () => {
      cancelAnimationFrame(raf);
      io.disconnect();
      el.removeEventListener("pointerenter", onEnter);
      el.removeEventListener("pointerleave", onLeave);
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerdown", onDown);
      el.removeEventListener("pointerup", onUp);
      el.removeEventListener("pointercancel", onUp);
      window.removeEventListener("wheel", onWheel);
      window.removeEventListener("touchstart", onTouchStart);
      window.removeEventListener("touchmove", onTouchMove);
      window.removeEventListener("touchend", onTouchEnd);
      window.removeEventListener("touchcancel", onTouchEnd);
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", measure);
    };
  }, []);

  return (
    <a
      ref={root}
      href={href}
      className="mega"
      aria-label="Deck on GitHub"
      draggable={false}
      onClick={(e) => {
        // A pull is not a click.
        if (moved.current) {
          e.preventDefault();
          moved.current = false;
        }
      }}
    >
      <span className="mega-word" aria-hidden>
        {[...word].map((l, i) => (
          <span key={i} className="mega-letter">
            {l}
          </span>
        ))}
      </span>
      <span className="mega-cursor" aria-hidden />
    </a>
  );
}

// Light or dark. The page follows the system until you pick one here; the
// pick is kept in localStorage and applied before paint by the script in
// layout.tsx. Which icon shows is left to CSS, so nothing re-renders.
export function ThemeToggle() {
  const flip = () => {
    const root = document.documentElement;
    const current = root.dataset.theme ?? (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
    const next = current === "dark" ? "light" : "dark";
    root.dataset.theme = next;
    try {
      localStorage.setItem("deck-theme", next);
    } catch {
      // Private windows can refuse storage; the pick still holds for this visit.
    }
  };
  return (
    <button type="button" className="theme-toggle" onClick={flip} aria-label="Switch between light and dark">
      <HalfMoon aria-hidden className="icon icon-moon" />
      <SunLight aria-hidden className="icon icon-sun" />
    </button>
  );
}
