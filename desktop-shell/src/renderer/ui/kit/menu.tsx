import { type ComponentProps, type ReactNode, useEffect, useRef } from "react";
import { cn } from "../../lib/cn";

// A popup anchored to its trigger: it closes on a click outside and on
// Escape. side says whether it opens above (the composer's menus) or below.
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
  return (
    <div ref={ref} className="relative inline-flex">
      {trigger}
      {open && (
        <div
          role={role}
          aria-label={label}
          className={cn(
            "absolute z-50 min-w-48 overflow-hidden rounded-xl border bg-popover p-1 text-foreground shadow-xl shadow-black/30",
            side === "top" ? "bottom-full mb-1.5" : "top-full mt-1.5",
            align === "start" ? "left-0" : "right-0",
            className,
          )}
        >
          {children}
        </div>
      )}
    </div>
  );
}

export function MenuLabel({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-2 pt-1.5 pb-1 font-medium text-2xs text-muted-foreground", className)} {...props} />;
}

export function MenuItem({ className, type, ...props }: ComponentProps<"button">) {
  return (
    <button
      type={type ?? "button"}
      role="menuitem"
      className={cn(
        "flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] outline-none hover:bg-accent focus-visible:bg-accent disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-3.5 [&_svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}

export function MenuSeparator() {
  return <div className="-mx-1 my-1 h-px bg-border" />;
}
