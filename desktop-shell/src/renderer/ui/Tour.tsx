import { kindColour } from "@web/theme";
import { Fragment, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { DeckBridge } from "../../shared/bridge";
import { getPrefs, setPrefs, usePrefs } from "../prefs";
import {
  autoStarts,
  type Box,
  closeTour,
  getTour,
  placeCard,
  setTourStep,
  spotlight,
  startTour,
  shows,
  stepFrom,
  stepsFor,
  stepText,
  type TourScreen,
  type TourStep,
  useTour,
} from "../tour";
import { ArrowLeft, ArrowRight, Check, X } from "./icons";
import { Button } from "./kit/button";
import { isMac, modKey } from "./keys";

// The starter tour (tour.ts, docs/design-glass.md, section 14): a
// spotlight cut out of a dim veil around the step's target, and a small
// card of thick glass beside it with the step, its dots, and Back, Next
// and Skip. The veil never takes a click: only the card does, so the
// window under it stays usable, and Open a folder still opens a folder.
// The arrow keys step, Esc leaves.

// The first element a selector names that shows (a hidden one has no box).
// A step's selector can name its target in each layout (the sidebar's
// foot, the top bar's tools), so it takes the one on screen.
function shown(selector: string): HTMLElement | null {
  for (const el of document.querySelectorAll<HTMLElement>(selector)) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height > 0) return el;
  }
  return null;
}
const present = (selector: string) => shown(selector) !== null;

// The screens whose first-run tour has had its chance in this window.
const tried = new Set<TourScreen>();

// What each screen waits for before its first-run tour starts.
const READY: Record<TourScreen, string> = { welcome: ".welcome-screen .agent-clis", shell: ".composer" };

// Starts the first-run tour on a screen once: only while the pref says the
// person has not seen it, and never in a window a test drives.
function useFirstRun(screen: TourScreen | null, bridge: DeckBridge): void {
  const pref = usePrefs().tour;
  useEffect(() => {
    if (screen === null || pref !== "new" || tried.has(screen)) return;
    let gone = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    void bridge.appInfo().then((info) => {
      const may = autoStarts({ pref: getPrefs().tour, webdriver: navigator.webdriver === true, automated: info.automated === true });
      if (gone || !may) return;
      let waited = 0;
      const poll = () => {
        if (gone) return;
        if (document.querySelector(READY[screen]) === null && waited < 8000) {
          waited += 250;
          timer = setTimeout(poll, 250);
          return;
        }
        tried.add(screen);
        if (!getTour().open) startTour(screen, true);
      };
      // A moment for the screen to settle before the veil comes down.
      timer = setTimeout(poll, 700);
    });
    return () => {
      gone = true;
      clearTimeout(timer);
    };
  }, [screen, pref, bridge]);
}

// Each screen's steps, one list for the window's life.
const STEPS: Record<TourScreen, TourStep[]> = { welcome: stepsFor("welcome"), shell: stepsFor("shell") };

// The tour over the screen that shows: App renders it once, with the
// screen (null on the connect screen, where it has no steps).
export function Tour({ screen, bridge }: { screen: TourScreen | null; bridge: DeckBridge }) {
  const tour = useTour();
  useFirstRun(screen, bridge);
  // Leaving the screen ends its part: Open a folder in the middle of the
  // Welcome's steps goes on with the project's.
  useEffect(() => {
    if (tour.open && tour.screen !== screen) closeTour();
  }, [tour.open, tour.screen, screen]);
  if (!tour.open || tour.screen !== screen) return null;
  const steps = STEPS[tour.screen];
  // The Welcome's part of a first run hands over to the project's; any
  // other end, and every skip, is the end of the tour.
  const handover = tour.auto && tour.screen === "welcome";
  return (
    <TourOverlay
      steps={steps}
      index={Math.min(tour.index, steps.length - 1)}
      last={handover ? "Got it" : "Done"}
      onIndex={setTourStep}
      onDone={() => {
        closeTour();
        if (!handover) setPrefs({ tour: "done" });
      }}
      onSkip={() => {
        closeTour();
        setPrefs({ tour: "done" });
      }}
    />
  );
}

