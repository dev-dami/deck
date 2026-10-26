import { X } from "../icons";
import { type ReactNode, useEffect, useRef } from "react";
import { cn } from "../../lib/cn";
import { Button } from "./button";
import { useFocusTrap } from "./focus";

// A modal over the window: Escape and a click outside close it. Tab stays
// inside, and the focus goes back where it was when it closes. A body taller
// than the window scrolls between the header and the footer.
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
  children?: ReactNode;
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
    <div className="scrim fixed inset-0 z-50 flex items-start justify-center pt-[12vh]" onMouseDown={onClose} role="presentation">
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className={cn(
          "material flex max-h-[calc(88vh-1rem)] w-[480px] max-w-[calc(100vw-2rem)] animate-enter flex-col rounded-[22px] outline-none",
          className,
        )}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <header className="flex items-start gap-3 px-5 pt-5 pb-2">
          <div className="min-w-0 flex-1">
            <h2 className="text-balance font-semibold text-base">{title}</h2>
            {description !== undefined && <p className="mt-1 text-muted-foreground text-xs leading-relaxed">{description}</p>}
          </div>
          <Button variant="ghost-muted" size="icon-xs" aria-label="Close" onClick={onClose}>
            <X />
          </Button>
        </header>
        {children !== undefined && <div className="flex min-h-0 flex-col gap-3 overflow-y-auto px-5 py-2">{children}</div>}
        {footer !== undefined && <footer className="flex items-center justify-end gap-2 px-5 pt-3 pb-5">{footer}</footer>}
      </div>
    </div>
  );
}

// The question before a destructive action. The title and the description
// name what it acts on; the button names the action.
export function Confirm({
  title,
  description,
  action,
  onConfirm,
  onClose,
}: {
  title: string;
  description: ReactNode;
  action: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Dialog
      title={title}
      description={description}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => {
              onClose();
              onConfirm();
            }}
          >
            {action}
          </Button>
        </>
      }
    />
  );
}

// A text field: a recessed well with a hairline, and the accent's ring
// while it has the focus.
export const fieldClass =
  "w-full rounded-[10px] border border-input bg-hover px-3 py-2 text-sm outline-none transition-[border-color,box-shadow] placeholder:text-faint focus:border-primary/50 focus:ring-[3px] focus:ring-ring";
