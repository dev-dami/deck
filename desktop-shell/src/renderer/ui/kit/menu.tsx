import { type ComponentProps, type ReactNode, useEffect, useLayoutEffect, useRef } from "react";
import { cn } from "../../lib/cn";
import { moveIndex, restoreFocus } from "./focus";

// How near a window edge a popover may come, in pixels.
const EDGE = 8;

const ITEMS = '[role="menuitem"]:not(:disabled), [role="menuitemradio"]:not(:disabled)';

// A popup anchored to its trigger: it closes on a click outside and on
// Escape. side says whether it opens above (the composer's menus) or below.
// A menu takes the focus on its first item (its first checked one, when it
// has radio items), moves it with the arrows, Home and End, and gives it
// back to the trigger when it closes. It stays inside the window: one
// that would run past an edge (a bell in a sidebar on the right) slides
// back in.
export function Popover({
  open,
  onClose,
  trigger,
  children,
  side = "bottom",
  align = "start",
  className,
  label,
  role = "menu",
}: {
  open: boolean;
  onClose: () => void;
  trigger: ReactNode;
  children: ReactNode;
  side?: "top" | "bottom";
  align?: "start" | "end";
  className?: string;
  label: string;
  role?: "menu" | "dialog" | "listbox";
}) {
  const ref = useRef<HTMLDivElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const down = (e: MouseEvent) => {
      if (ref.current !== null && !ref.current.contains(e.target as Node)) onClose();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", key);
    };
  }, [open, onClose]);
  useEffect(() => {
    if (!open) return;
    const back = document.activeElement;
    const popup = pop.current;
    if (role === "menu") (popup?.querySelector<HTMLElement>('[role="menuitemradio"][aria-checked="true"]') ?? popup?.querySelector<HTMLElement>(ITEMS))?.focus();
    return () => restoreFocus(back, popup);
  }, [open, role]);
  useLayoutEffect(() => {
    const popup = pop.current;
    if (!open || popup === null) return;
    const fit = () => {
      popup.style.transform = "";
      const r = popup.getBoundingClientRect();
      const room = document.documentElement.clientWidth - EDGE;
      const dx = r.right > room ? room - r.right : r.left < EDGE ? EDGE - r.left : 0;
      if (dx !== 0) popup.style.transform = `translateX(${Math.max(dx, EDGE - r.left)}px)`;
    };
    fit();
    window.addEventListener("resize", fit);
    return () => window.removeEventListener("resize", fit);
  }, [open]);
  return (
    <div ref={ref} className="relative inline-flex">
      {trigger}
      {open && (
        <div
          ref={pop}
          role={role}
          aria-label={label}
          className={cn(
            "material absolute z-50 min-w-48 animate-enter overflow-hidden rounded-[14px] p-1.5 text-foreground",
            side === "top" ? "bottom-full mb-1.5" : "top-full mt-1.5",
            align === "start" ? "left-0" : "right-0",
            className,
          )}
          onKeyDown={(e) => {
            if (role !== "menu") return;
            const items = [...e.currentTarget.querySelectorAll<HTMLElement>(ITEMS)];
            const next = moveIndex(e.key, items.indexOf(document.activeElement as HTMLElement), items.length);
            if (next === null) return;
            e.preventDefault();
            items[next]?.focus();
          }}
        >
          {children}
        </div>
      )}
    </div>
  );
}

export function MenuLabel({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-2 pt-1.5 pb-1 font-medium text-2xs text-faint", className)} {...props} />;
}

export function MenuItem({ className, type, ...props }: ComponentProps<"button">) {
  return (
    <button
      type={type ?? "button"}
      role="menuitem"
      className={cn(
        "flex h-7 w-full cursor-pointer items-center gap-2 rounded-[7px] px-2 text-left text-sm outline-none transition-colors hover:bg-hover focus-visible:bg-selected disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-3.5 [&_svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}

export function MenuSeparator() {
  return <div className="mx-1 my-1 h-px bg-hairline" />;
}