interface Geometry {
  view: { w: number; h: number };
  // The target's box and corner radius; null when the step has none or it
  // does not show.
  rect: Box | null;
  radius: number;
  // Which steps have their target on screen now.
  avail: boolean[];
}

const sameGeo = (a: Geometry | null, b: Geometry) => a !== null && JSON.stringify(a) === JSON.stringify(b);

// Agent mentions in a step's text in their agent's hue, and the composer's
// triggers in mono, as the Welcome's example writes them.
function Body({ text }: { text: string }) {
  const parts = text.split(/(\$[a-z]+|(?<=^|\s)[@#](?=\s))/g);
  return (
    <>
      {parts.map((p, i) =>
        p.startsWith("$") ? (
          <code key={i} className="font-mono text-[12px]" style={{ color: kindColour(p.slice(1)) }}>
            {p}
          </code>
        ) : p === "@" || p === "#" ? (
          <code key={i} className="tour-key font-mono text-[12px] text-foreground">
            {p}
          </code>
        ) : (
          <Fragment key={i}>{p}</Fragment>
        ),
      )}
    </>
  );
}

function TourOverlay({
  steps,
  index,
  last,
  onIndex,
  onDone,
  onSkip,
}: {
  steps: TourStep[];
  index: number;
  last: string;
  onIndex: (i: number) => void;
  onDone: () => void;
  onSkip: () => void;
}) {
  const step = steps[index] as TourStep;
  const mod = useMemo(() => modKey(isMac()), []);
  const text = stepText(step, mod);
  const card = useRef<HTMLDivElement>(null);
  const next = useRef<HTMLButtonElement>(null);
  const [geo, setGeo] = useState<Geometry | null>(null);
  const [size, setSize] = useState({ w: 320, h: 180 });
  const titleId = useId();
  const bodyId = useId();

  // Where the target is, read again whenever the window or the target
  // moves, and on a slow beat for what neither tells (a card that loads).
  const measure = useCallback(() => {
    const el = step.target === null ? null : shown(step.target);
    const r = el?.getBoundingClientRect();
    const g: Geometry = {
      view: { w: window.innerWidth, h: window.innerHeight },
      rect: r === undefined ? null : { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) },
      radius: el === null ? 0 : Number.parseFloat(getComputedStyle(el).borderTopLeftRadius) || 8,
      avail: steps.map((s) => shows(s, present)),
    };
    setGeo((old) => (sameGeo(old, g) ? old : g));
    if (card.current !== null) {
      const w = card.current.offsetWidth;
      const h = card.current.offsetHeight;
      setSize((old) => (old.w === w && old.h === h ? old : { w, h }));
    }
  }, [step, steps]);

  useLayoutEffect(() => {
    if (step.target !== null) shown(step.target)?.scrollIntoView({ block: "nearest", inline: "nearest" });
    measure();
    const el = step.target === null ? null : shown(step.target);
    const ro = new ResizeObserver(measure);
    ro.observe(document.documentElement);
    if (el !== null) ro.observe(el);
    if (card.current !== null) ro.observe(card.current);
    window.addEventListener("resize", measure);
    window.addEventListener("scroll", measure, true);
    const beat = setInterval(measure, 400);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", measure);
      window.removeEventListener("scroll", measure, true);
      clearInterval(beat);
    };
  }, [step, measure]);

  const forward = useCallback(() => {
    const n = stepFrom(steps, index, 1, present);
    if (n === null) onDone();
    else onIndex(n);
  }, [steps, index, onIndex, onDone]);
  const back = useCallback(() => {
    const n = stepFrom(steps, index, -1, present);
    if (n !== null) onIndex(n);
  }, [steps, index, onIndex]);

  // Each step takes the focus to Next, so Enter goes on; the tour gives it
  // back where it was when it ends.
  useEffect(() => {
    next.current?.focus({ preventScroll: true });
  }, [index]);
  useEffect(() => {
    const before = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return () => {
      if (before?.isConnected === true) before.focus({ preventScroll: true });
    };
  }, []);

  // The keys, ahead of the window's own: not while the person types in a
  // field outside the card.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target instanceof HTMLElement ? e.target : null;
      const inCard = t !== null && card.current?.contains(t) === true;
      const typing = t !== null && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName));
      if (typing && !inCard) return;
      if (e.key === "Escape") onSkip();
      else if (e.key === "ArrowRight") forward();
      else if (e.key === "ArrowLeft") back();
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [forward, back, onSkip]);

  const view = geo?.view ?? { w: window.innerWidth, h: window.innerHeight };
  const rect = geo?.rect ?? null;
  const hole = rect === null ? null : spotlight(rect, view);
  const at = placeCard(hole, size, view);
  const avail = geo?.avail ?? steps.map(() => true);
  const live = steps.map((s, i) => ({ s, i })).filter(({ i }) => avail[i] === true || i === index);
  const pos = live.findIndex(({ i }) => i === index);
  const canBack = avail.slice(0, index).some(Boolean);
  const isLast = !avail.slice(index + 1).some(Boolean);
  // A step with no target dims the whole window: the hole shrinks to a
  // point under the card.
  const spot =
    hole === null
      ? { left: at.x + size.w / 2, top: at.y + size.h / 2, width: 0, height: 0, borderRadius: 0 }
      : { left: hole.x, top: hole.y, width: hole.w, height: hole.h, borderRadius: Math.min((geo?.radius ?? 8) + 6, hole.w / 2, hole.h / 2) };

  return (
    <div className="tour" data-step={step.id} role="presentation">
      <div aria-hidden className="tour-spot" data-empty={hole === null ? "" : undefined} style={spot} />
      <div
        ref={card}
        className="tour-card liquid-thick flex flex-col gap-2.5 rounded-(--radius) p-4"
        data-side={at.side}
        style={{ left: at.x, top: at.y }}
        role="dialog"
        aria-modal="false"
        aria-labelledby={titleId}
        aria-describedby={bodyId}
      >
        <div className="flex items-center gap-2">
          <span className="tour-count text-2xs text-faint tabular-nums">
            Tour · {pos + 1} of {live.length}
          </span>
          <Button variant="ghost-muted" size="icon-xs" className="-mr-1.5 -mt-1 ml-auto" aria-label="Close the tour" title="Close (Esc)" onClick={onSkip}>
            <X />
          </Button>
        </div>
        <div key={step.id} className="tour-text flex flex-col gap-1">
          <h2 id={titleId} className="text-balance font-medium text-[15px] text-foreground tracking-[-0.01em]">
            {text.title}
          </h2>
          <p id={bodyId} className="text-pretty text-[13px] text-muted-foreground leading-relaxed">
            <Body text={text.body} />
          </p>
        </div>
        <div className="mt-1 flex items-center gap-1.5">
          <ol className="tour-dots mr-auto flex items-center gap-1" aria-label="Steps">
            {live.map(({ s, i }) => (
              <li key={s.id} className="tour-dot" aria-current={i === index ? "step" : undefined}>
                <span className="sr-only">{stepText(s, mod).title}</span>
              </li>
            ))}
          </ol>
          <Button variant="ghost-muted" size="xs" onClick={onSkip}>
            Skip
          </Button>
          <Button variant="outline" size="xs" disabled={!canBack} onClick={back}>
            <ArrowLeft />
            Back
          </Button>
          <Button ref={next} size="xs" onClick={forward}>
            {isLast ? last : "Next"}
            {isLast ? <Check /> : <ArrowRight />}
          </Button>
        </div>
      </div>
    </div>
  );
}

