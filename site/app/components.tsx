"use client";

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
        {done ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

// The footer wordmark, made of rubber. Each letter is a spring: it leans
// taller under the pointer, stretches when you grab it and pull up (or
// squashes when you push down), wobbles as the page scrolls past, and pulls
// up when you keep scrolling at the very bottom. Let go and it snaps back.
export function Wordmark({ href, word }: { href: string; word: string }) {
  const root = useRef<HTMLAnchorElement>(null);
  const moved = useRef(false);

  useEffect(() => {
    const el = root.current;
    if (el === null || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const parts = Array.from(el.querySelectorAll<HTMLElement>(".mega-letter, .mega-cursor"));
    const n = parts.length;
    const bar = n - 1; // the cursor stretches sideways, the letters upward
    const x = new Float64Array(n).fill(1);
    const v = new Float64Array(n);
    let centres: number[] = [];
    let over = false;
    let px = 0.5;
    let dragging = false;
    let grab = 0.5;
    let startY = 0;
    let pull = 0;
    let shove = 0;
    let lastShove = 0;
    let touchY = 0;
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
    };
    const atBottom = () => window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2;
    // Soft limits: past them the rubber gets stiffer instead of stopping.
    const soft = (t: number) => (t >= 1 ? 1 + 0.6 * Math.tanh((t - 1) / 0.6) : 1 - 0.4 * Math.tanh((1 - t) / 0.4));

    const frame = (now: number) => {
      const dt = Math.min((now - last) / 1000, 1 / 30);
      last = now;
      if (now - lastShove > 90) shove *= Math.exp(-dt * 9);
      let busy = dragging || shove > 0.001;
      for (let i = 0; i < n; i++) {
        const d = centres[i] - (dragging ? grab : px);
        const near = Math.exp(-(d * d) / 0.05);
        let t = 1 + shove * (0.75 + 0.25 * Math.sin(i * 1.7));
        if (dragging) t += pull * (0.3 + 0.7 * near);
        else if (over) t += 0.14 * near;
        if (i === bar) t = 1 + (t - 1) * 0.3;
        t = soft(t);
        v[i] += (240 * (t - x[i]) - 12 * v[i]) * dt;
        x[i] += v[i] * dt;
        if (Math.abs(v[i]) > 0.0005 || Math.abs(t - x[i]) > 0.0005) busy = true;
        parts[i].style.transform = i === bar ? `scaleX(${x[i]})` : `scaleY(${x[i]})`;
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
        pull = (dy / r.height) * 1.6;
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
    const onWheel = (e: WheelEvent) => {
      if (!visible || e.deltaY <= 0 || !atBottom()) return;
      shove = Math.min(0.3, shove + e.deltaY * 0.0008);
      lastShove = performance.now();
      kick();
    };
    const onTouchStart = (e: TouchEvent) => {
      touchY = e.touches[0].clientY;
    };
    const onTouchMove = (e: TouchEvent) => {
      const dy = touchY - e.touches[0].clientY;
      if (!visible || dy <= 0 || !atBottom()) return;
      shove = Math.min(0.3, dy * 0.0015);
      lastShove = performance.now();
      kick();
    };
    const onScroll = () => {
      const dy = Math.max(-80, Math.min(80, window.scrollY - lastScroll));
      lastScroll = window.scrollY;
      if (!visible) return;
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
