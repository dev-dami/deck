import type { ReactNode } from "react";

// After T3 Code's settings layout: a section title, then a rounded group
// of rows, each with a title, a description and a control on the right.
export function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-2.5">
      <div className="flex min-h-7 items-center px-4">
        <h2 className="font-medium text-2xs text-faint">{title}</h2>
      </div>
      <div className="rounded-xl border border-hairline bg-surface-1 inset-shadow-hl [&>*+*]:border-hairline [&>*+*]:border-t">
        {children}
      </div>
    </section>
  );
}

export function Row({ title, description, control }: { title: ReactNode; description?: ReactNode; control?: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between" data-slot="settings-row">
      <div className="min-w-0 space-y-1">
        <h3 className="font-medium text-sm">{title}</h3>
        {description !== undefined && <p className="text-muted-foreground text-xs leading-relaxed">{description}</p>}
      </div>
      {control !== undefined && <div className="flex shrink-0 items-center gap-2">{control}</div>}
    </div>
  );
}
