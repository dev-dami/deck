import { X } from "lucide-react";
import { type ReactNode, useEffect, useRef } from "react";
import { cn } from "../../lib/cn";
import { Button } from "./button";
import { useFocusTrap } from "./focus";

// A modal over the window: Escape and a click outside close it. Tab stays
// inside, and the focus goes back where it was when it closes.
export function Dialog({
  title,
  description,
  onClose,
  children,
  footer,
  className,
}: {
  title: string;
  description?: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useFocusTrap(ref);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/45 pt-[12vh] backdrop-blur-[2px]" onMouseDown={onClose} role="presentation">
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className={cn(
          "flex w-[480px] max-w-[calc(100vw-2rem)] animate-enter flex-col rounded-2xl border border-hairline-strong bg-surface-2 shadow-popover inset-shadow-hl outline-none",
          className,
        )}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <header className="flex items-start gap-3 px-5 pt-4 pb-2">
          <div className="min-w-0 flex-1">
            <h2 className="font-medium text-base">{title}</h2>
            {description !== undefined && <p className="mt-1 text-muted-foreground text-xs leading-relaxed">{description}</p>}
          </div>
          <Button variant="ghost-muted" size="icon-xs" aria-label="Close" onClick={onClose}>
            <X />
          </Button>
        </header>
        <div className="flex flex-col gap-3 px-5 py-2">{children}</div>
        {footer !== undefined && <footer className="flex items-center justify-end gap-2 px-5 pt-2 pb-4">{footer}</footer>}
      </div>
    </div>
  );
}

// A text field: a hairline, and an amber ring at 40% while it has the focus.
export const fieldClass =
  "w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none transition-[border-color,box-shadow] placeholder:text-faint focus:border-primary/50 focus:ring-[3px] focus:ring-ring";
