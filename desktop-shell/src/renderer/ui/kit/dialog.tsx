import { X } from "lucide-react";
import { type ReactNode, useEffect } from "react";
import { cn } from "../../lib/cn";
import { Button } from "./button";

// A modal over the window: Escape and a click outside close it.
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
        role="dialog"
        aria-label={title}
        className={cn("flex w-[480px] max-w-[calc(100vw-2rem)] flex-col rounded-2xl border bg-popover shadow-2xl shadow-black/40", className)}
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

export const fieldClass =
  "w-full rounded-lg border border-input bg-background px-3 py-2 text-sm outline-none placeholder:text-muted-foreground/70 focus:border-primary";
