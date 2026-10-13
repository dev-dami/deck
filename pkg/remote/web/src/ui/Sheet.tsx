import type { ComponentChildren } from "preact";
import { useEffect, useRef } from "preact/hooks";

interface SheetProps {
  title: string;
  onClose?: (() => void) | undefined;
  children: ComponentChildren;
  // Modal sheets that demand an answer (approvals) are not dismissed by the backdrop.
  persistent?: boolean;
}

export function Sheet({ title, onClose, children, persistent }: SheetProps) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement;
    ref.current?.focus();
    return () => {
      if (prev instanceof HTMLElement) prev.focus();
    };
  }, []);
  useEffect(() => {
    if (onClose === undefined || persistent === true) return;
    const on = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [onClose, persistent]);
  const dismiss = persistent === true ? undefined : onClose;
  return (
    <div class="sheet-root">
      <button
        type="button"
        class="backdrop"
        tabIndex={-1}
        aria-label="Close"
        onClick={dismiss}
        disabled={dismiss === undefined}
      />
      <div class="sheet" role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} ref={ref}>
        <div class="sheet-head">
          <h2>{title}</h2>
          {dismiss !== undefined ? (
            <button type="button" class="btn ghost" onClick={dismiss}>
              Close
            </button>
          ) : null}
        </div>
        <div class="sheet-body">{children}</div>
      </div>
    </div>
  );
}
